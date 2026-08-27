package corpus

import (
	"bytes"
	"reflect"
	"testing"
)

func sampleStore() *Store {
	s := &Store{
		Players: []Player{
			{CricsheetID: "aaaa1111", Name: "A Batter", Aliases: []string{"A Batter", "Alpha Batter"}, CricinfoID: "1"},
			{CricsheetID: "bbbb2222", Name: "B Bowler", Aliases: []string{"B Bowler"}, CricinfoID: "2"},
		},
		Teams:  []string{"Team A", "Team B"},
		Venues: []string{"Test Ground"},
		Cities: []string{"Testville"},
	}
	s.M.CricsheetID = []uint32{12345}
	s.M.Date = []int32{20240401}
	s.M.Season = []uint16{2024}
	s.M.Venue = []VenueID{0}
	s.M.City = []CityID{0}
	s.M.TeamA = []TeamID{0}
	s.M.TeamB = []TeamID{1}
	s.M.TossWinner = []TeamID{0}
	s.M.TossField = []bool{true}
	s.M.Winner = []TeamID{NoTeam}
	s.M.Playoff = []bool{false}

	s.Inn.Match = []MatchID{0}
	s.Inn.BattingTeam = []TeamID{0}
	s.Inn.BowlingTeam = []TeamID{1}
	s.Inn.Target = []uint16{0}
	s.Inn.SuperOver = []bool{false}
	s.Inn.Miscounted = []bool{false}
	s.Inn.Start = []uint32{0}
	s.Inn.End = []uint32{2}
	s.Inn.Runs = []uint16{5}
	s.Inn.Wickets = []uint8{1}
	s.Inn.LegalBalls = []uint16{2}

	s.D.Innings = []InningsID{0, 0}
	s.D.Over = []uint8{0, 0}
	s.D.BallInOver = []uint8{0, 1}
	s.D.LegalBall = []uint8{0, 1}
	s.D.Legal = []bool{true, true}
	s.D.Batter = []PlayerID{0, 0}
	s.D.NonStriker = []PlayerID{1, 1}
	s.D.Bowler = []PlayerID{1, 1}
	s.D.RunsBat = []uint8{4, 1}
	s.D.Wides = []uint8{0, 0}
	s.D.NoBalls = []uint8{0, 0}
	s.D.Byes = []uint8{0, 0}
	s.D.LegByes = []uint8{0, 0}
	s.D.Penalty = []uint8{0, 0}
	s.D.Wicket = []WicketKind{WicketNone, WicketCaught}
	s.D.PlayerOut = []PlayerID{NoPlayer, 0}
	s.D.ScoreBefore = []uint16{0, 4}
	s.D.WicketsBefore = []uint8{0, 0}
	s.D.LegalBallsBefore = []uint8{0, 1}
	return s
}

func TestCodecRoundTrip(t *testing.T) {
	want := sampleStore()
	got, err := Decode(want.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// byCricsheetID is a lazily built cache and is not serialised.
	want.byCricsheetID, got.byCricsheetID = nil, nil
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip changed the store\n got: %+v\nwant: %+v", got, want)
	}
}

func TestCodecIsDeterministic(t *testing.T) {
	s := sampleStore()
	if a, b := s.Encode(), s.Encode(); !bytes.Equal(a, b) {
		t.Error("Encode is not deterministic")
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	good := sampleStore().Encode()

	tests := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"bad magic", append([]byte("NOTACORP"), good[8:]...)},
		{"truncated header", good[:6]},
		{"truncated body", good[:len(good)-4]},
		{"truncated mid-column", good[:len(good)/2]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.in); err == nil {
				t.Error("Decode: want error, got nil")
			}
		})
	}
}

// TestDecodeRejectsAbsurdLength guards against a corrupt length prefix forcing
// an enormous allocation on the load path.
func TestDecodeRejectsAbsurdLength(t *testing.T) {
	b := sampleStore().Encode()
	// Overwrite the player-count prefix, which sits immediately after the
	// 8-byte magic and the 4-byte version, with an implausible value.
	b[12], b[13], b[14], b[15] = 0xFF, 0xFF, 0xFF, 0x7F
	if _, err := Decode(b); err == nil {
		t.Error("Decode: want error for absurd length prefix, got nil")
	}
}

func TestPhaseOf(t *testing.T) {
	tests := []struct {
		over uint8
		want Phase
	}{
		{0, PhasePowerplay},
		{5, PhasePowerplay},
		{6, PhaseMiddle},
		{14, PhaseMiddle},
		{15, PhaseDeath},
		{19, PhaseDeath},
	}
	for _, tc := range tests {
		if got := PhaseOf(tc.over); got != tc.want {
			t.Errorf("PhaseOf(%d) = %v, want %v", tc.over, got, tc.want)
		}
	}
}

func TestWicketKindSemantics(t *testing.T) {
	tests := []struct {
		in       string
		bowler   bool
		costsWkt bool
	}{
		{"caught", true, true},
		{"bowled", true, true},
		{"lbw", true, true},
		{"caught and bowled", true, true},
		{"stumped", true, true},
		{"hit wicket", true, true},
		{"run out", false, true},
		{"obstructing the field", false, true},
		{"retired out", false, true},
		{"retired hurt", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			k, err := ParseWicketKind(tc.in)
			if err != nil {
				t.Fatalf("ParseWicketKind(%q): %v", tc.in, err)
			}
			if got := k.CreditedToBowler(); got != tc.bowler {
				t.Errorf("CreditedToBowler() = %v, want %v", got, tc.bowler)
			}
			if got := k.CostsWicket(); got != tc.costsWkt {
				t.Errorf("CostsWicket() = %v, want %v", got, tc.costsWkt)
			}
		})
	}

	if _, err := ParseWicketKind("teleported"); err == nil {
		t.Error("ParseWicketKind: want error for unknown kind")
	}
	if WicketNone.CostsWicket() {
		t.Error("WicketNone must not cost a wicket")
	}
}

func TestDeliveryRunAccounting(t *testing.T) {
	var d Deliveries
	d.RunsBat = []uint8{1}
	d.Wides = []uint8{2}
	d.NoBalls = []uint8{1}
	d.Byes = []uint8{4}
	d.LegByes = []uint8{1}
	d.Penalty = []uint8{5}

	if got, want := d.TotalRuns(0), 14; got != want {
		t.Errorf("TotalRuns = %d, want %d", got, want)
	}
	// Byes, leg-byes and penalties are not charged to the bowler.
	if got, want := d.BowlerRuns(0), 4; got != want {
		t.Errorf("BowlerRuns = %d, want %d", got, want)
	}
}
