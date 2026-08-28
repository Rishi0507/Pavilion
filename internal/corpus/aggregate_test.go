package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	mk := func(runsBat, wides, noballs uint8, wk WicketKind) *Deliveries {
		d := &Deliveries{
			RunsBat: []uint8{runsBat},
			Wides:   []uint8{wides},
			NoBalls: []uint8{noballs},
			Byes:    []uint8{0},
			LegByes: []uint8{0},
			Penalty: []uint8{0},
			Wicket:  []WicketKind{wk},
		}
		return d
	}
	tests := []struct {
		name string
		d    *Deliveries
		want Outcome
	}{
		{"dot", mk(0, 0, 0, WicketNone), Dot},
		{"single", mk(1, 0, 0, WicketNone), One},
		{"two", mk(2, 0, 0, WicketNone), Two},
		{"three", mk(3, 0, 0, WicketNone), Three},
		{"four", mk(4, 0, 0, WicketNone), Four},
		{"six", mk(6, 0, 0, WicketNone), Six},
		{"five off the bat counts as a four", mk(5, 0, 0, WicketNone), Four},
		{"wide", mk(0, 1, 0, WicketNone), Wide},
		{"no-ball", mk(0, 0, 1, WicketNone), NoBall},
		// A wicket dominates whatever else happened on the ball.
		{"wicket", mk(0, 0, 0, WicketCaught), Wicket},
		{"wicket off a wide", mk(0, 1, 0, WicketStumped), Wicket},
		{"run out after two", mk(2, 0, 0, WicketRunOut), Wicket},
		// Retired hurt does not cost a wicket, so it is not one.
		{"retired hurt is not a wicket", mk(1, 0, 0, WicketRetiredHurt), One},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.Classify(0); got != tc.want {
				t.Errorf("Classify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterPhaseBounds(t *testing.T) {
	tests := []struct {
		phase  Phase
		lo, hi uint8
	}{
		{PhasePowerplay, 0, 5},
		{PhaseMiddle, 6, 14},
		{PhaseDeath, 15, 19},
	}
	for _, tc := range tests {
		t.Run(tc.phase.String(), func(t *testing.T) {
			f := NewFilter().Phase(tc.phase)
			if f.MinOver != tc.lo || f.MaxOver != tc.hi {
				t.Errorf("%v bounds = %d-%d, want %d-%d", tc.phase, f.MinOver, f.MaxOver, tc.lo, tc.hi)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	s := volumeStore(false)

	t.Run("unfiltered", func(t *testing.T) {
		d := s.Aggregate(NewFilter())
		if d.Deliveries != 5 || d.LegalBalls != 4 {
			t.Errorf("deliveries=%d legal=%d, want 5 and 4", d.Deliveries, d.LegalBalls)
		}
		if d.RunsOffBat != 11 || d.Extras != 1 || d.TotalRuns != 12 {
			t.Errorf("runs: bat=%d extras=%d total=%d, want 11/1/12", d.RunsOffBat, d.Extras, d.TotalRuns)
		}
		if d.Wickets != 1 || d.BowlerWkts != 1 {
			t.Errorf("wickets=%d bowler=%d, want 1 and 1", d.Wickets, d.BowlerWkts)
		}
		if d.Outcomes[Four] != 1 || d.Outcomes[Six] != 1 || d.Outcomes[Wide] != 1 || d.Outcomes[Wicket] != 1 {
			t.Errorf("outcomes = %v", d.Outcomes)
		}
	})

	t.Run("super overs excluded by default", func(t *testing.T) {
		sup := volumeStore(true)
		if d := sup.Aggregate(NewFilter()); d.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0", d.Deliveries)
		}
		f := NewFilter()
		f.IncludeSuperOvers = true
		if d := sup.Aggregate(f); d.Deliveries != 5 {
			t.Errorf("with super overs included, deliveries = %d, want 5", d.Deliveries)
		}
	})

	t.Run("bowler filter", func(t *testing.T) {
		f := NewFilter()
		f.Bowler = 2
		if d := s.Aggregate(f); d.Deliveries != 5 {
			t.Errorf("deliveries = %d, want 5", d.Deliveries)
		}
		f.Bowler = 0
		if d := s.Aggregate(f); d.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0", d.Deliveries)
		}
	})

	t.Run("over range", func(t *testing.T) {
		f := NewFilter()
		f.MinOver, f.MaxOver = 1, 19
		if d := s.Aggregate(f); d.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0: every ball is in over 0", d.Deliveries)
		}
	})

	t.Run("season range", func(t *testing.T) {
		f := NewFilter()
		f.SeasonFrom, f.SeasonTo = 2025, 2026
		if d := s.Aggregate(f); d.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0: the match is from 2024", d.Deliveries)
		}
		f.SeasonFrom, f.SeasonTo = 2024, 2024
		if d := s.Aggregate(f); d.Deliveries != 5 {
			t.Errorf("deliveries = %d, want 5", d.Deliveries)
		}
	})

	t.Run("membership mask", func(t *testing.T) {
		f := NewFilter()
		f.BatterIn = []bool{false, false, false}
		if d := s.Aggregate(f); d.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0", d.Deliveries)
		}
		f.BatterIn = []bool{true, false, false}
		if d := s.Aggregate(f); d.Deliveries != 5 {
			t.Errorf("deliveries = %d, want 5", d.Deliveries)
		}
	})
}

func TestDistributionRates(t *testing.T) {
	d := Distribution{LegalBalls: 100, RunsOffBat: 130, Extras: 20, BowlerWkts: 4}
	d.Outcomes[Dot] = 30
	d.Outcomes[Four] = 10
	d.Outcomes[Six] = 5

	if got := d.Economy(); got != 9 {
		t.Errorf("Economy = %.2f, want 9", got)
	}
	if got := d.StrikeRate(); got != 130 {
		t.Errorf("StrikeRate = %.1f, want 130", got)
	}
	if got := d.DotRate(); got != 0.30 {
		t.Errorf("DotRate = %.2f, want 0.30", got)
	}
	if got := d.BoundaryRate(); got != 0.15 {
		t.Errorf("BoundaryRate = %.2f, want 0.15", got)
	}
	if got := d.BallsPerWicket(); got != 25 {
		t.Errorf("BallsPerWicket = %.1f, want 25", got)
	}

	// An empty distribution must not divide by zero.
	var empty Distribution
	if empty.Economy() != 0 || empty.StrikeRate() != 0 || empty.BallsPerWicket() != 0 {
		t.Error("an empty distribution must yield zero rates, not NaN")
	}
}

func realStore(tb testing.TB) *Store {
	tb.Helper()
	path := filepath.Join("..", "..", "data", "out", "corpus.bin")
	if _, err := os.Stat(path); err != nil {
		tb.Skipf("corpus not built at %s; run make etl", path)
	}
	s, err := Load(path)
	if err != nil {
		tb.Fatalf("load corpus: %v", err)
	}
	return s
}

// BenchmarkAggregate measures the query the milestone is judged on: one
// bowler, one phase, over the whole corpus.
func BenchmarkAggregate(b *testing.B) {
	s := realStore(b)
	f := NewFilter().Phase(PhaseDeath)
	f.Bowler = 100
	b.ResetTimer()
	for b.Loop() {
		s.Aggregate(f)
	}
}

func BenchmarkAggregateWithMask(b *testing.B) {
	s := realStore(b)
	f := NewFilter().Phase(PhaseDeath)
	f.Bowler = 100
	mask := make([]bool, len(s.Players))
	for i := range mask {
		mask[i] = i%2 == 0
	}
	f.BatterIn = mask
	b.ResetTimer()
	for b.Loop() {
		s.Aggregate(f)
	}
}

func BenchmarkLoad(b *testing.B) {
	path := filepath.Join("..", "..", "data", "out", "corpus.bin")
	if _, err := os.Stat(path); err != nil {
		b.Skipf("corpus not built")
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}
