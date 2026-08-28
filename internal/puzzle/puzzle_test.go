package puzzle

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluationAcceptance(t *testing.T) {
	// Acceptance is checked directly on the criteria rather than through a
	// Monte Carlo, so the rule is pinned independently of the simulation.
	crit := Criteria{MinWinRate: 0.35, MaxWinRate: 0.65, MinDecisionSpread: 0.018}

	tests := []struct {
		name     string
		defend   float64
		chase    float64
		spread   float64
		accept   bool
		mentions string
	}{
		{"balanced and interesting", 0.50, 0.48, 0.022, true, ""},
		{"at the edges of the band", 0.35, 0.65, 0.018, true, ""},
		{"defend too easy", 0.80, 0.50, 0.022, false, "defend rate"},
		{"defend too hard", 0.10, 0.50, 0.022, false, "defend rate"},
		{"chase too easy", 0.50, 0.85, 0.022, false, "chase rate"},
		{"balanced but decided", 0.50, 0.50, 0.004, false, "decision spread"},
		{"wrong in every way", 0.90, 0.05, 0.001, false, "decision spread"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := judge(tc.defend, tc.chase, tc.spread, crit)
			if ev.Accepted != tc.accept {
				t.Errorf("accepted = %v, want %v (%v)", ev.Accepted, tc.accept, ev.Rejections)
			}
			if tc.mentions != "" {
				joined := strings.Join(ev.Rejections, " ")
				if !strings.Contains(joined, tc.mentions) {
					t.Errorf("rejections %q do not mention %q", joined, tc.mentions)
				}
			}
		})
	}
}

// TestScorePrefersInterestingDays checks the ranking among accepted candidates:
// balance is a threshold, but what separates a good day from a dull one is how
// much the decisions mattered.
func TestScorePrefersInterestingDays(t *testing.T) {
	dull := Evaluation{DefendRate: 0.50, ChaseRate: 0.50, DecisionSpread: 0.018}
	sharp := Evaluation{DefendRate: 0.58, ChaseRate: 0.44, DecisionSpread: 0.030}
	if sharp.Score() <= dull.Score() {
		t.Errorf("a perfectly balanced day with no decisions in it (%.4f) outranked "+
			"a slightly lopsided one where the choices mattered (%.4f)",
			dull.Score(), sharp.Score())
	}

	// Between two equally interesting days, the better balanced one wins.
	a := Evaluation{DefendRate: 0.50, ChaseRate: 0.50, DecisionSpread: 0.025}
	b := Evaluation{DefendRate: 0.64, ChaseRate: 0.36, DecisionSpread: 0.025}
	if a.Score() <= b.Score() {
		t.Errorf("balance was not the tie-breaker: %.4f vs %.4f", a.Score(), b.Score())
	}
}

func TestQueueRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "puzzles.json")
	q := &Queue{
		Criteria: DefaultCriteria,
		Puzzles: []Queued{
			{Date: "2026-09-03", Target: 190, Venue: "Somewhere"},
			{Date: "2026-09-01", Target: 175, Venue: "Elsewhere"},
			{Date: "2026-09-02", Target: 182, Venue: "Elsewhere"},
		},
	}
	if err := q.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadQueue(path)
	if err != nil {
		t.Fatalf("LoadQueue: %v", err)
	}
	if len(got.Puzzles) != 3 {
		t.Fatalf("got %d puzzles, want 3", len(got.Puzzles))
	}
	// The file must read chronologically, so a human can see what is coming.
	for i := 1; i < len(got.Puzzles); i++ {
		if got.Puzzles[i-1].Date > got.Puzzles[i].Date {
			t.Errorf("queue is not sorted: %s before %s", got.Puzzles[i-1].Date, got.Puzzles[i].Date)
		}
	}

	p, ok := got.For("2026-09-02")
	if !ok || p.Target != 182 {
		t.Errorf("For(2026-09-02) = %+v, %v", p, ok)
	}
	if _, ok := got.For("2030-01-01"); ok {
		t.Error("For returned a puzzle for an unscheduled date")
	}
	if got.Generated == "" {
		t.Error("the queue records no generation time")
	}
}

func TestLoadQueueRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadQueue(path); err == nil {
		t.Error("LoadQueue accepted a file that is not JSON")
	}
	if _, err := LoadQueue(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("LoadQueue accepted a missing file")
	}
}

func TestAbsf(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{{-2, 2}, {2, 2}, {0, 0}} {
		if got := absf(tc.in); got != tc.want || math.Signbit(got) {
			t.Errorf("absf(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
