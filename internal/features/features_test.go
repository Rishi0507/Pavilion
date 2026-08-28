package features

import (
	"math"
	"testing"

	"manhattan/internal/attr"
	"manhattan/internal/corpus"
)

func TestNamesMatchExtractOrder(t *testing.T) {
	if Dim != len(Names) {
		t.Fatalf("Dim = %d, Names has %d entries", Dim, len(Names))
	}
	seen := map[string]bool{}
	for i, n := range Names {
		if n == "" {
			t.Errorf("feature %d has an empty name", i)
		}
		if seen[n] {
			t.Errorf("feature name %q appears twice", n)
		}
		seen[n] = true
	}
}

func idx(t *testing.T, name string) int {
	t.Helper()
	for i, n := range Names {
		if n == name {
			return i
		}
	}
	t.Fatalf("no feature named %q", name)
	return -1
}

// emptyContext is enough to exercise the situational features, which are
// computed arithmetically and do not depend on any fitted table.
func emptyContext() *Context {
	return &Context{venueRunRate: []float32{1.25}, meanRunRate: 1.25}
}

func TestExtractSituation(t *testing.T) {
	c := emptyContext()
	x := make([]float32, Dim)

	c.Extract(State{
		Innings:          2,
		Over:             15,
		Score:            140,
		Wickets:          4,
		LegalBallsBowled: 90,
		Target:           187,
		StrikerBalls:     3,
		PartnershipRuns:  22,
		PartnershipBalls: 14,
		BatterHand:       attr.LeftHandBat,
		BowlerClass:      attr.OffBreak,
	}, x)

	check := func(name string, want float32) {
		t.Helper()
		if got := x[idx(t, name)]; math.Abs(float64(got-want)) > 1e-4 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	check("innings", 2)
	check("over", 15)
	check("balls_remaining", 30)
	check("wickets_in_hand", 6)
	check("score", 140)
	check("current_run_rate", 6*140.0/90)
	check("chasing", 1)
	check("runs_required", 47)
	check("required_rate", 6*47.0/30)
	check("striker_balls", 3)
	check("new_batter", 1) // fewer than six balls faced
	check("partnership_runs", 22)
	check("partnership_balls", 14)
	check("batter_left_handed", 1)
	check("bowler_spin", 1)
	// Off break to a left-hander turns away from the bat.
	check("moves_away", 1)
	check("venue_run_rate", 1.25)
}

func TestExtractFirstInningsHasNoChase(t *testing.T) {
	c := emptyContext()
	x := make([]float32, Dim)
	c.Extract(State{Innings: 1, Over: 3, Score: 30, LegalBallsBowled: 18}, x)

	for _, name := range []string{"chasing", "runs_required", "required_rate"} {
		if got := x[idx(t, name)]; got != 0 {
			t.Errorf("%s = %v in the first innings, want 0", name, got)
		}
	}
}

// TestExtractClampsImpossibleSituations guards against a chase that has already
// been won, and an innings that has run past its allotted balls, producing
// negative features the model never saw in training.
func TestExtractClampsImpossibleSituations(t *testing.T) {
	c := emptyContext()
	x := make([]float32, Dim)

	c.Extract(State{Innings: 2, Score: 200, Target: 180, LegalBallsBowled: 130}, x)
	if got := x[idx(t, "runs_required")]; got != 0 {
		t.Errorf("runs_required = %v after the target is passed, want 0", got)
	}
	if got := x[idx(t, "balls_remaining")]; got != 0 {
		t.Errorf("balls_remaining = %v past the end of the innings, want 0", got)
	}
}

func TestExtractMatchupIsARate(t *testing.T) {
	c := emptyContext()
	x := make([]float32, Dim)

	c.Extract(State{Matchup: Matchup{Balls: 50, Runs: 75, Outs: 2}}, x)
	if got := x[idx(t, "matchup_balls")]; got != 50 {
		t.Errorf("matchup_balls = %v, want 50", got)
	}
	if got := x[idx(t, "matchup_strike_rate")]; math.Abs(float64(got-150)) > 1e-4 {
		t.Errorf("matchup_strike_rate = %v, want 150", got)
	}
	if got := x[idx(t, "matchup_out_rate")]; math.Abs(float64(got-0.04)) > 1e-6 {
		t.Errorf("matchup_out_rate = %v, want 0.04", got)
	}

	// An unplayed matchup must be zeroed rather than dividing by zero.
	c.Extract(State{}, x)
	for _, name := range []string{"matchup_balls", "matchup_strike_rate", "matchup_out_rate"} {
		if got := x[idx(t, name)]; got != 0 {
			t.Errorf("%s = %v for an unplayed matchup, want 0", name, got)
		}
	}
}

// leakStore builds two matches between the same pair. The first match is where
// all the runs are scored; the second is what the test inspects.
func leakStore() (*corpus.Store, attr.Table) {
	s := &corpus.Store{
		Players: []corpus.Player{
			{CricsheetID: "bat", Name: "Batter"},
			{CricsheetID: "bwl", Name: "Bowler"},
		},
		Teams:  []string{"A", "B"},
		Venues: []string{"Ground"},
		Cities: []string{"City"},
	}
	for m := range 2 {
		s.M.CricsheetID = append(s.M.CricsheetID, uint32(m+1))
		s.M.Season = append(s.M.Season, uint16(2023+m))
		s.M.Date = append(s.M.Date, int32(20230401+m*10000))
		s.M.Venue = append(s.M.Venue, 0)
		s.M.City = append(s.M.City, 0)
		s.M.TeamA = append(s.M.TeamA, 0)
		s.M.TeamB = append(s.M.TeamB, 1)
		s.M.TossWinner = append(s.M.TossWinner, 0)
		s.M.TossField = append(s.M.TossField, false)
		s.M.Winner = append(s.M.Winner, 0)
		s.M.Playoff = append(s.M.Playoff, false)

		start := uint32(s.D.Len())
		for range 12 {
			s.D.Innings = append(s.D.Innings, corpus.InningsID(m))
			s.D.Over = append(s.D.Over, 0)
			s.D.BallInOver = append(s.D.BallInOver, 0)
			s.D.LegalBall = append(s.D.LegalBall, 0)
			s.D.Legal = append(s.D.Legal, true)
			s.D.Batter = append(s.D.Batter, 0)
			s.D.NonStriker = append(s.D.NonStriker, 0)
			s.D.Bowler = append(s.D.Bowler, 1)
			s.D.RunsBat = append(s.D.RunsBat, 4)
			s.D.Wides = append(s.D.Wides, 0)
			s.D.NoBalls = append(s.D.NoBalls, 0)
			s.D.Byes = append(s.D.Byes, 0)
			s.D.LegByes = append(s.D.LegByes, 0)
			s.D.Penalty = append(s.D.Penalty, 0)
			s.D.Wicket = append(s.D.Wicket, corpus.WicketNone)
			s.D.PlayerOut = append(s.D.PlayerOut, corpus.NoPlayer)
			s.D.ScoreBefore = append(s.D.ScoreBefore, 0)
			s.D.WicketsBefore = append(s.D.WicketsBefore, 0)
			s.D.LegalBallsBefore = append(s.D.LegalBallsBefore, 0)
		}
		s.Inn.Match = append(s.Inn.Match, corpus.MatchID(m))
		s.Inn.BattingTeam = append(s.Inn.BattingTeam, 0)
		s.Inn.BowlingTeam = append(s.Inn.BowlingTeam, 1)
		s.Inn.Target = append(s.Inn.Target, 0)
		s.Inn.SuperOver = append(s.Inn.SuperOver, false)
		s.Inn.Miscounted = append(s.Inn.Miscounted, false)
		s.Inn.Start = append(s.Inn.Start, start)
		s.Inn.End = append(s.Inn.End, uint32(s.D.Len()))
		s.Inn.Runs = append(s.Inn.Runs, 48)
		s.Inn.Wickets = append(s.Inn.Wickets, 0)
		s.Inn.LegalBalls = append(s.Inn.LegalBalls, 12)
	}

	a := attr.Table{
		"bat": {CricsheetID: "bat", Bat: attr.RightHandBat, Provenance: attr.Sourced},
		"bwl": {CricsheetID: "bwl", Bat: attr.RightHandBat, Bowl: attr.RightArmPace, Provenance: attr.Sourced},
	}
	return s, a
}

// TestWalkDoesNotLeakTheCurrentMatch is the regression guard for the bug that
// made the first version of this model worse than predicting the population
// average.
//
// Head-to-head features are career totals, so featurising a delivery with a
// graph built over all of history hands the model a summary that already
// contains the ball it is being asked to predict. Trained that way it scored
// 39% of its feature importance on the leak and then lost to the baseline on
// data it had not seen.
func TestWalkDoesNotLeakTheCurrentMatch(t *testing.T) {
	s, a := leakStore()
	newContext := func(uint16) *Context { return emptyContext() }

	ballsIdx := idx(t, "matchup_balls")

	var rows []Row
	Walk(s, a, newContext, func(r Row) { rows = append(rows, r) })

	if len(rows) != 24 {
		t.Fatalf("got %d rows, want 24", len(rows))
	}

	// Every delivery of the first match must see an empty head-to-head: there
	// is no prior history at all.
	for i := range 12 {
		if got := rows[i].X[ballsIdx]; got != 0 {
			t.Errorf("match 1 ball %d: matchup_balls = %v, want 0", i, got)
		}
	}

	// Every delivery of the second match must see exactly the first match's
	// twelve balls, and never its own accumulating total.
	for i := 12; i < 24; i++ {
		if got := rows[i].X[ballsIdx]; got != 12 {
			t.Errorf("match 2 ball %d: matchup_balls = %v, want 12 (the first match only)", i-12, got)
		}
	}
}

// TestWalkRefitsPerSeason checks the other half of the leak fix: the rate table
// a row is featurised with must have been fitted on earlier seasons only.
func TestWalkRefitsPerSeason(t *testing.T) {
	s, a := leakStore()

	var asked []uint16
	newContext := func(maxSeason uint16) *Context {
		asked = append(asked, maxSeason)
		return emptyContext()
	}
	Walk(s, a, newContext, func(Row) {})

	if len(asked) != 2 {
		t.Fatalf("context was built %d times, want once per season", len(asked))
	}
	// The matches are in 2023 and 2024, so the fits must be through 2022 and
	// 2023 respectively: strictly earlier than the season being featurised.
	if asked[0] != 2022 || asked[1] != 2023 {
		t.Errorf("contexts fitted through %v, want [2022 2023]", asked)
	}
}

func TestWalkSkipsUnknownAttributes(t *testing.T) {
	s, a := leakStore()
	delete(a, "bwl") // the bowler's class is now unknown

	n := 0
	Walk(s, a, func(uint16) *Context { return emptyContext() }, func(Row) { n++ })
	if n != 0 {
		t.Errorf("emitted %d rows for deliveries with an unclassifiable matchup, want 0", n)
	}
}
