package model

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// WinProbFeatures is the feature order the win probability model expects. It is
// short and situational by design: a chase is decided by how much is needed,
// how long there is, and how many batters are left, and adding player identity
// would make the share grid react to who happens to be at the crease rather
// than to the decision that was just taken.
var WinProbFeatures = []string{
	"runs_required",
	"balls_remaining",
	"wickets_in_hand",
	"required_rate",
	"target",
	"venue_run_rate",
}

// WinProb predicts the chasing side's probability of winning.
//
// It is the model that does the most work in the game: it colours every square
// of the share grid, it chooses the AI batting side's intent, and it scores the
// player on decision quality rather than on how the coin landed.
type WinProb struct {
	m *Outcome // the tree ensemble; binary models have a single class
}

// LoadWinProb reads the win probability model.
func LoadWinProb(modelPath, metaPath string) (*WinProb, error) {
	if metaPath != "" {
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			return nil, fmt.Errorf("model: read %s: %w", metaPath, err)
		}
		var meta struct {
			Features []string `json:"features"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("model: parse %s: %w", metaPath, err)
		}
		if len(meta.Features) > 0 {
			if len(meta.Features) != len(WinProbFeatures) {
				return nil, fmt.Errorf("model: win probability trained on %d features, Go supplies %d",
					len(meta.Features), len(WinProbFeatures))
			}
			for i := range meta.Features {
				if meta.Features[i] != WinProbFeatures[i] {
					return nil, fmt.Errorf("model: win probability feature %d is %q, Go supplies %q",
						i, meta.Features[i], WinProbFeatures[i])
				}
			}
		}
	}

	m, err := parse(modelPath)
	if err != nil {
		return nil, err
	}
	if m.numClass != 1 {
		return nil, fmt.Errorf("model: win probability model has %d classes, want 1", m.numClass)
	}
	m.temperature = 1
	return &WinProb{m: m}, nil
}

// Trees returns the ensemble size.
func (w *WinProb) Trees() int { return w.m.Trees() }

// Situation is a chase in progress, as the win probability model sees it.
type Situation struct {
	RunsRequired   int
	BallsRemaining int
	WicketsInHand  int
	Target         int
	VenueRunRate   float64
}

// Features writes the situation into the model's feature order.
func (s Situation) Features(dst []float64) {
	rate := 0.0
	if s.BallsRemaining > 0 {
		rate = 6 * float64(s.RunsRequired) / float64(s.BallsRemaining)
	}
	dst[0] = float64(s.RunsRequired)
	dst[1] = float64(s.BallsRemaining)
	dst[2] = float64(s.WicketsInHand)
	dst[3] = rate
	dst[4] = float64(s.Target)
	dst[5] = s.VenueRunRate
}

// Predict returns the chasing side's probability of winning.
//
// Three cases are decided by the rules of cricket rather than by the model,
// because a decision tree cannot extrapolate and would otherwise answer them
// from whichever leaf its inputs happened to land in:
//
//   - the target has been reached, so the chase is already won;
//   - there are no wickets or no balls left, so it is already lost;
//   - more runs are needed than there are balls to score six off, which is not
//     unlikely but arithmetically impossible.
//
// Stating them directly is both more accurate and more honest than hoping a
// deeper tree would have found them. The same three rules are applied in the
// training script, so the two sides continue to agree exactly.
func (w *WinProb) Predict(s Situation) float64 {
	if s.RunsRequired <= 0 {
		return 1
	}
	if s.BallsRemaining <= 0 || s.WicketsInHand <= 0 {
		return 0
	}
	if s.RunsRequired > 6*s.BallsRemaining {
		return 0
	}

	x := make([]float64, len(WinProbFeatures))
	s.Features(x)
	return w.raw(x)
}

// PredictRaw evaluates the ensemble on an explicit feature vector, applying the
// impossibility rule but nothing else. It exists for the parity test, which
// must compare exactly what Python computed.
func (w *WinProb) PredictRaw(x []float64) float64 {
	if x[0] > 6*x[1] {
		return 0
	}
	return w.raw(x)
}

func (w *WinProb) raw(x []float64) float64 {
	var score [1]float64
	w.m.RawScore(x, score[:])
	// LightGBM's binary objective reports a logit with a sigmoid scale of one,
	// which the model file records explicitly.
	return 1 / (1 + math.Exp(-score[0]))
}
