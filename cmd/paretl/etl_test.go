package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"manhattan/internal/corpus"
)

// synthDelivery is a compact way to spell a delivery in the tests below.
type synthDelivery struct {
	actual  string
	batter  string
	bowler  string
	runsBat int
	wides   int
	noballs int
	wicket  string
}

func buildSynthetic(t *testing.T, deliveries []synthDelivery) (*builder, corpus.Store) {
	t.Helper()

	registry := map[string]string{
		"Striker":    "aaaaaaaa",
		"NonStriker": "bbbbbbbb",
		"Bowler":     "cccccccc",
	}

	var over csOver
	for _, d := range deliveries {
		var cd csDelivery
		cd.ActualDelivery = d.actual
		cd.Batter = d.batter
		cd.NonStriker = "NonStriker"
		cd.Bowler = d.bowler
		cd.Runs.Batter = d.runsBat
		cd.Extras.Wides = d.wides
		cd.Extras.NoBalls = d.noballs
		cd.Runs.Extras = d.wides + d.noballs
		cd.Runs.Total = cd.Runs.Batter + cd.Runs.Extras
		if d.wicket != "" {
			cd.Wickets = append(cd.Wickets, struct {
				Kind      string `json:"kind"`
				PlayerOut string `json:"player_out"`
			}{Kind: d.wicket, PlayerOut: d.batter})
		}
		over.Deliveries = append(over.Deliveries, cd)
	}

	m := &csMatch{}
	m.Meta.DataVersion = "1.2.0"
	m.Info = csInfo{
		BallsPerOver: 6,
		City:         "Testville",
		Venue:        "Test Ground, Testville",
		Dates:        []string{"2024-04-01"},
		MatchType:    "T20",
		Overs:        20,
		Season:       json.RawMessage(`"2024"`),
		Teams:        []string{"Team A", "Team B"},
	}
	m.Info.Toss.Winner = "Team A"
	m.Info.Toss.Decision = "bat"
	m.Info.Outcome.Winner = "Team A"
	m.Info.Registry.People = registry
	m.Innings = []csInnings{
		{Team: "Team A", Overs: []csOver{over}},
		{Team: "Team B"},
	}

	b := newBuilder()
	b.scanRegistries([]*csMatch{m})
	b.resolvePlayers()
	if err := b.addMatch(m, "1.json"); err != nil {
		t.Fatalf("addMatch: %v", err)
	}
	return b, b.st
}

// TestOverIndexingAcrossExtras is the central correctness test for the ETL.
//
// A wide or a no-ball does not advance the legal-ball count, so an over may
// contain more than six deliveries. Getting this wrong shifts every subsequent
// ball into the wrong over, which silently corrupts every phase-based rate in
// the corpus. The expectations here are computed by hand and cross-checked
// against Cricsheet's own actual_delivery field by the builder.
func TestOverIndexingAcrossExtras(t *testing.T) {
	deliveries := []synthDelivery{
		{actual: "0.1", batter: "Striker", bowler: "Bowler", runsBat: 0},
		{actual: "0.2", batter: "Striker", bowler: "Bowler", runsBat: 4},
		{actual: "0.3", batter: "Striker", bowler: "Bowler", wides: 1},
		{actual: "0.3", batter: "Striker", bowler: "Bowler", runsBat: 1},
		{actual: "0.4", batter: "Striker", bowler: "Bowler", noballs: 1},
		{actual: "0.4", batter: "Striker", bowler: "Bowler", runsBat: 0},
		{actual: "0.5", batter: "Striker", bowler: "Bowler", wicket: "bowled"},
		{actual: "0.6", batter: "Striker", bowler: "Bowler", runsBat: 6},
	}

	b, st := buildSynthetic(t, deliveries)

	if got := st.D.Len(); got != len(deliveries) {
		t.Fatalf("deliveries = %d, want %d", got, len(deliveries))
	}
	if b.q.Integrity.BallIndexMismatches != 0 {
		t.Errorf("ball index mismatches = %d, want 0: %v",
			b.q.Integrity.BallIndexMismatches, b.q.Integrity.BallIndexSamples)
	}

	want := []struct {
		legalBall     uint8
		legal         bool
		legalBefore   uint8
		scoreBefore   uint16
		wicketsBefore uint8
	}{
		{0, true, 0, 0, 0},
		{1, true, 1, 0, 0},
		{2, false, 2, 4, 0}, // wide: does not advance the legal count
		{2, true, 2, 5, 0},
		{3, false, 3, 6, 0}, // no-ball: likewise
		{3, true, 3, 7, 0},
		{4, true, 4, 7, 0}, // wicket falls here
		{5, true, 5, 7, 1},
	}
	for i, w := range want {
		if got := st.D.LegalBall[i]; got != w.legalBall {
			t.Errorf("delivery %d: LegalBall = %d, want %d", i, got, w.legalBall)
		}
		if got := st.D.Legal[i]; got != w.legal {
			t.Errorf("delivery %d: Legal = %v, want %v", i, got, w.legal)
		}
		if got := st.D.LegalBallsBefore[i]; got != w.legalBefore {
			t.Errorf("delivery %d: LegalBallsBefore = %d, want %d", i, got, w.legalBefore)
		}
		if got := st.D.ScoreBefore[i]; got != w.scoreBefore {
			t.Errorf("delivery %d: ScoreBefore = %d, want %d", i, got, w.scoreBefore)
		}
		if got := st.D.WicketsBefore[i]; got != w.wicketsBefore {
			t.Errorf("delivery %d: WicketsBefore = %d, want %d", i, got, w.wicketsBefore)
		}
		if got := st.D.BallInOver[i]; got != uint8(i) {
			t.Errorf("delivery %d: BallInOver = %d, want %d", i, got, i)
		}
	}

	if got, want := st.Inn.Runs[0], uint16(13); got != want {
		t.Errorf("innings runs = %d, want %d", got, want)
	}
	if got, want := st.Inn.Wickets[0], uint8(1); got != want {
		t.Errorf("innings wickets = %d, want %d", got, want)
	}
	if got, want := st.Inn.LegalBalls[0], uint16(6); got != want {
		t.Errorf("innings legal balls = %d, want %d", got, want)
	}
}

// TestRetiredHurtDoesNotCostWicket guards a rule that is easy to get wrong: a
// retired-hurt batter may return, so the batting side has not lost a wicket.
// Retired out, by contrast, does count.
func TestRetiredHurtDoesNotCostWicket(t *testing.T) {
	tests := []struct {
		kind string
		want uint8
	}{
		{"retired hurt", 0},
		{"retired out", 1},
		{"run out", 1},
		{"caught", 1},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			_, st := buildSynthetic(t, []synthDelivery{
				{actual: "0.1", batter: "Striker", bowler: "Bowler", wicket: tc.kind},
			})
			if got := st.Inn.Wickets[0]; got != tc.want {
				t.Errorf("%s: innings wickets = %d, want %d", tc.kind, got, tc.want)
			}
		})
	}
}

// TestAmbiguousNameResolution asserts that two cricketers sharing a display
// name stay distinct. The real corpus contains exactly this case, and keying
// rates by name would silently merge two players into one.
func TestAmbiguousNameResolution(t *testing.T) {
	mkMatch := func(personID string) *csMatch {
		m := &csMatch{}
		m.Info = csInfo{
			BallsPerOver: 6,
			MatchType:    "T20",
			Dates:        []string{"2024-04-01"},
			Teams:        []string{"Team A", "Team B"},
			Venue:        "Test Ground",
			City:         "Testville",
		}
		m.Info.Registry.People = map[string]string{"Harmeet Singh": personID}
		m.Innings = []csInnings{{Team: "Team A"}, {Team: "Team B"}}
		return m
	}

	b := newBuilder()
	b.scanRegistries([]*csMatch{mkMatch("11111111"), mkMatch("22222222")})
	b.resolvePlayers()

	if got := len(b.st.Players); got != 2 {
		t.Fatalf("players = %d, want 2 distinct people for one display name", got)
	}
	if got := len(b.q.Entity.AmbiguousNames); got != 1 {
		t.Fatalf("ambiguous names reported = %d, want 1", got)
	}
	amb := b.q.Entity.AmbiguousNames[0]
	if amb.Name != "Harmeet Singh" || len(amb.IDs) != 2 {
		t.Errorf("ambiguous name = %+v, want Harmeet Singh with 2 ids", amb)
	}

	one, ok := b.st.PlayerByCricsheetID("11111111")
	if !ok {
		t.Fatal("first identifier did not resolve")
	}
	two, ok := b.st.PlayerByCricsheetID("22222222")
	if !ok {
		t.Fatal("second identifier did not resolve")
	}
	if one == two {
		t.Error("two identifiers collapsed to the same PlayerID")
	}
}

// TestAbandonedMatchesAreSkipped checks that a match with no second innings is
// excluded rather than half-ingested.
func TestAbandonedMatchesAreSkipped(t *testing.T) {
	m := &csMatch{}
	m.Info = csInfo{
		BallsPerOver: 6,
		MatchType:    "T20",
		Dates:        []string{"2024-04-01"},
		Teams:        []string{"Team A", "Team B"},
	}
	m.Info.Outcome.Result = "no result"
	m.Innings = []csInnings{{Team: "Team A"}}

	b := newBuilder()
	b.scanRegistries([]*csMatch{m})
	b.resolvePlayers()
	if err := b.addMatch(m, "abandoned.json"); err != nil {
		t.Fatalf("addMatch: %v", err)
	}
	if b.st.M.Len() != 0 {
		t.Errorf("matches ingested = %d, want 0", b.st.M.Len())
	}
	if b.q.Integrity.AbandonedMatches != 1 {
		t.Errorf("abandoned matches = %d, want 1", b.q.Integrity.AbandonedMatches)
	}
}

// corpusDir locates the real Cricsheet archive, skipping the test when the raw
// data has not been downloaded.
func corpusDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "data", "raw", "ipl_json")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("raw Cricsheet data not present at %s; run make data", dir)
	}
	return dir
}

// TestCorpusIsReproducible asserts the ETL is a pure function of its input.
// The corpus binary is a build artifact that models and the match engine are
// keyed to, so a byte-unstable ETL would make every downstream artifact
// unreproducible. Player id assignment in particular must not depend on map
// iteration order.
func TestCorpusIsReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the full corpus")
	}
	dir := corpusDir(t)
	register := filepath.Join("..", "..", "data", "raw", "people.csv")

	build := func() []byte {
		matches, files, err := parseAll(dir)
		if err != nil {
			t.Fatalf("parseAll: %v", err)
		}
		b := newBuilder()
		if err := b.loadRegister(register); err != nil {
			t.Fatalf("loadRegister: %v", err)
		}
		b.scanRegistries(matches)
		b.resolvePlayers()
		for i, m := range matches {
			if err := b.addMatch(m, files[i]); err != nil {
				t.Fatalf("addMatch %s: %v", files[i], err)
			}
		}
		return b.st.Encode()
	}

	first, second := build(), build()
	if !bytes.Equal(first, second) {
		t.Fatalf("corpus is not byte-reproducible: %d bytes vs %d bytes", len(first), len(second))
	}
}

// TestRealCorpusIntegrity runs the full ETL and asserts the invariants that the
// data quality report tracks. This is the check that fails the build if a new
// Cricsheet release introduces a format change.
func TestRealCorpusIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the full corpus")
	}
	dir := corpusDir(t)

	matches, files, err := parseAll(dir)
	if err != nil {
		t.Fatalf("parseAll: %v", err)
	}
	b := newBuilder()
	if err := b.loadRegister(filepath.Join("..", "..", "data", "raw", "people.csv")); err != nil {
		t.Fatalf("loadRegister: %v", err)
	}
	b.scanRegistries(matches)
	b.resolvePlayers()
	for i, m := range matches {
		if err := b.addMatch(m, files[i]); err != nil {
			t.Fatalf("addMatch %s: %v", files[i], err)
		}
	}
	b.finish(dir, len(files))

	if b.q.Integrity.BallIndexMismatches != 0 {
		t.Errorf("ball index mismatches = %d, want 0:\n%v",
			b.q.Integrity.BallIndexMismatches, b.q.Integrity.BallIndexSamples)
	}
	if b.q.Integrity.RunsTotalMismatches != 0 {
		t.Errorf("runs.total mismatches = %d, want 0", b.q.Integrity.RunsTotalMismatches)
	}
	if n := len(b.q.Entity.UnregisteredNames); n != 0 {
		t.Errorf("unregistered player names = %d, want 0", n)
	}
	if got := b.q.Coverage["delivery.batter_resolved"]; got != 100 {
		t.Errorf("batter resolution coverage = %.4f%%, want 100%%", got)
	}
	if got := b.q.Coverage["delivery.bowler_resolved"]; got != 100 {
		t.Errorf("bowler resolution coverage = %.4f%%, want 100%%", got)
	}

	// Every innings must be a contiguous, non-overlapping slice of the
	// delivery table, in order. The graph and the aggregation layer both rely
	// on being able to address an innings as a range.
	for i := 0; i < b.st.Inn.Len(); i++ {
		start, end := b.st.Inn.Start[i], b.st.Inn.End[i]
		if start > end || int(end) > b.st.D.Len() {
			t.Fatalf("innings %d: bad range [%d, %d)", i, start, end)
		}
		if i > 0 && start != b.st.Inn.End[i-1] {
			t.Fatalf("innings %d starts at %d, previous ended at %d", i, start, b.st.Inn.End[i-1])
		}
		for j := start; j < end; j++ {
			if b.st.D.Innings[j] != corpus.InningsID(i) {
				t.Fatalf("delivery %d claims innings %d, is inside innings %d", j, b.st.D.Innings[j], i)
			}
		}
	}
}
