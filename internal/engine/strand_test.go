package engine

import (
	"testing"

	"manhattan/internal/sim"
)

// TestNeverStrandedWithRealModel plays many innings against the real outcome
// model, choosing legally but adversarially, and asserts a legal bowler always
// exists until the innings is genuinely over.
//
// The engine's own test covers this with a synthetic predictor. This one uses
// the shipped model, because the stranding condition depends on how long the
// innings lasts, and a synthetic predictor that ends every innings early would
// never reach the overs where the constraint bites.
func TestNeverStrandedWithRealModel(t *testing.T) {
	e := testEngine(t)
	key, _ := sim.DeriveDailyKey([]byte("strand"), "2026-08-28")

	// A target high enough that the chase rarely finishes early, so the full
	// twenty overs actually get bowled.
	p, err := e.BuildPuzzle("2026-08-28", key, 400)
	if err != nil {
		t.Fatal(err)
	}

	for trial := range 40 {
		k := key
		k[0] ^= byte(trial)
		s := sim.NewChase(p)

		for !s.Done {
			legal := s.LegalBowlers()
			if len(legal) == 0 {
				t.Fatalf("trial %d: stranded at over %d, overs bowled %v, last %d",
					trial, s.Over, s.OversBowled, s.LastBowler)
			}
			// Pick the last legal option, which is the choice most likely to
			// paint the innings into a corner.
			intent, err := e.ChooseIntent(s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sim.PlayOver(s, k, legal[len(legal)-1], intent, e); err != nil {
				t.Fatalf("trial %d over %d: %v", trial, s.Over, err)
			}
		}
		if s.Over != sim.MaxOvers && !s.Won() && s.Wickets < sim.Wickets {
			t.Fatalf("trial %d ended after %d overs without a result", trial, s.Over)
		}
	}
}
