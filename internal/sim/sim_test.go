package sim

import (
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
	"pavilion/internal/features"
)

// fixedPredictor returns the same distribution for every situation, so tests
// isolate the engine from the model entirely.
type fixedPredictor struct{ p []float64 }

func (f fixedPredictor) Probabilities(_ features.State, dst []float64) error {
	copy(dst, f.p)
	return nil
}

// situationalPredictor varies with the state, which is what makes the
// determinism tests meaningful: an engine whose predictions ignore the
// situation would pass them trivially.
type situationalPredictor struct{}

func (situationalPredictor) Probabilities(s features.State, dst []float64) error {
	// More aggression as the required rate climbs.
	heat := float64(s.Over) / 20
	if s.Target > 0 && s.LegalBallsBowled < 120 {
		need := float64(int(s.Target)-int(s.Score)) / float64(120-int(s.LegalBallsBowled))
		heat += math.Min(need/3, 1)
	}
	base := []float64{
		corpus.Dot:    0.34 - 0.10*heat,
		corpus.One:    0.36 - 0.05*heat,
		corpus.Two:    0.06,
		corpus.Three:  0.004,
		corpus.Four:   0.10 + 0.07*heat,
		corpus.Six:    0.05 + 0.06*heat,
		corpus.Wicket: 0.045 + 0.02*heat,
		corpus.Wide:   0.035,
		corpus.NoBall: 0.006,
	}
	total := 0.0
	for _, v := range base {
		total += v
	}
	for i := range base {
		dst[i] = base[i] / total
	}
	return nil
}

func testPuzzle() *Puzzle {
	p := &Puzzle{Date: "2026-08-28", Target: 187, Venue: 0}
	for i := range 5 {
		class := attr.RightArmPace
		if i >= 3 {
			class = attr.OffBreak
		}
		p.Attack = append(p.Attack, Player{
			ID: corpus.PlayerID(100 + i), Name: string(rune('A' + i)), Class: class, Hand: attr.RightHandBat,
		})
	}
	for i := range 11 {
		hand := attr.RightHandBat
		if i%3 == 0 {
			hand = attr.LeftHandBat
		}
		p.Batting = append(p.Batting, Player{
			ID: corpus.PlayerID(200 + i), Name: string(rune('a' + i)), Hand: hand,
		})
	}
	return p
}

func testKey(t testing.TB) DailyKey {
	t.Helper()
	k, err := DeriveDailyKey([]byte("test-master-secret"), "2026-08-28")
	if err != nil {
		t.Fatalf("DeriveDailyKey: %v", err)
	}
	return k
}

// playAll runs a whole innings with a given bowling order, returning every over.
func playAll(t testing.TB, key DailyKey, order []int, p Predictor) ([]Over, Result) {
	t.Helper()
	s := NewChase(testPuzzle())
	var overs []Over
	for _, b := range order {
		if s.Done {
			break
		}
		o, err := PlayOver(s, key, b, Rotate, p)
		if err != nil {
			t.Fatalf("over %d with bowler %d: %v", s.Over, b, err)
		}
		overs = append(overs, o)
	}
	return overs, s.Result()
}

// legalOrder produces a bowling order that respects both the four-over limit
// and the rule against consecutive overs.
func legalOrder() []int {
	return []int{0, 1, 2, 3, 4, 0, 1, 2, 3, 4, 0, 1, 2, 3, 4, 0, 1, 2, 3, 4}
}

// TestSameDecisionsSameMatch is the property the whole design exists to
// guarantee. The brief asks for a thousand repetitions of an identical decision
// sequence producing byte-identical output.
func TestSameDecisionsSameMatch(t *testing.T) {
	key := testKey(t)
	p := situationalPredictor{}
	order := legalOrder()

	want, wantResult := playAll(t, key, order, p)
	for i := range 1000 {
		got, gotResult := playAll(t, key, order, p)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d diverged from the first", i)
		}
		if gotResult != wantResult {
			t.Fatalf("run %d produced a different result: %+v vs %+v", i, gotResult, wantResult)
		}
	}
}

// TestLuckIsFixedByCoordinate is the harder half of the guarantee, and the one
// that makes the daily comparison mean anything.
//
// The uniform drawn for a delivery must depend only on where that delivery sits
// in the innings, never on what was decided earlier. Two players who bowl
// completely different attacks through the first sixteen overs must meet the
// same luck in the seventeenth; what differs is what that luck is worth against
// the bowler they chose.
func TestLuckIsFixedByCoordinate(t *testing.T) {
	key := testKey(t)

	for over := range uint8(MaxOvers) {
		for ball := range uint8(9) {
			c := Coord{Innings: 2, Over: over, Delivery: ball}
			first := Draw(key, c)
			for range 50 {
				if got := Draw(key, c); got != first {
					t.Fatalf("draw at %+v is not stable: %v then %v", c, first, got)
				}
			}
			if first < 0 || first >= 1 {
				t.Fatalf("draw at %+v is %v, outside [0,1)", c, first)
			}
		}
	}

	// Distinct coordinates must not collide.
	seen := map[float64]Coord{}
	for inn := range uint8(2) {
		for over := range uint8(MaxOvers) {
			for ball := range uint8(9) {
				c := Coord{Innings: inn + 1, Over: over, Delivery: ball}
				u := Draw(key, c)
				if prev, dup := seen[u]; dup {
					t.Fatalf("coordinates %+v and %+v share a draw", prev, c)
				}
				seen[u] = c
			}
		}
	}
}

// TestDecisionOrderDoesNotChangeLuck plays the same set of overs in different
// orders and checks that each over's draws depend on the over number alone.
func TestDecisionOrderDoesNotChangeLuck(t *testing.T) {
	key := testKey(t)

	// A fixed distribution means the outcome depends only on the draw, so any
	// difference between orderings would be the draw moving.
	p := fixedPredictor{p: []float64{0.3, 0.35, 0.06, 0.004, 0.12, 0.06, 0.05, 0.05, 0.006}}

	a, _ := playAll(t, key, []int{0, 1, 2, 3, 4, 0, 1, 2, 3, 4, 0, 1, 2, 3, 4, 0, 1, 2, 3, 4}, p)
	b, _ := playAll(t, key, []int{4, 3, 2, 1, 0, 4, 3, 2, 1, 0, 4, 3, 2, 1, 0, 4, 3, 2, 1, 0}, p)

	if len(a) != len(b) {
		t.Fatalf("different over counts: %d and %d", len(a), len(b))
	}
	for i := range a {
		if len(a[i].Deliveries) != len(b[i].Deliveries) {
			t.Fatalf("over %d: %d deliveries one way, %d the other", i, len(a[i].Deliveries), len(b[i].Deliveries))
		}
		for j := range a[i].Deliveries {
			if a[i].Deliveries[j].Outcome != b[i].Deliveries[j].Outcome {
				t.Errorf("over %d ball %d: %v under one bowling order, %v under another",
					i, j, a[i].Deliveries[j].Outcome, b[i].Deliveries[j].Outcome)
			}
		}
	}
}

// TestShuffledDecisionOrders is the property test the quality bar calls for:
// across many random legal bowling orders, every over's luck stays put.
func TestShuffledDecisionOrders(t *testing.T) {
	key := testKey(t)
	p := fixedPredictor{p: []float64{0.3, 0.35, 0.06, 0.004, 0.12, 0.06, 0.05, 0.05, 0.006}}
	r := rand.New(rand.NewPCG(11, 22))

	reference, _ := playAll(t, key, legalOrder(), p)

	for trial := range 200 {
		order := shuffledLegalOrder(r)
		got, _ := playAll(t, key, order, p)
		for i := range min(len(got), len(reference)) {
			for j := range min(len(got[i].Deliveries), len(reference[i].Deliveries)) {
				if got[i].Deliveries[j].Outcome != reference[i].Deliveries[j].Outcome {
					t.Fatalf("trial %d order %v: over %d ball %d differs", trial, order, i, j)
				}
			}
		}
	}
}

// shuffledLegalOrder builds a random bowling order obeying every constraint,
// including that each choice must leave the rest of the innings bowlable.
func shuffledLegalOrder(r *rand.Rand) []int {
	remaining := []int{4, 4, 4, 4, 4}
	order := make([]int, 0, MaxOvers)
	last := -1
	for range MaxOvers {
		var choices []int
		for i, n := range remaining {
			if n <= 0 || i == last {
				continue
			}
			remaining[i]--
			ok := completable(remaining, i)
			remaining[i]++
			if ok {
				choices = append(choices, i)
			}
		}
		if len(choices) == 0 {
			break
		}
		pick := choices[r.IntN(len(choices))]
		order = append(order, pick)
		remaining[pick]--
		last = pick
	}
	return order
}

// TestCompletable pins the scheduling rule directly.
func TestCompletable(t *testing.T) {
	tests := []struct {
		name      string
		remaining []int
		last      int
		want      bool
	}{
		{"nothing left", []int{0, 0}, 0, true},
		{"one bowler owes both remaining overs", []int{2, 0}, 1, false},
		{"two bowlers owe one each", []int{1, 1}, 0, true},
		{"the last bowler owes the majority of an odd remainder", []int{2, 1}, 0, false},
		{"someone else owes the majority of an odd remainder", []int{2, 1}, 1, true},
		{"a full attack at the start", []int{4, 4, 4, 4, 4}, -1, true},
		{"one bowler owes far too many", []int{4, 1, 0, 0, 0}, 1, false},
		{"balanced remainder", []int{2, 2, 2}, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := completable(tc.remaining, tc.last); got != tc.want {
				t.Errorf("completable(%v, last=%d) = %v, want %v", tc.remaining, tc.last, got, tc.want)
			}
		})
	}
}

// TestNoStrandedInnings plays many random innings to the end and asserts a
// legal bowler is always available. This is the guarantee the feasibility check
// exists to provide.
func TestNoStrandedInnings(t *testing.T) {
	key := testKey(t)
	p := situationalPredictor{}
	r := rand.New(rand.NewPCG(99, 100))

	for trial := range 300 {
		s := NewChase(testPuzzle())
		for !s.Done {
			legal := s.LegalBowlers()
			if len(legal) == 0 {
				t.Fatalf("trial %d: stranded at over %d with overs bowled %v",
					trial, s.Over, s.OversBowled)
			}
			if _, err := PlayOver(s, key, legal[r.IntN(len(legal))], Rotate, p); err != nil {
				t.Fatalf("trial %d over %d: %v", trial, s.Over, err)
			}
		}
		// An innings that went the distance must have used all twenty overs.
		if !s.Won() && s.Wickets < Wickets && s.Over != MaxOvers {
			t.Fatalf("trial %d ended after %d overs", trial, s.Over)
		}
	}
}

func TestBowlerLimits(t *testing.T) {
	key := testKey(t)
	p := fixedPredictor{p: []float64{1, 0, 0, 0, 0, 0, 0, 0, 0}}

	t.Run("a bowler may not bowl five overs", func(t *testing.T) {
		s := NewChase(testPuzzle())
		for i := range 4 {
			if _, err := PlayOver(s, key, 0, Rotate, p); err != nil {
				t.Fatalf("over %d: %v", i, err)
			}
			if _, err := PlayOver(s, key, 1, Rotate, p); err != nil {
				t.Fatalf("spacer %d: %v", i, err)
			}
		}
		if _, err := PlayOver(s, key, 0, Rotate, p); err == nil {
			t.Error("a fifth over was allowed")
		}
	})

	t.Run("a bowler may not bowl consecutive overs", func(t *testing.T) {
		s := NewChase(testPuzzle())
		if _, err := PlayOver(s, key, 2, Rotate, p); err != nil {
			t.Fatal(err)
		}
		if _, err := PlayOver(s, key, 2, Rotate, p); err == nil {
			t.Error("consecutive overs were allowed")
		}
	})

	t.Run("LegalBowlers agrees with PlayOver", func(t *testing.T) {
		s := NewChase(testPuzzle())
		for !s.Done {
			legal := s.LegalBowlers()
			if len(legal) == 0 {
				t.Fatal("no legal bowler but the innings is not over")
			}
			for i := range s.Puzzle.Attack {
				allowed := false
				for _, l := range legal {
					if l == i {
						allowed = true
					}
				}
				probe := *s
				probe.OversBowled = append([]uint8(nil), s.OversBowled...)
				probe.BallsFaced = append([]uint16(nil), s.BallsFaced...)
				probe.RunsScored = append([]uint16(nil), s.RunsScored...)
				_, err := PlayOver(&probe, key, i, Rotate, p)
				if allowed != (err == nil) {
					t.Fatalf("over %d bowler %d: LegalBowlers says %v, PlayOver says %v",
						s.Over, i, allowed, err)
				}
			}
			if _, err := PlayOver(s, key, legal[0], Rotate, p); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// TestFiveBowlersExactlyCoverTwentyOvers checks the constraint that makes the
// puzzle a puzzle: five bowlers at four overs each is exactly twenty, so there
// is no slack anywhere.
func TestFiveBowlersExactlyCoverTwentyOvers(t *testing.T) {
	if len(testPuzzle().Attack)*MaxOversPerBowler != MaxOvers {
		t.Fatalf("%d bowlers of %d overs does not make %d",
			len(testPuzzle().Attack), MaxOversPerBowler, MaxOvers)
	}
}

func TestInningsEndsCorrectly(t *testing.T) {
	key := testKey(t)

	t.Run("all out", func(t *testing.T) {
		s := NewChase(testPuzzle())
		p := fixedPredictor{p: []float64{corpus.Wicket: 1}}
		for !s.Done {
			legal := s.LegalBowlers()
			if _, err := PlayOver(s, key, legal[0], Rotate, p); err != nil {
				t.Fatal(err)
			}
		}
		r := s.Result()
		if !r.AllOut || r.Wickets != Wickets {
			t.Errorf("result = %+v, want all out at %d wickets", r, Wickets)
		}
		if !r.Defended {
			t.Error("an all-out side that fell short must count as defended")
		}
	})

	t.Run("target reached", func(t *testing.T) {
		s := NewChase(testPuzzle())
		p := fixedPredictor{p: []float64{corpus.Six: 1}}
		for !s.Done {
			legal := s.LegalBowlers()
			if _, err := PlayOver(s, key, legal[0], Rotate, p); err != nil {
				t.Fatal(err)
			}
		}
		r := s.Result()
		if !r.TargetMet || r.Defended {
			t.Errorf("result = %+v, want the target met", r)
		}
		if s.Score < s.Puzzle.Target {
			t.Errorf("score %d is below the target %d", s.Score, s.Puzzle.Target)
		}
	})

	t.Run("twenty overs survived", func(t *testing.T) {
		s := NewChase(testPuzzle())
		p := fixedPredictor{p: []float64{corpus.Dot: 1}}
		for !s.Done {
			legal := s.LegalBowlers()
			if _, err := PlayOver(s, key, legal[0], Rotate, p); err != nil {
				t.Fatal(err)
			}
		}
		if s.Over != MaxOvers {
			t.Errorf("innings ended after %d overs, want %d", s.Over, MaxOvers)
		}
		if s.LegalBalls != MaxOvers*6 {
			t.Errorf("%d legal balls, want %d", s.LegalBalls, MaxOvers*6)
		}
	})
}

// TestExtrasDoNotConsumeABall pins the rule that an over is six legal balls,
// however many wides it takes to get there.
func TestExtrasDoNotConsumeABall(t *testing.T) {
	key := testKey(t)
	s := NewChase(testPuzzle())
	// Mostly wides, so the over must run long.
	p := fixedPredictor{p: []float64{corpus.Dot: 0.2, corpus.Wide: 0.8}}

	o, err := PlayOver(s, key, 0, Rotate, p)
	if err != nil {
		t.Fatal(err)
	}
	legal := 0
	for _, d := range o.Deliveries {
		if d.Legal {
			legal++
		}
	}
	if legal != 6 {
		t.Errorf("%d legal balls in the over, want 6", legal)
	}
	if len(o.Deliveries) <= 6 {
		t.Errorf("%d deliveries with an 80%% wide rate; expected the over to run long", len(o.Deliveries))
	}
	if s.LegalBalls != 6 {
		t.Errorf("innings legal balls = %d, want 6", s.LegalBalls)
	}
}

func TestStrikeRotation(t *testing.T) {
	key := testKey(t)

	t.Run("odd runs change the strike", func(t *testing.T) {
		s := NewChase(testPuzzle())
		p := fixedPredictor{p: []float64{corpus.One: 1}}
		if _, err := PlayOver(s, key, 0, Rotate, p); err != nil {
			t.Fatal(err)
		}
		// Six singles is an even number of crossings, returning the pair to
		// where they started, and then the change of ends puts the other
		// batter on strike. This is what happens in a real over of singles.
		if s.Striker != 1 || s.NonStriker != 0 {
			t.Errorf("striker %d non-striker %d, want 1 and 0", s.Striker, s.NonStriker)
		}
	})

	t.Run("ends change between overs", func(t *testing.T) {
		s := NewChase(testPuzzle())
		p := fixedPredictor{p: []float64{corpus.Dot: 1}}
		if _, err := PlayOver(s, key, 0, Rotate, p); err != nil {
			t.Fatal(err)
		}
		if s.Striker != 1 || s.NonStriker != 0 {
			t.Errorf("striker %d non-striker %d after a maiden, want 1 and 0", s.Striker, s.NonStriker)
		}
	})
}

func TestTilt(t *testing.T) {
	base := []float64{0.3, 0.35, 0.06, 0.004, 0.12, 0.06, 0.05, 0.05, 0.006}
	dst := make([]float64, len(base))

	t.Run("rotate leaves the distribution alone", func(t *testing.T) {
		Tilt(base, aggressionValue, Rotate.lambda(), dst)
		for i := range base {
			if math.Abs(dst[i]-base[i]) > 1e-12 {
				t.Errorf("category %d changed under a zero tilt", i)
			}
		}
	})

	sums := func(v []float64) float64 {
		t := 0.0
		for _, x := range v {
			t += x
		}
		return t
	}

	t.Run("attacking raises boundaries and wickets", func(t *testing.T) {
		Tilt(base, aggressionValue, Attack.lambda(), dst)
		if math.Abs(sums(dst)-1) > 1e-12 {
			t.Errorf("tilted distribution sums to %.12f", sums(dst))
		}
		if dst[corpus.Six] <= base[corpus.Six] {
			t.Error("attacking did not raise the chance of a six")
		}
		if dst[corpus.Wicket] <= base[corpus.Wicket] {
			t.Error("attacking did not raise the chance of a wicket")
		}
		if dst[corpus.Dot] >= base[corpus.Dot] {
			t.Error("attacking did not lower the chance of a dot")
		}
	})

	t.Run("blocking does the reverse", func(t *testing.T) {
		Tilt(base, aggressionValue, Block.lambda(), dst)
		if dst[corpus.Dot] <= base[corpus.Dot] {
			t.Error("blocking did not raise the chance of a dot")
		}
		if dst[corpus.Wicket] >= base[corpus.Wicket] {
			t.Error("blocking did not lower the chance of a wicket")
		}
	})

	t.Run("an impossible outcome stays impossible", func(t *testing.T) {
		zeroSix := append([]float64(nil), base...)
		zeroSix[corpus.Six] = 0
		Tilt(zeroSix, aggressionValue, Attack.lambda(), dst)
		if dst[corpus.Six] != 0 {
			t.Errorf("a zero-probability outcome became %v under tilting", dst[corpus.Six])
		}
	})
}

func TestSample(t *testing.T) {
	p := []float64{0.25, 0.25, 0.5}
	tests := []struct {
		u    float64
		want int
	}{
		{0.0, 0}, {0.24, 0}, {0.25, 1}, {0.49, 1}, {0.5, 2}, {0.999999, 2},
	}
	for _, tc := range tests {
		if got := Sample(p, tc.u); got != tc.want {
			t.Errorf("Sample(u=%v) = %d, want %d", tc.u, got, tc.want)
		}
	}

	t.Run("a uniform at the very top still lands somewhere valid", func(t *testing.T) {
		if got := Sample([]float64{0.5, 0.5}, math.Nextafter(1, 0)); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})
	t.Run("zero-probability outcomes are never chosen", func(t *testing.T) {
		q := []float64{0, 1, 0}
		for u := 0.0; u < 1; u += 0.01 {
			if got := Sample(q, u); got != 1 {
				t.Fatalf("Sample(u=%v) = %d, want 1", u, got)
			}
		}
	})
}

func TestDeriveDailyKey(t *testing.T) {
	a, err := DeriveDailyKey([]byte("secret"), "2026-08-28")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveDailyKey([]byte("secret"), "2026-08-28")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("the same secret and date gave different keys")
	}

	c, err := DeriveDailyKey([]byte("secret"), "2026-08-29")
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Error("different dates gave the same key")
	}

	d, err := DeriveDailyKey([]byte("other"), "2026-08-28")
	if err != nil {
		t.Fatal(err)
	}
	if a == d {
		t.Error("different secrets gave the same key")
	}

	if _, err := DeriveDailyKey(nil, "2026-08-28"); err == nil {
		t.Error("an empty secret was accepted")
	}
	if _, err := DeriveDailyKey([]byte("secret"), ""); err == nil {
		t.Error("an empty date was accepted")
	}
}

// TestBudgetBindsBothSides checks that the defending half and the chasing half
// are the same innings from opposite chairs.
//
// The budget used to bind the player only, so the AI side could attack in all
// twenty overs while the player had six tokens. That made the two halves
// different problems and their win rates incomparable: defending fell to a
// quarter of games while chasing sat above a half, and the generator could not
// find a target at which both halves were a contest, because no such target
// existed. The game says "same score, other side", and this is what makes that
// sentence true.
func TestBudgetBindsBothSides(t *testing.T) {
	key := testKey(t)
	p := fixedPredictor{p: []float64{corpus.Dot: 1}}

	for _, tc := range []struct {
		name  string
		state *State
	}{
		{"the defend half", NewChase(testPuzzle())},
		{"the chase half", NewPlayerChase(testPuzzle())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.state
			spent := 0
			for !s.Done && spent < MaxAttacks {
				legal := s.LegalBowlers()
				if len(legal) == 0 {
					t.Fatal("no legal bowler")
				}
				if _, err := PlayOver(s, key, legal[0], Attack, p); err != nil {
					t.Fatalf("over %d, attack %d of %d: %v", s.Over, spent+1, MaxAttacks, err)
				}
				spent++
			}
			if s.AttacksLeft() != 0 {
				t.Fatalf("AttacksLeft = %d after spending the budget", s.AttacksLeft())
			}
			legal := s.LegalBowlers()
			if len(legal) == 0 {
				return
			}
			if _, err := PlayOver(s, key, legal[0], Attack, p); !errors.Is(err, ErrNoAttacksLeft) {
				t.Fatalf("a seventh attacking over was allowed: %v", err)
			}
		})
	}
}

func TestAttackBudget(t *testing.T) {
	key := testKey(t)
	p := fixedPredictor{p: []float64{corpus.Dot: 1}}

	s := NewPlayerChase(testPuzzle())
	if s.AttacksLeft() != MaxAttacks {
		t.Fatalf("AttacksLeft = %d at the start, want %d", s.AttacksLeft(), MaxAttacks)
	}

	spent := 0
	for !s.Done && spent < MaxAttacks {
		legal := s.LegalBowlers()
		if _, err := PlayOver(s, key, legal[0], Attack, p); err != nil {
			t.Fatalf("over %d: %v", s.Over, err)
		}
		spent++
		if got := s.AttacksLeft(); got != MaxAttacks-spent {
			t.Errorf("after %d attacking overs, AttacksLeft = %d, want %d", spent, got, MaxAttacks-spent)
		}
	}

	legal := s.LegalBowlers()
	if _, err := PlayOver(s, key, legal[0], Attack, p); !errors.Is(err, ErrNoAttacksLeft) {
		t.Errorf("a seventh attacking over gave %v, want ErrNoAttacksLeft", err)
	}
	// Blocking and rotating must still be available.
	if _, err := PlayOver(s, key, legal[0], Rotate, p); err != nil {
		t.Errorf("rotating after the budget ran out: %v", err)
	}
}

// TestBlockingDoesNotSpendTheBudget guards the obvious mistake.
func TestBlockingDoesNotSpendTheBudget(t *testing.T) {
	key := testKey(t)
	p := fixedPredictor{p: []float64{corpus.Dot: 1}}
	s := NewPlayerChase(testPuzzle())
	for _, intent := range []Intent{Block, Rotate, Block, Rotate} {
		legal := s.LegalBowlers()
		if _, err := PlayOver(s, key, legal[0], intent, p); err != nil {
			t.Fatal(err)
		}
	}
	if s.AttacksLeft() != MaxAttacks {
		t.Errorf("AttacksLeft = %d after four non-attacking overs, want %d", s.AttacksLeft(), MaxAttacks)
	}
}

// TestAttackingRiskDependsOnNeed pins the rule that makes the timing of the
// attacking overs matter.
//
// With a fixed tilt, spending the budget early and spending it late chased at
// the same rate, because attacking shifted the distribution identically
// wherever it was applied. Swinging while the rate is under control has to cost
// more than swinging when the chase demands it, or there is no decision in when
// to spend.
func TestAttackingRiskDependsOnNeed(t *testing.T) {
	base := []float64{0.30, 0.34, 0.06, 0.004, 0.12, 0.06, 0.05, 0.06, 0.006}
	total := 0.0
	for _, v := range base {
		total += v
	}
	for i := range base {
		base[i] /= total
	}
	dst := make([]float64, corpus.NumOutcomes)

	sums := func(v []float64) float64 {
		t := 0.0
		for _, x := range v {
			t += x
		}
		return t
	}

	t.Run("attacking with the rate under control is dangerous", func(t *testing.T) {
		ApplyIntent(base, Attack, 5.0, dst)
		if dst[corpus.Wicket] <= base[corpus.Wicket] {
			t.Errorf("wicket chance %.4f, not above the base %.4f",
				dst[corpus.Wicket], base[corpus.Wicket])
		}
		if math.Abs(sums(dst)-1) > 1e-9 {
			t.Errorf("distribution sums to %.12f", sums(dst))
		}
	})

	t.Run("attacking when the chase demands it is not punished twice", func(t *testing.T) {
		var easy, hard float64
		ApplyIntent(base, Attack, 5.0, dst)
		easy = dst[corpus.Wicket]
		ApplyIntent(base, Attack, 12.0, dst)
		hard = dst[corpus.Wicket]

		if hard >= easy {
			t.Errorf("attacking at a required rate of 12 risks %.4f, "+
				"no less than attacking at 5 which risks %.4f", hard, easy)
		}
	})

	t.Run("the risk fades smoothly as the rate climbs", func(t *testing.T) {
		prev := 99.0
		for _, req := range []float64{2, 4, 6, 8, ParRate, 10, 14} {
			ApplyIntent(base, Attack, req, dst)
			w := dst[corpus.Wicket]
			if w > prev+1e-12 {
				t.Errorf("required %.1f risks %.4f, more than at a lower rate (%.4f)", req, w, prev)
			}
			prev = w
		}
	})

	t.Run("blocking and rotating are unaffected by the rate", func(t *testing.T) {
		for _, in := range []Intent{Block, Rotate} {
			a := make([]float64, corpus.NumOutcomes)
			b := make([]float64, corpus.NumOutcomes)
			ApplyIntent(base, in, 3.0, a)
			ApplyIntent(base, in, 14.0, b)
			for k := range a {
				if math.Abs(a[k]-b[k]) > 1e-12 {
					t.Errorf("%v changed with the required rate at category %d", in, k)
				}
			}
		}
	})

	// The balance the mechanic depends on, pinned.
	//
	// Attacking used to buy about 1.2 runs an over against up to three times the
	// chance of a wicket, which made spending a token a mistake nearly
	// everywhere and the whole budget something to be ignored. It now has to
	// clear a real bar when the chase needs runs, and still has to be a mistake
	// once the chase is already won, or the timing decision disappears in the
	// other direction.
	t.Run("attacking pays when runs are needed and costs when they are not", func(t *testing.T) {
		// A wicket in the middle overs is worth roughly twelve runs of chase
		// equity. The exact figure only sets the scale of the comparison.
		const wicketWorth = 12.0

		net := func(req float64) float64 {
			atk := make([]float64, corpus.NumOutcomes)
			rot := make([]float64, corpus.NumOutcomes)
			ApplyIntent(base, Attack, req, atk)
			ApplyIntent(base, Rotate, req, rot)
			runs := func(p []float64) float64 {
				return p[corpus.One] + 2*p[corpus.Two] + 3*p[corpus.Three] +
					4*p[corpus.Four] + 6*p[corpus.Six]
			}
			dRuns := 6 * (runs(atk) - runs(rot))
			dWkts := 6 * (atk[corpus.Wicket] - rot[corpus.Wicket])
			return dRuns - wicketWorth*dWkts
		}

		for _, req := range []float64{freeRate, 8.5, 10, 12, 15} {
			if v := net(req); v < 0.6 {
				t.Errorf("at required %.1f an attacking over is worth %+.2f runs; "+
					"nobody would spend a token for that", req, v)
			}
		}
		if v := net(3.0); v > -0.5 {
			t.Errorf("with the chase already won an attacking over is worth %+.2f runs; "+
				"throwing the bat at it should cost something", v)
		}
	})

	t.Run("attacking always scores faster than rotating", func(t *testing.T) {
		runsOf := func(p []float64) float64 {
			return p[corpus.One] + 2*p[corpus.Two] + 3*p[corpus.Three] +
				4*p[corpus.Four] + 6*p[corpus.Six]
		}
		for _, req := range []float64{3, 8, 15} {
			atk := make([]float64, corpus.NumOutcomes)
			rot := make([]float64, corpus.NumOutcomes)
			ApplyIntent(base, Attack, req, atk)
			ApplyIntent(base, Rotate, req, rot)
			if runsOf(atk) <= runsOf(rot) {
				t.Errorf("at required %.0f, attacking scores %.3f and rotating %.3f",
					req, runsOf(atk), runsOf(rot))
			}
		}
	})
}
