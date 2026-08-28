package model

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"manhattan/internal/features"
)

func paths(tb testing.TB) (modelPath, metaPath, parityPath string) {
	tb.Helper()
	dir := filepath.Join("..", "..", "data", "models")
	modelPath = filepath.Join(dir, "outcome.txt")
	metaPath = filepath.Join(dir, "outcome.json")
	parityPath = filepath.Join(dir, "parity.json")
	for _, p := range []string{modelPath, metaPath, parityPath} {
		if _, err := os.Stat(p); err != nil {
			tb.Skipf("model artifacts not built (%s); run make model", p)
		}
	}
	return modelPath, metaPath, parityPath
}

func load(tb testing.TB) *Outcome {
	tb.Helper()
	modelPath, metaPath, _ := paths(tb)
	m, err := Load(modelPath, metaPath, features.Names)
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return m
}

// TestPythonGoParity is the requirement from the quality bar: the same features
// in must give the same probabilities out, to 1e-6.
//
// The fixtures are held-out rows with the probabilities the Python model
// actually produced at export time. If the two ever diverge, the game would be
// simulating something other than what was trained and calibrated, and the
// divergence would be invisible from the outside.
func TestPythonGoParity(t *testing.T) {
	m := load(t)
	_, _, parityPath := paths(t)

	raw, err := os.ReadFile(parityPath)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var fx struct {
		Features    []string `json:"features"`
		Outcomes    []string `json:"outcomes"`
		Temperature float64  `json:"temperature"`
		Rows        []struct {
			X []float64 `json:"x"`
			P []float64 `json:"p"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(fx.Rows) == 0 {
		t.Fatal("no parity fixtures")
	}

	// The fixture must describe the same feature contract the Go side builds.
	if len(fx.Features) != features.Dim {
		t.Fatalf("fixtures have %d features, Go builds %d", len(fx.Features), features.Dim)
	}
	for i := range fx.Features {
		if fx.Features[i] != features.Names[i] {
			t.Fatalf("feature %d is %q in the fixtures, %q in Go", i, fx.Features[i], features.Names[i])
		}
	}
	if math.Abs(fx.Temperature-m.Temperature()) > 1e-12 {
		t.Errorf("temperature %v in fixtures, %v in the model", fx.Temperature, m.Temperature())
	}

	const tol = 1e-6
	p := make([]float64, m.NumClass())
	var worst float64
	worstRow, worstClass := -1, -1

	for i, row := range fx.Rows {
		if len(row.X) != features.Dim {
			t.Fatalf("fixture %d has %d features", i, len(row.X))
		}
		m.Predict(row.X, p)

		total := 0.0
		for k := range p {
			total += p[k]
			if d := math.Abs(p[k] - row.P[k]); d > worst {
				worst, worstRow, worstClass = d, i, k
			}
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("fixture %d: probabilities sum to %.12f", i, total)
		}
	}

	if worst > tol {
		row := fx.Rows[worstRow]
		m.Predict(row.X, p)
		t.Errorf("worst divergence %.3g exceeds %.0e at row %d class %d (%s): Go %.10f, Python %.10f",
			worst, tol, worstRow, worstClass, fx.Outcomes[worstClass], p[worstClass], row.P[worstClass])
	} else {
		t.Logf("%d fixtures, worst divergence %.3g, tolerance %.0e", len(fx.Rows), worst, tol)
	}
}

func TestLoadValidatesFeatureContract(t *testing.T) {
	modelPath, metaPath, _ := paths(t)

	t.Run("a permuted feature order is rejected", func(t *testing.T) {
		bad := append([]string(nil), features.Names...)
		bad[0], bad[1] = bad[1], bad[0]
		if _, err := Load(modelPath, metaPath, bad); err == nil {
			t.Error("Load accepted a permuted feature order")
		}
	})

	t.Run("a wrong feature count is rejected", func(t *testing.T) {
		if _, err := Load(modelPath, metaPath, features.Names[:5]); err == nil {
			t.Error("Load accepted a truncated feature list")
		}
	})

	t.Run("a missing model is an error", func(t *testing.T) {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.txt"), metaPath, nil); err == nil {
			t.Error("Load accepted a missing model file")
		}
	})

	t.Run("a corrupt model is an error", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.txt")
		if err := os.WriteFile(p, []byte("not a lightgbm model"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p, metaPath, nil); err == nil {
			t.Error("Load accepted a file with no trees")
		}
	})
}

// TestPredictIsADistribution guards the property every consumer relies on: the
// simulator samples from this output, so it must be a valid distribution for
// any input, including absurd ones.
func TestPredictIsADistribution(t *testing.T) {
	m := load(t)
	p := make([]float64, m.NumClass())

	inputs := [][]float64{
		make([]float64, features.Dim),
		filled(features.Dim, 1e9),
		filled(features.Dim, -1e9),
		filled(features.Dim, math.NaN()),
	}
	for i, x := range inputs {
		m.Predict(x, p)
		total := 0.0
		for k, v := range p {
			if v < 0 || v > 1 || math.IsNaN(v) {
				t.Errorf("input %d class %d = %v", i, k, v)
			}
			total += v
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("input %d: probabilities sum to %.12f", i, total)
		}
	}
}

func filled(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestModelShape(t *testing.T) {
	m := load(t)
	if m.NumClass() != 9 {
		t.Errorf("num class = %d, want 9", m.NumClass())
	}
	if m.Trees()%m.NumClass() != 0 {
		t.Errorf("%d trees is not a multiple of %d classes", m.Trees(), m.NumClass())
	}
	if m.Temperature() <= 0 {
		t.Errorf("temperature = %v", m.Temperature())
	}
	t.Logf("%d trees over %d classes, temperature %.4f", m.Trees(), m.NumClass(), m.Temperature())
}

func BenchmarkPredict(b *testing.B) {
	m := load(b)
	x := make([]float64, features.Dim)
	for i := range x {
		x[i] = float64(i)
	}
	p := make([]float64, m.NumClass())
	b.ResetTimer()
	for b.Loop() {
		m.Predict(x, p)
	}
}
