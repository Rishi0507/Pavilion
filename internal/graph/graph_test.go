package graph

import (
	"os"
	"path/filepath"
	"testing"

	"manhattan/internal/corpus"
)

// testStore builds a small corpus by hand: two batters, two bowlers, known
// tallies, plus a super over that must be ignored.
func testStore() *corpus.Store {
	s := &corpus.Store{
		Players: []corpus.Player{
			{CricsheetID: "bat0", Name: "Batter Zero"},
			{CricsheetID: "bat1", Name: "Batter One"},
			{CricsheetID: "bwl0", Name: "Bowler Zero"},
			{CricsheetID: "bwl1", Name: "Bowler One"},
		},
		Teams:  []string{"A", "B"},
		Venues: []string{"Ground"},
		Cities: []string{"City"},
	}
	s.M.CricsheetID = []uint32{1}
	s.M.Season = []uint16{2024}
	s.M.Date = []int32{20240401}
	s.M.Venue = []corpus.VenueID{0}
	s.M.City = []corpus.CityID{0}
	s.M.TeamA = []corpus.TeamID{0}
	s.M.TeamB = []corpus.TeamID{1}
	s.M.TossWinner = []corpus.TeamID{0}
	s.M.TossField = []bool{false}
	s.M.Winner = []corpus.TeamID{0}
	s.M.Playoff = []bool{false}

	// Innings 0 is real, innings 1 is a super over.
	s.Inn.Match = []corpus.MatchID{0, 0}
	s.Inn.BattingTeam = []corpus.TeamID{0, 0}
	s.Inn.BowlingTeam = []corpus.TeamID{1, 1}
	s.Inn.Target = []uint16{0, 0}
	s.Inn.SuperOver = []bool{false, true}
	s.Inn.Miscounted = []bool{false, false}
	s.Inn.Start = []uint32{0, 6}
	s.Inn.End = []uint32{6, 7}
	s.Inn.Runs = []uint16{15, 6}
	s.Inn.Wickets = []uint8{1, 0}
	s.Inn.LegalBalls = []uint16{6, 1}

	add := func(inn corpus.InningsID, over, ball uint8, bat, bowl corpus.PlayerID,
		runs uint8, legal bool, wk corpus.WicketKind, out corpus.PlayerID) {
		s.D.Innings = append(s.D.Innings, inn)
		s.D.Over = append(s.D.Over, over)
		s.D.BallInOver = append(s.D.BallInOver, ball)
		s.D.LegalBall = append(s.D.LegalBall, ball)
		s.D.Legal = append(s.D.Legal, legal)
		s.D.Batter = append(s.D.Batter, bat)
		s.D.NonStriker = append(s.D.NonStriker, 1-bat)
		s.D.Bowler = append(s.D.Bowler, bowl)
		s.D.RunsBat = append(s.D.RunsBat, runs)
		s.D.Wides = append(s.D.Wides, 0)
		s.D.NoBalls = append(s.D.NoBalls, 0)
		s.D.Byes = append(s.D.Byes, 0)
		s.D.LegByes = append(s.D.LegByes, 0)
		s.D.Penalty = append(s.D.Penalty, 0)
		s.D.Wicket = append(s.D.Wicket, wk)
		s.D.PlayerOut = append(s.D.PlayerOut, out)
		s.D.ScoreBefore = append(s.D.ScoreBefore, 0)
		s.D.WicketsBefore = append(s.D.WicketsBefore, 0)
		s.D.LegalBallsBefore = append(s.D.LegalBallsBefore, ball)
	}

	// bat0 vs bwl0: 4 balls, 4+0+6+0 = 10 runs, one caught.
	add(0, 0, 0, 0, 2, 4, true, corpus.WicketNone, corpus.NoPlayer)
	add(0, 0, 1, 0, 2, 0, true, corpus.WicketNone, corpus.NoPlayer)
	add(0, 0, 2, 0, 2, 6, true, corpus.WicketNone, corpus.NoPlayer)
	add(0, 0, 3, 0, 2, 0, true, corpus.WicketCaught, 0)
	// bat1 vs bwl1: 2 balls, 5 runs, one run out (must not count on the edge).
	add(0, 1, 0, 1, 3, 4, true, corpus.WicketNone, corpus.NoPlayer)
	add(0, 1, 1, 1, 3, 1, true, corpus.WicketRunOut, 1)
	// Super over: bat0 vs bwl1, must be excluded entirely.
	add(1, 0, 0, 0, 3, 6, true, corpus.WicketNone, corpus.NoPlayer)

	return s
}

func TestBuildAggregatesMatchups(t *testing.T) {
	g := Build(testStore())
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	e, ok := g.Matchup(0, 2)
	if !ok {
		t.Fatal("bat0 vs bwl0 missing")
	}
	if e.Balls != 4 || e.Runs != 10 || e.Dismissals != 1 {
		t.Errorf("bat0 vs bwl0 = %+v, want 4 balls, 10 runs, 1 dismissal", e)
	}
	// Three of the four balls scored 4, 0 and 6; the fourth was a wicket off
	// what would otherwise have been a dot. A wicket outranks a dot in the
	// outcome taxonomy, so it is counted as a wicket and not also as a dot.
	if e.Dots != 1 || e.Fours != 1 || e.Sixes != 1 {
		t.Errorf("bat0 vs bwl0 shape = %d dots, %d fours, %d sixes; want 1/1/1", e.Dots, e.Fours, e.Sixes)
	}
	if got := e.StrikeRate(); got != 250 {
		t.Errorf("strike rate = %.1f, want 250", got)
	}
	if avg, ok := e.Average(); !ok || avg != 10 {
		t.Errorf("average = %.1f, %v; want 10, true", avg, ok)
	}
}

// TestRunOutsAreNotCreditedToTheBowler guards a rule that would otherwise
// quietly overstate every matchup: a run out says nothing about the contest
// between bat and ball.
func TestRunOutsAreNotCreditedToTheBowler(t *testing.T) {
	g := Build(testStore())
	e, ok := g.Matchup(1, 3)
	if !ok {
		t.Fatal("bat1 vs bwl1 missing")
	}
	if e.Dismissals != 0 {
		t.Errorf("dismissals = %d, want 0: the wicket was a run out", e.Dismissals)
	}
	if _, ok := e.Average(); ok {
		t.Error("average must report as unavailable when the batter was never out to this bowler")
	}
}

func TestSuperOversAreExcluded(t *testing.T) {
	g := Build(testStore())
	// bat0 faced bwl1 only in the super over, so the edge must not exist.
	if e, ok := g.Matchup(0, 3); ok {
		t.Errorf("super-over matchup was included: %+v", e)
	}
}

func TestBothDirectionsAgree(t *testing.T) {
	g := Build(testStore())
	bowlers, batEdges := g.BowlersFaced(0)
	if len(bowlers) != 1 || bowlers[0] != 2 {
		t.Fatalf("BowlersFaced(bat0) = %v, want [2]", bowlers)
	}
	batters, bowEdges := g.BattersFaced(2)
	if len(batters) != 1 || batters[0] != 0 {
		t.Fatalf("BattersFaced(bwl0) = %v, want [0]", batters)
	}
	if batEdges[0] != bowEdges[0] {
		t.Errorf("the two directions disagree: %+v vs %+v", batEdges[0], bowEdges[0])
	}
}

func TestDegree(t *testing.T) {
	g := Build(testStore())
	asBat, asBowl := g.Degree(0)
	if asBat != 1 || asBowl != 0 {
		t.Errorf("Degree(bat0) = (%d, %d), want (1, 0)", asBat, asBowl)
	}
	asBat, asBowl = g.Degree(2)
	if asBat != 0 || asBowl != 1 {
		t.Errorf("Degree(bwl0) = (%d, %d), want (0, 1)", asBat, asBowl)
	}
}

func TestMissingEdgesAndOutOfRangeNodes(t *testing.T) {
	g := Build(testStore())
	if _, ok := g.Matchup(0, 1); ok {
		t.Error("two batters must not have a matchup edge")
	}
	if n, e := g.BowlersFaced(9999); n != nil || e != nil {
		t.Error("an out-of-range node must yield an empty row, not panic")
	}
	if _, ok := g.Matchup(9999, 0); ok {
		t.Error("an out-of-range node must have no edges")
	}
}

// TestBuildIsDeterministic matters because construction iterates a map. If the
// CSR layout depended on map order, the graph would differ between runs and
// nothing built on top of it would be reproducible.
func TestBuildIsDeterministic(t *testing.T) {
	s := testStore()
	a, b := Build(s), Build(s)
	if a.Edges() != b.Edges() || a.Nodes() != b.Nodes() {
		t.Fatalf("sizes differ: %d/%d vs %d/%d", a.Nodes(), a.Edges(), b.Nodes(), b.Edges())
	}
	for n := range a.Nodes() {
		an, ae := a.BowlersFaced(corpus.PlayerID(n))
		bn, be := b.BowlersFaced(corpus.PlayerID(n))
		if len(an) != len(bn) {
			t.Fatalf("node %d: row lengths differ", n)
		}
		for i := range an {
			if an[i] != bn[i] || ae[i] != be[i] {
				t.Fatalf("node %d entry %d differs", n, i)
			}
		}
	}
}

// realStore loads the built corpus, skipping when it has not been generated.
func realStore(tb testing.TB) *corpus.Store {
	tb.Helper()
	path := filepath.Join("..", "..", "data", "out", "corpus.bin")
	if _, err := os.Stat(path); err != nil {
		tb.Skipf("corpus not built at %s; run make etl", path)
	}
	s, err := corpus.Load(path)
	if err != nil {
		tb.Fatalf("load corpus: %v", err)
	}
	return s
}

func TestRealGraphIsValid(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the full corpus")
	}
	g := Build(realStore(t))
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if g.Edges() == 0 {
		t.Fatal("no edges built")
	}
	t.Logf("%d nodes, %d matchup edges", g.Nodes(), g.Edges())
}

func BenchmarkBuild(b *testing.B) {
	s := realStore(b)
	b.ResetTimer()
	for b.Loop() {
		Build(s)
	}
}

func BenchmarkMatchupLookup(b *testing.B) {
	g := Build(realStore(b))
	b.ResetTimer()
	for b.Loop() {
		g.Matchup(100, 200)
	}
}
