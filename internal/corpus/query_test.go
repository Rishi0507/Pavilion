package corpus

import "testing"

// volumeStore builds a two-innings corpus with known volumes:
// player 0 faces 4 legal balls, player 2 bowls 4 legal balls plus a wide.
func volumeStore(superOver bool) *Store {
	s := &Store{
		Players: []Player{
			{CricsheetID: "bat", Name: "Batter"},
			{CricsheetID: "non", Name: "NonStriker"},
			{CricsheetID: "bwl", Name: "Bowler"},
		},
		Teams:  []string{"A", "B"},
		Venues: []string{"Ground"},
		Cities: []string{"City"},
	}
	s.M.CricsheetID = []uint32{1}
	s.M.Season = []uint16{2024}
	s.M.Date = []int32{20240401}
	s.M.Venue = []VenueID{0}
	s.M.City = []CityID{0}
	s.M.TeamA = []TeamID{0}
	s.M.TeamB = []TeamID{1}
	s.M.TossWinner = []TeamID{0}
	s.M.TossField = []bool{false}
	s.M.Winner = []TeamID{0}
	s.M.Playoff = []bool{false}

	s.Inn.Match = []MatchID{0}
	s.Inn.BattingTeam = []TeamID{0}
	s.Inn.BowlingTeam = []TeamID{1}
	s.Inn.Target = []uint16{0}
	s.Inn.SuperOver = []bool{superOver}
	s.Inn.Miscounted = []bool{false}
	s.Inn.Start = []uint32{0}
	s.Inn.End = []uint32{5}
	s.Inn.Runs = []uint16{11}
	s.Inn.Wickets = []uint8{1}
	s.Inn.LegalBalls = []uint16{4}

	// Four legal balls and one wide.
	s.D.Innings = []InningsID{0, 0, 0, 0, 0}
	s.D.Over = []uint8{0, 0, 0, 0, 0}
	s.D.BallInOver = []uint8{0, 1, 2, 3, 4}
	s.D.LegalBall = []uint8{0, 1, 2, 2, 3}
	s.D.Legal = []bool{true, true, false, true, true}
	s.D.Batter = []PlayerID{0, 0, 0, 0, 0}
	s.D.NonStriker = []PlayerID{1, 1, 1, 1, 1}
	s.D.Bowler = []PlayerID{2, 2, 2, 2, 2}
	s.D.RunsBat = []uint8{4, 1, 0, 6, 0}
	s.D.Wides = []uint8{0, 0, 1, 0, 0}
	s.D.NoBalls = []uint8{0, 0, 0, 0, 0}
	s.D.Byes = []uint8{0, 0, 0, 0, 0}
	s.D.LegByes = []uint8{0, 0, 0, 0, 0}
	s.D.Penalty = []uint8{0, 0, 0, 0, 0}
	s.D.Wicket = []WicketKind{WicketNone, WicketNone, WicketNone, WicketNone, WicketCaught}
	s.D.PlayerOut = []PlayerID{NoPlayer, NoPlayer, NoPlayer, NoPlayer, 0}
	s.D.ScoreBefore = []uint16{0, 4, 5, 6, 12}
	s.D.WicketsBefore = []uint8{0, 0, 0, 0, 0}
	s.D.LegalBallsBefore = []uint8{0, 1, 2, 2, 3}
	return s
}

func TestVolumes(t *testing.T) {
	v := volumeStore(false).Volumes()

	bat := v[0]
	if bat.BallsFaced != 4 {
		t.Errorf("BallsFaced = %d, want 4 (the wide does not count)", bat.BallsFaced)
	}
	if bat.RunsScored != 11 {
		t.Errorf("RunsScored = %d, want 11", bat.RunsScored)
	}
	if bat.Dismissals != 1 {
		t.Errorf("Dismissals = %d, want 1", bat.Dismissals)
	}

	bowl := v[2]
	if bowl.BallsBowled != 4 {
		t.Errorf("BallsBowled = %d, want 4 (the wide does not count)", bowl.BallsBowled)
	}
	// 4 + 1 + 6 off the bat, plus the wide, all charged to the bowler.
	if bowl.RunsConceded != 12 {
		t.Errorf("RunsConceded = %d, want 12", bowl.RunsConceded)
	}
	if bowl.Wickets != 1 {
		t.Errorf("Wickets = %d, want 1", bowl.Wickets)
	}
	if bowl.Innings != 1 {
		t.Errorf("Innings = %d, want 1", bowl.Innings)
	}
	if bowl.FirstSeason != 2024 || bowl.LastSeason != 2024 {
		t.Errorf("seasons = %d-%d, want 2024-2024", bowl.FirstSeason, bowl.LastSeason)
	}
}

// TestVolumesExcludeSuperOvers matters because a one-over shootout has none of
// the phase structure the game models, and counting it would distort
// death-overs rates in particular.
func TestVolumesExcludeSuperOvers(t *testing.T) {
	v := volumeStore(true).Volumes()
	if v[0].BallsFaced != 0 || v[2].BallsBowled != 0 {
		t.Errorf("super-over deliveries were counted: faced=%d bowled=%d",
			v[0].BallsFaced, v[2].BallsBowled)
	}
}

func TestEligibility(t *testing.T) {
	tests := []struct {
		name   string
		vol    Volume
		rule   Eligibility
		bowler bool
		batter bool
	}{
		{
			name:   "clears both",
			vol:    Volume{BallsBowled: 200, BallsFaced: 400, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200},
			bowler: true, batter: true,
		},
		{
			name:   "specialist bowler need not clear the batting bar",
			vol:    Volume{BallsBowled: 3000, BallsFaced: 40, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200},
			bowler: true, batter: false,
		},
		{
			name:   "specialist batter need not bowl",
			vol:    Volume{BallsBowled: 0, BallsFaced: 4000, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200},
			bowler: false, batter: true,
		},
		{
			name:   "exactly at the threshold qualifies",
			vol:    Volume{BallsBowled: 120, BallsFaced: 200, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200},
			bowler: true, batter: true,
		},
		{
			name:   "one short does not",
			vol:    Volume{BallsBowled: 119, BallsFaced: 199, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200},
			bowler: false, batter: false,
		},
		{
			name:   "recency filter excludes a retired player",
			vol:    Volume{BallsBowled: 5000, BallsFaced: 5000, LastSeason: 2013},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200, SinceSeason: 2020},
			bowler: false, batter: false,
		},
		{
			name:   "recency filter admits a current player",
			vol:    Volume{BallsBowled: 5000, BallsFaced: 5000, LastSeason: 2026},
			rule:   Eligibility{MinBallsBowled: 120, MinBallsFaced: 200, SinceSeason: 2020},
			bowler: true, batter: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bowler, batter := tc.rule.Qualifies(tc.vol)
			if bowler != tc.bowler || batter != tc.batter {
				t.Errorf("Qualifies = (bowler %v, batter %v), want (%v, %v)",
					bowler, batter, tc.bowler, tc.batter)
			}
		})
	}
}

func TestEligibleSelectsRoles(t *testing.T) {
	s := volumeStore(false)
	// Thresholds low enough that the sample store's players qualify.
	all, bowlers, batters := s.Eligible(Eligibility{MinBallsBowled: 1, MinBallsFaced: 1})

	if len(all) != 2 {
		t.Fatalf("eligible = %d, want 2 (the non-striker faced nothing)", len(all))
	}
	if len(bowlers) != 1 || bowlers[0] != 2 {
		t.Errorf("bowlers = %v, want [2]", bowlers)
	}
	if len(batters) != 1 || batters[0] != 0 {
		t.Errorf("batters = %v, want [0]", batters)
	}
}
