package rates

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
)

func TestCellIndexRoundTrip(t *testing.T) {
	seen := map[int]bool{}
	for _, role := range []Role{Bowling, Batting} {
		for p := range corpus.NumPhases {
			for o := range NumOpps {
				i := CellIndex(role, corpus.Phase(p), Opp(o))
				if i < 0 || i >= NumCells {
					t.Fatalf("CellIndex(%v,%d,%d) = %d, outside [0,%d)", role, p, o, i, NumCells)
				}
				if seen[i] {
					t.Fatalf("index %d assigned twice", i)
				}
				seen[i] = true

				gotRole, gotPhase, gotOpp := UnpackCell(i)
				if gotRole != role || int(gotPhase) != p || int(gotOpp) != o {
					t.Errorf("UnpackCell(%d) = (%v,%v,%d), want (%v,%d,%d)",
						i, gotRole, gotPhase, gotOpp, role, p, o)
				}
			}
		}
	}
	if len(seen) != NumCells {
		t.Errorf("covered %d cells, want %d", len(seen), NumCells)
	}
}

// ratesStore builds a corpus where one bowler bowls only to a left-hander and
// one only to a right-hander, so that cell assignment can be checked exactly.
func ratesStore() (*corpus.Store, attr.Table) {
	s := &corpus.Store{
		Players: []corpus.Player{
			{CricsheetID: "rhb", Name: "Right Hand Bat"},
			{CricsheetID: "lhb", Name: "Left Hand Bat"},
			{CricsheetID: "pace", Name: "Pace Bowler"},
			{CricsheetID: "spin", Name: "Spin Bowler"},
			{CricsheetID: "nobody", Name: "Unknown Attributes"},
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

	s.Inn.Match = []corpus.MatchID{0}
	s.Inn.BattingTeam = []corpus.TeamID{0}
	s.Inn.BowlingTeam = []corpus.TeamID{1}
	s.Inn.Target = []uint16{0}
	s.Inn.SuperOver = []bool{false}
	s.Inn.Miscounted = []bool{false}
	s.Inn.Start = []uint32{0}
	s.Inn.Runs = []uint16{0}
	s.Inn.Wickets = []uint8{0}
	s.Inn.LegalBalls = []uint16{0}

	add := func(over uint8, bat, bowl corpus.PlayerID, runs uint8) {
		s.D.Innings = append(s.D.Innings, 0)
		s.D.Over = append(s.D.Over, over)
		s.D.BallInOver = append(s.D.BallInOver, 0)
		s.D.LegalBall = append(s.D.LegalBall, 0)
		s.D.Legal = append(s.D.Legal, true)
		s.D.Batter = append(s.D.Batter, bat)
		s.D.NonStriker = append(s.D.NonStriker, bat)
		s.D.Bowler = append(s.D.Bowler, bowl)
		s.D.RunsBat = append(s.D.RunsBat, runs)
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

	// Death overs: pace to the right-hander, spin to the left-hander.
	for range 10 {
		add(18, 0, 2, 4)
		add(18, 1, 3, 1)
	}
	// A delivery involving a player with no known attributes: it must not
	// contribute to anyone's matchup cell.
	add(18, 4, 4, 6)

	s.Inn.End = []uint32{uint32(s.D.Len())}

	a := attr.Table{
		"rhb":  {CricsheetID: "rhb", Bat: attr.RightHandBat, Provenance: attr.Sourced},
		"lhb":  {CricsheetID: "lhb", Bat: attr.LeftHandBat, Provenance: attr.Sourced},
		"pace": {CricsheetID: "pace", Bat: attr.RightHandBat, Bowl: attr.RightArmPace, Provenance: attr.Sourced},
		"spin": {CricsheetID: "spin", Bat: attr.RightHandBat, Bowl: attr.LeftArmOrthodox, Provenance: attr.Sourced},
	}
	return s, a
}

func TestBuildAssignsCells(t *testing.T) {
	s, a := ratesStore()
	tbl := Build(s, a, Options{})

	paceVsRHB := CellIndex(Bowling, corpus.PhaseDeath, VsRightHand)
	spinVsLHB := CellIndex(Bowling, corpus.PhaseDeath, VsLeftHand)

	if got := tbl.Players[2].Cells[paceVsRHB].Deliveries; got != 10 {
		t.Errorf("pace bowler vs RHB at the death = %d deliveries, want 10", got)
	}
	if got := tbl.Players[2].Cells[spinVsLHB].Deliveries; got != 0 {
		t.Errorf("pace bowler vs LHB = %d, want 0", got)
	}
	if got := tbl.Players[3].Cells[spinVsLHB].Deliveries; got != 10 {
		t.Errorf("spin bowler vs LHB at the death = %d deliveries, want 10", got)
	}

	// The right-hander faced only pace; the left-hander only spin.
	batVsPace := CellIndex(Batting, corpus.PhaseDeath, VsPace)
	batVsSpin := CellIndex(Batting, corpus.PhaseDeath, VsSpin)
	if got := tbl.Players[0].Cells[batVsPace].Deliveries; got != 10 {
		t.Errorf("RHB vs pace = %d deliveries, want 10", got)
	}
	if got := tbl.Players[1].Cells[batVsSpin].Deliveries; got != 10 {
		t.Errorf("LHB vs spin = %d deliveries, want 10", got)
	}
}

// TestUnknownAttributesAreExcluded is the rule that made the attribute work a
// prerequisite: a delivery whose matchup cannot be classified is not evidence
// about a matchup, so it must not silently land in some default cell.
func TestUnknownAttributesAreExcluded(t *testing.T) {
	s, a := ratesStore()
	tbl := Build(s, a, Options{})

	for c := range NumCells {
		if got := tbl.Players[4].Cells[c].Deliveries; got != 0 {
			t.Errorf("the unattributed player has %d deliveries in %s, want 0", got, CellName(c))
		}
	}

	total := 0
	for c := range NumCells {
		total += tbl.Cells[c].Deliveries
	}
	// 20 deliveries, each contributing to one bowling cell and one batting
	// cell; the 21st is excluded from both.
	if total != 40 {
		t.Errorf("total cell deliveries = %d, want 40", total)
	}
}

func TestPlayersWithNoDataGetThePriorAtZeroWeight(t *testing.T) {
	s, a := ratesStore()
	tbl := Build(s, a, Options{})

	// The spin bowler never bowled to a right-hander.
	c := CellIndex(Bowling, corpus.PhaseDeath, VsRightHand)
	cr := tbl.Players[3].Cells[c]
	if cr.Deliveries != 0 {
		t.Fatalf("expected no deliveries, got %d", cr.Deliveries)
	}
	if cr.Weight != 0 {
		t.Errorf("weight = %.4f, want 0: nothing is known about this player here", cr.Weight)
	}
	for k := range corpus.NumOutcomes {
		if math.Abs(cr.Posterior[k]-tbl.Cells[c].Prior[k]) > 1e-12 {
			t.Errorf("category %d: posterior %.6f, want the population prior %.6f",
				k, cr.Posterior[k], tbl.Cells[c].Prior[k])
		}
	}
}

func TestPosteriorsAreDistributions(t *testing.T) {
	s, a := ratesStore()
	tbl := Build(s, a, Options{})
	for i := range tbl.Players {
		for c := range NumCells {
			total := 0.0
			for k := range corpus.NumOutcomes {
				v := tbl.Players[i].Cells[c].Posterior[k]
				if v < 0 || v > 1 || math.IsNaN(v) {
					t.Fatalf("player %d cell %d category %d = %v", i, c, k, v)
				}
				total += v
			}
			if math.Abs(total-1) > 1e-9 {
				t.Errorf("player %d cell %s sums to %.12f", i, CellName(c), total)
			}
		}
	}
}

// TestShrinkagePullsTowardThePopulation is the behaviour the game depends on.
// A bowler with a handful of unrepresentative balls must not be rated as though
// those balls were the truth about him.
func TestShrinkagePullsTowardThePopulation(t *testing.T) {
	s, a := ratesStore()

	// Add a population of ordinary bowlers to the same cell, each with plenty
	// of deliveries. Their outcomes vary both within and between bowlers,
	// because a population of identical bowlers looks maximally overdispersed
	// to the estimator and drives the fitted concentration to its floor.
	for i := range 40 {
		id := "ord" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		s.Players = append(s.Players, corpus.Player{CricsheetID: id, Name: id})
		a[id] = attr.Player{CricsheetID: id, Bat: attr.RightHandBat, Bowl: attr.RightArmPace, Provenance: attr.Sourced}
		bowler := corpus.PlayerID(len(s.Players) - 1)
		for j := range 300 {
			// Roughly 55% dots, 30% singles, 15% fours, shifted slightly per
			// bowler so that the cell has believable between-player spread.
			runs := uint8(0)
			switch (j + i) % 20 {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10:
				runs = 0
			case 11, 12, 13, 14, 15, 16:
				runs = 1
			default:
				runs = 4
			}
			s.D.Innings = append(s.D.Innings, 0)
			s.D.Over = append(s.D.Over, 18)
			s.D.BallInOver = append(s.D.BallInOver, 0)
			s.D.LegalBall = append(s.D.LegalBall, 0)
			s.D.Legal = append(s.D.Legal, true)
			s.D.Batter = append(s.D.Batter, 0)
			s.D.NonStriker = append(s.D.NonStriker, 0)
			s.D.Bowler = append(s.D.Bowler, bowler)
			s.D.RunsBat = append(s.D.RunsBat, runs)
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
	}
	s.Inn.End = []uint32{uint32(s.D.Len())}

	tbl := Build(s, a, Options{})

	// Player 2 conceded a boundary off all ten of his deliveries. The
	// population concedes a single a ball. His posterior must land far closer
	// to the population than to his own record.
	pr := tbl.Phase(2, Bowling, corpus.PhaseDeath)
	if pr.Deliveries != 10 {
		t.Fatalf("deliveries = %d, want 10", pr.Deliveries)
	}
	if math.Abs(pr.RawEconomy-24) > 1e-6 {
		t.Fatalf("raw economy = %.4f, want 24", pr.RawEconomy)
	}
	if pr.Economy >= pr.RawEconomy {
		t.Errorf("shrunk economy %.2f is not below the raw %.2f", pr.Economy, pr.RawEconomy)
	}
	// Ten balls against a population of forty bowlers with three hundred each
	// must not survive as a rating. The exact magnitude depends on the fitted
	// concentration, which is validated against known truth in shrink_test.go;
	// what matters here is that most of the estimate comes from the population.
	if pr.Weight > 0.5 {
		t.Errorf("weight %.3f: ten deliveries should not dominate their own rating", pr.Weight)
	}
	if pr.Economy > 0.5*pr.RawEconomy {
		t.Errorf("shrunk economy %.2f is still more than half the raw %.2f", pr.Economy, pr.RawEconomy)
	}

	// A population bowler with 300 deliveries must be trusted far more.
	ord := tbl.Phase(len(s.Players)-1, Bowling, corpus.PhaseDeath)
	if ord.Weight <= pr.Weight {
		t.Errorf("a 300-ball bowler has weight %.3f, no more than a 10-ball bowler at %.3f",
			ord.Weight, pr.Weight)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s, a := ratesStore()
	want := Build(s, a, Options{})

	path := filepath.Join(t.TempDir(), "rates.bin")
	if err := Save(want, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Players) != len(want.Players) {
		t.Fatalf("players = %d, want %d", len(got.Players), len(want.Players))
	}
	for c := range NumCells {
		if got.Cells[c].Kappa != want.Cells[c].Kappa {
			t.Errorf("cell %s kappa = %v, want %v", CellName(c), got.Cells[c].Kappa, want.Cells[c].Kappa)
		}
	}
	for i := range want.Players {
		if got.Players[i] != want.Players[i] {
			t.Errorf("player %d did not round-trip", i)
		}
	}
}

func TestLoadRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.bin")
	if err := os.WriteFile(path, []byte("not a rate table"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load: want error for a corrupt file")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.bin")); err == nil {
		t.Error("Load: want error for a missing file")
	}
}

// TestPhaseCombinesOppositionClasses checks that the two opposition cells are
// merged in proportion to how much data each holds, not averaged blindly.
func TestPhaseCombinesOppositionClasses(t *testing.T) {
	s, a := ratesStore()
	tbl := Build(s, a, Options{})

	pr := tbl.Phase(2, Bowling, corpus.PhaseDeath)
	if pr.Deliveries != 10 {
		t.Errorf("deliveries = %d, want 10", pr.Deliveries)
	}
	if pr.Economy <= 0 {
		t.Errorf("economy = %.2f, want a positive rate", pr.Economy)
	}
	// Every ball went for four, so the raw economy is 24 an over.
	if math.Abs(pr.RawEconomy-24) > 1e-6 {
		t.Errorf("raw economy = %.4f, want 24", pr.RawEconomy)
	}
	// This bowler is the only one in his cell, so the population mean is his
	// own record and there is nothing to shrink toward. Partial pooling only
	// says anything once there is a population to pool with.
	if math.Abs(pr.Economy-pr.RawEconomy) > 1e-6 {
		t.Errorf("shrunk %.4f differs from raw %.4f, but this player is the whole population",
			pr.Economy, pr.RawEconomy)
	}

	t.Run("a player with nothing in the phase", func(t *testing.T) {
		if got := tbl.Phase(4, Bowling, corpus.PhaseDeath); got.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0", got.Deliveries)
		}
	})
	t.Run("an out-of-range player", func(t *testing.T) {
		if got := tbl.Phase(9999, Bowling, corpus.PhaseDeath); got.Deliveries != 0 {
			t.Errorf("deliveries = %d, want 0", got.Deliveries)
		}
	})
}
