// Package model serves the ball outcome model.
//
// The model is a LightGBM gradient-boosted ensemble trained in Python. It is
// evaluated here by walking the trees directly, rather than through an ONNX
// runtime.
//
// That is a deliberate departure from the brief. onnxruntime-go needs cgo and a
// bundled shared library, which turns a static Go binary into a platform-
// specific build with a native dependency, and buys nothing for a model that is
// a few hundred decision trees. Reading LightGBM's own text format is about two
// hundred lines, keeps the binary static and cross-compilable, evaluates faster
// than ONNX does for trees this small, and makes exact parity with Python
// something that can be tested to 1e-6 rather than hoped for.
package model

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// LightGBM decision_type is a bit field. Only these two bits matter for the
// trees this project trains: there are no categorical splits and no linear
// leaves, and the loader rejects a model that has them rather than silently
// mispredicting.
const (
	maskCategorical = 1
	maskDefaultLeft = 2
)

// tree is one decision tree, flattened into parallel slices.
//
// Internal nodes are indexed from zero. A child index is either a non-negative
// internal node or a negative value encoding leaf index ^child, which is
// LightGBM's own convention and is kept rather than translated so that the
// layout can be compared against the source file directly.
type tree struct {
	splitFeature []int32
	threshold    []float64
	defaultLeft  []bool
	leftChild    []int32
	rightChild   []int32
	leafValue    []float64
}

// node is one internal decision node in the flattened ensemble.
//
// Trees are parsed into per-tree slices and then flattened into one contiguous
// array. Traversal is memory-bound rather than compute-bound: the ensemble is
// most of a megabyte of nodes and a prediction touches a few cache lines in
// each of 585 trees, so the layout is worth about 20% (45 to 36 microseconds a
// prediction) and no more.
//
// That is comfortable for serving, where predictions are one ball at a time at
// human pace. Puzzle validation is the case that will need more, since it
// simulates millions of deliveries; the answer there is to predict in batches
// across simulations, iterating trees on the outside and rows on the inside, so
// each tree is loaded once for many rows. That belongs with the Monte Carlo
// work rather than here.
//
// Child indices are global: non-negative selects another node, negative encodes
// a leaf as ^child into the shared leaf array.
type node struct {
	threshold   float64
	left        int32
	right       int32
	feature     int32
	defaultLeft bool
	_           [3]byte // keep the struct a round 24 bytes
}

// Outcome is the trained ball outcome model.
type Outcome struct {
	nodes    []node
	leaves   []float64
	treeRoot []int32 // index into nodes, or -1 for a single-leaf tree
	treeLeaf []int32 // index into leaves of that tree's first leaf

	numClass    int
	features    []string
	outcomes    []string
	temperature float64
}

// NumClass returns the size of the outcome space.
func (m *Outcome) NumClass() int { return m.numClass }

// Features returns the feature names the model was trained on, in order.
func (m *Outcome) Features() []string { return m.features }

// Temperature returns the fitted calibration temperature.
func (m *Outcome) Temperature() float64 { return m.temperature }

// Trees returns the number of trees in the ensemble.
func (m *Outcome) Trees() int { return len(m.treeRoot) }

// flatten lays the parsed trees out contiguously.
func (m *Outcome) flatten(trees []tree) {
	nodes, leaves := 0, 0
	for i := range trees {
		leaves += len(trees[i].leafValue)
		if n := len(trees[i].leafValue) - 1; n > 0 {
			nodes += n
		}
	}
	m.nodes = make([]node, 0, nodes)
	m.leaves = make([]float64, 0, leaves)
	m.treeRoot = make([]int32, len(trees))
	m.treeLeaf = make([]int32, len(trees))

	for i := range trees {
		t := &trees[i]
		leafBase := int32(len(m.leaves))
		m.treeLeaf[i] = leafBase
		m.leaves = append(m.leaves, t.leafValue...)

		if len(t.leafValue) <= 1 {
			// A boosting round that found nothing to split on.
			m.treeRoot[i] = -1
			continue
		}

		nodeBase := int32(len(m.nodes))
		m.treeRoot[i] = nodeBase
		rebase := func(c int32) int32 {
			if c < 0 {
				// ^c is the tree-local leaf index; re-encode it globally.
				return ^(leafBase + ^c)
			}
			return nodeBase + c
		}
		for j := range t.splitFeature {
			m.nodes = append(m.nodes, node{
				threshold:   t.threshold[j],
				left:        rebase(t.leftChild[j]),
				right:       rebase(t.rightChild[j]),
				feature:     t.splitFeature[j],
				defaultLeft: t.defaultLeft[j],
			})
		}
	}
}

// Meta mirrors the JSON the training script writes alongside the model.
type Meta struct {
	Features    []string `json:"features"`
	Outcomes    []string `json:"outcomes"`
	Temperature float64  `json:"temperature"`
	NumClass    int      `json:"num_class"`
}

// Load reads a LightGBM text model and the metadata beside it.
//
// wantFeatures is the feature order the caller will supply. Loading fails if it
// disagrees with what the model was trained on, because a silently permuted
// feature vector produces confident nonsense rather than an error.
func Load(modelPath, metaPath string, wantFeatures []string) (*Outcome, error) {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("model: read %s: %w", metaPath, err)
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("model: parse %s: %w", metaPath, err)
	}

	m, err := parse(modelPath)
	if err != nil {
		return nil, err
	}
	m.temperature = meta.Temperature
	m.outcomes = meta.Outcomes
	if m.temperature <= 0 {
		return nil, fmt.Errorf("model: temperature %v in %s must be positive", m.temperature, metaPath)
	}
	if meta.NumClass != 0 && meta.NumClass != m.numClass {
		return nil, fmt.Errorf("model: %s says %d classes, %s has %d",
			metaPath, meta.NumClass, modelPath, m.numClass)
	}

	if len(wantFeatures) > 0 {
		if len(wantFeatures) != len(m.features) {
			return nil, fmt.Errorf("model: trained on %d features, caller supplies %d",
				len(m.features), len(wantFeatures))
		}
		for i := range wantFeatures {
			if wantFeatures[i] != m.features[i] {
				return nil, fmt.Errorf("model: feature %d is %q in the model but %q in the caller",
					i, m.features[i], wantFeatures[i])
			}
		}
	}
	return m, nil
}

func parse(path string) (*Outcome, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("model: open %s: %w", path, err)
	}
	defer f.Close()

	m := &Outcome{numClass: 1}
	var trees []tree
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	var cur *tree
	var numLeaves int
	line := 0

	commit := func() error {
		if cur == nil {
			return nil
		}
		if len(cur.leafValue) != numLeaves {
			return fmt.Errorf("model: tree %d has %d leaf values, expected %d",
				len(trees), len(cur.leafValue), numLeaves)
		}
		if numLeaves > 1 {
			n := numLeaves - 1
			if len(cur.splitFeature) != n || len(cur.threshold) != n ||
				len(cur.leftChild) != n || len(cur.rightChild) != n ||
				len(cur.defaultLeft) != n {
				return fmt.Errorf("model: tree %d has inconsistent internal node arrays", len(trees))
			}
		}
		trees = append(trees, *cur)
		cur = nil
		return nil
	}

	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "end of trees" {
			break
		}
		if text == "" {
			continue
		}

		key, value, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}

		switch key {
		case "num_class":
			if m.numClass, err = strconv.Atoi(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "feature_names":
			m.features = strings.Fields(value)
		case "Tree":
			if err := commit(); err != nil {
				return nil, err
			}
			cur = &tree{}
		case "num_leaves":
			if cur == nil {
				continue
			}
			if numLeaves, err = strconv.Atoi(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "num_cat":
			if n, err := strconv.Atoi(value); err == nil && n != 0 {
				return nil, errors.New("model: categorical splits are not supported")
			}
		case "is_linear":
			if v, err := strconv.Atoi(value); err == nil && v != 0 {
				return nil, errors.New("model: linear-leaf trees are not supported")
			}
		case "split_feature":
			if cur == nil {
				continue
			}
			if cur.splitFeature, err = parseInts(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "threshold":
			if cur == nil {
				continue
			}
			if cur.threshold, err = parseFloats(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "decision_type":
			if cur == nil {
				continue
			}
			types, err := parseInts(value)
			if err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
			cur.defaultLeft = make([]bool, len(types))
			for i, d := range types {
				if d&maskCategorical != 0 {
					return nil, errors.New("model: categorical splits are not supported")
				}
				cur.defaultLeft[i] = d&maskDefaultLeft != 0
			}
		case "left_child":
			if cur == nil {
				continue
			}
			if cur.leftChild, err = parseInts(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "right_child":
			if cur == nil {
				continue
			}
			if cur.rightChild, err = parseInts(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		case "leaf_value":
			if cur == nil {
				continue
			}
			if cur.leafValue, err = parseFloats(value); err != nil {
				return nil, fmt.Errorf("model: %s line %d: %w", path, line, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("model: read %s: %w", path, err)
	}
	if err := commit(); err != nil {
		return nil, err
	}

	if len(trees) == 0 {
		return nil, fmt.Errorf("model: %s contains no trees", path)
	}
	if m.numClass < 1 {
		return nil, fmt.Errorf("model: %s has num_class %d", path, m.numClass)
	}
	if len(trees)%m.numClass != 0 {
		return nil, fmt.Errorf("model: %d trees is not a multiple of %d classes", len(trees), m.numClass)
	}
	m.flatten(trees)
	return m, nil
}

func parseInts(s string) ([]int32, error) {
	fields := strings.Fields(s)
	out := make([]int32, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseInt(f, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", f, err)
		}
		out[i] = int32(v)
	}
	return out, nil
}

func parseFloats(s string) ([]float64, error) {
	fields := strings.Fields(s)
	out := make([]float64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", f, err)
		}
		out[i] = v
	}
	return out, nil
}

// RawScore accumulates the ensemble margin for each class.
//
// LightGBM interleaves trees by class: tree i contributes to class i mod
// numClass. The boost-from-average initial score is already folded into the
// first round's leaf values, so there is nothing to add here.
func (m *Outcome) RawScore(x []float64, dst []float64) {
	for i := range dst {
		dst[i] = 0
	}
	nodes, leaves := m.nodes, m.leaves
	class := 0
	for i, root := range m.treeRoot {
		if root < 0 {
			dst[class] += leaves[m.treeLeaf[i]]
		} else {
			n := root
			for {
				nd := &nodes[n]
				v := x[nd.feature]
				goLeft := v <= nd.threshold
				if v != v { // NaN
					goLeft = nd.defaultLeft
				}
				if goLeft {
					n = nd.left
				} else {
					n = nd.right
				}
				if n < 0 {
					dst[class] += leaves[^n]
					break
				}
			}
		}
		class++
		if class == m.numClass {
			class = 0
		}
	}
}

// Predict returns the calibrated probability of each outcome.
//
// The temperature divides the margins before the softmax, which softens an
// overconfident ensemble without changing the ordering of its predictions.
func (m *Outcome) Predict(x []float64, dst []float64) {
	m.RawScore(x, dst)

	maxZ := math.Inf(-1)
	for i := range dst {
		dst[i] /= m.temperature
		if dst[i] > maxZ {
			maxZ = dst[i]
		}
	}
	total := 0.0
	for i := range dst {
		dst[i] = math.Exp(dst[i] - maxZ)
		total += dst[i]
	}
	for i := range dst {
		dst[i] /= total
	}
}

// PredictFloat32 is the convenience the feature extractor feeds directly.
// Widening float32 to float64 is exact, so this matches training precisely.
func (m *Outcome) PredictFloat32(x []float32, dst []float64) {
	buf := make([]float64, len(x))
	for i, v := range x {
		buf[i] = float64(v)
	}
	m.Predict(buf, dst)
}
