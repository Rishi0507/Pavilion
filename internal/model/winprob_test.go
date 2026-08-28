package model

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func loadWP(tb testing.TB) *WinProb {
	tb.Helper()
	dir := filepath.Join("..", "..", "data", "models")
	mp := filepath.Join(dir, "winprob.txt")
	if _, err := os.Stat(mp); err != nil {
		tb.Skipf("win probability model not built; run make model")
	}
	w, err := LoadWinProb(mp, filepath.Join(dir, "winprob.json"))
	if err != nil {
		tb.Fatalf("LoadWinProb: %v", err)
	}
	return w
}

// TestWinProbParity holds the Go implementation to the probabilities Python
// produced, on the same held-out rows.
func TestWinProbParity(t *testing.T) {
	w := loadWP(t)
	path := filepath.Join("..", "..", "data", "models", "winprob_parity.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no parity fixtures: %v", err)
	}
	var fx struct {
		Features []string `json:"features"`
		Rows     []struct {
			X []float64 `json:"x"`
			P float64   `json:"p"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	for i, n := range fx.Features {
		if n != WinProbFeatures[i] {
			t.Fatalf("feature %d is %q in the fixtures, %q in Go", i, n, WinProbFeatures[i])
		}
	}

	const tol = 1e-6
	var worst float64
	for _, row := range fx.Rows {
		got := w.PredictRaw(row.X)
		if d := math.Abs(got - row.P); d > worst {
			worst = d
		}
	}
	if worst > tol {
		t.Errorf("worst divergence %.3g exceeds %.0e", worst, tol)
	} else {
		t.Logf("%d fixtures, worst divergence %.3g", len(fx.Rows), worst)
	}
}

// TestWinProbRules covers the cases decided by cricket rather than by the model.
func TestWinProbRules(t *testing.T) {
	w := loadWP(t)
	tests := []struct {
		name string
		s    Situation
		want float64
	}{
		{"already won", Situation{RunsRequired: 0, BallsRemaining: 6, WicketsInHand: 5, Target: 180}, 1},
		{"target passed", Situation{RunsRequired: -4, BallsRemaining: 6, WicketsInHand: 5, Target: 180}, 1},
		{"all out", Situation{RunsRequired: 10, BallsRemaining: 6, WicketsInHand: 0, Target: 180}, 0},
		{"out of balls", Situation{RunsRequired: 1, BallsRemaining: 0, WicketsInHand: 5, Target: 180}, 0},
		{"arithmetically impossible", Situation{RunsRequired: 40, BallsRemaining: 6, WicketsInHand: 8, Target: 180}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Predict(tc.s); got != tc.want {
				t.Errorf("Predict(%+v) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestWinProbIsMonotone re-checks in Go what the training script constrained.
// The share grid is a derivative of this model, so a probability that moves the
// wrong way would colour a good over red, which players notice immediately.
func TestWinProbIsMonotone(t *testing.T) {
	w := loadWP(t)
	base := Situation{RunsRequired: 60, BallsRemaining: 60, WicketsInHand: 6, Target: 180, VenueRunRate: 1.25}

	t.Run("needing more runs never helps", func(t *testing.T) {
		prev := 2.0
		for need := 5; need <= 200; need += 5 {
			s := base
			s.RunsRequired = need
			p := w.Predict(s)
			if p > prev+1e-12 {
				t.Fatalf("needing %d gives %.4f, more than %.4f at %d fewer runs", need, p, prev, need-5)
			}
			prev = p
		}
	})

	t.Run("more balls never hurts", func(t *testing.T) {
		prev := -1.0
		for balls := 6; balls <= 120; balls += 6 {
			s := base
			s.BallsRemaining = balls
			p := w.Predict(s)
			if p < prev-1e-12 {
				t.Fatalf("%d balls gives %.4f, less than %.4f with six fewer", balls, p, prev)
			}
			prev = p
		}
	})

	t.Run("more wickets never hurts", func(t *testing.T) {
		prev := -1.0
		for wkts := 1; wkts <= 10; wkts++ {
			s := base
			s.WicketsInHand = wkts
			p := w.Predict(s)
			if p < prev-1e-12 {
				t.Fatalf("%d wickets gives %.4f, less than %.4f with one fewer", wkts, p, prev)
			}
			prev = p
		}
	})
}

// TestWinProbIsSane checks the model against situations any cricket follower
// would call the same way.
func TestWinProbIsSane(t *testing.T) {
	w := loadWP(t)
	v := 1.25

	cruising := w.Predict(Situation{RunsRequired: 20, BallsRemaining: 60, WicketsInHand: 8, Target: 180, VenueRunRate: v})
	if cruising < 0.9 {
		t.Errorf("20 off 60 with 8 in hand = %.3f, expected near certain", cruising)
	}
	desperate := w.Predict(Situation{RunsRequired: 60, BallsRemaining: 12, WicketsInHand: 2, Target: 180, VenueRunRate: v})
	if desperate > 0.1 {
		t.Errorf("60 off 12 with 2 in hand = %.3f, expected near hopeless", desperate)
	}
	tight := w.Predict(Situation{RunsRequired: 12, BallsRemaining: 6, WicketsInHand: 5, Target: 180, VenueRunRate: v})
	if tight < 0.15 || tight > 0.85 {
		t.Errorf("12 off the last over with 5 in hand = %.3f, expected a genuine contest", tight)
	}
}
