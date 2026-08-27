// Package corpus holds the ball-by-ball delivery store for Manhattan.
//
// The entire IPL corpus is a few hundred thousand deliveries and fits
// comfortably in memory, so it is kept as a struct-of-arrays: parallel typed
// slices rather than a slice of structs. Every aggregation the game needs is a
// linear scan over one or two columns, which is cache-friendly and fast enough
// that no query planner or index is warranted.
package corpus

import "fmt"

type (
	PlayerID  uint16
	TeamID    uint8
	VenueID   uint8
	CityID    uint8
	MatchID   uint16
	InningsID uint32
)

// NoPlayer marks an absent player reference (e.g. no batter dismissed).
const NoPlayer PlayerID = 0xFFFF

// NoTeam marks an absent team reference (e.g. an abandoned match has no winner).
const NoTeam TeamID = 0xFF

// WicketKind enumerates the dismissal types Cricsheet records.
type WicketKind uint8

const (
	WicketNone WicketKind = iota
	WicketCaught
	WicketBowled
	WicketRunOut
	WicketLBW
	WicketCaughtAndBowled
	WicketStumped
	WicketHitWicket
	WicketRetiredHurt
	WicketRetiredOut
	WicketObstructing
	WicketHitBallTwice
	WicketTimedOut
)

var wicketNames = map[string]WicketKind{
	"caught":                WicketCaught,
	"bowled":                WicketBowled,
	"run out":               WicketRunOut,
	"lbw":                   WicketLBW,
	"caught and bowled":     WicketCaughtAndBowled,
	"stumped":               WicketStumped,
	"hit wicket":            WicketHitWicket,
	"retired hurt":          WicketRetiredHurt,
	"retired out":           WicketRetiredOut,
	"obstructing the field": WicketObstructing,
	"hit the ball twice":    WicketHitBallTwice,
	"timed out":             WicketTimedOut,
}

// ParseWicketKind maps a Cricsheet dismissal string to a WicketKind.
func ParseWicketKind(s string) (WicketKind, error) {
	k, ok := wicketNames[s]
	if !ok {
		return WicketNone, fmt.Errorf("corpus: unknown wicket kind %q", s)
	}
	return k, nil
}

func (k WicketKind) String() string {
	for s, v := range wicketNames {
		if v == k {
			return s
		}
	}
	return "none"
}

// CreditedToBowler reports whether the dismissal counts against the bowler.
// Run outs, retirements and obstruction are the batting side's doing.
func (k WicketKind) CreditedToBowler() bool {
	switch k {
	case WicketCaught, WicketBowled, WicketLBW, WicketCaughtAndBowled,
		WicketStumped, WicketHitWicket, WicketHitBallTwice:
		return true
	}
	return false
}

// CostsWicket reports whether the batting side loses a wicket in hand.
// Retired hurt is the sole dismissal type that does not: the batter may return.
func (k WicketKind) CostsWicket() bool {
	return k != WicketNone && k != WicketRetiredHurt
}

// Phase splits a T20 innings into its three tactical regions.
type Phase uint8

const (
	PhasePowerplay Phase = iota // overs 1-6
	PhaseMiddle                 // overs 7-15
	PhaseDeath                  // overs 16-20
	numPhases
)

// NumPhases is the number of innings phases.
const NumPhases = int(numPhases)

// PhaseOf returns the phase for a zero-based over index.
func PhaseOf(over uint8) Phase {
	switch {
	case over < 6:
		return PhasePowerplay
	case over < 15:
		return PhaseMiddle
	default:
		return PhaseDeath
	}
}

func (p Phase) String() string {
	switch p {
	case PhasePowerplay:
		return "powerplay"
	case PhaseMiddle:
		return "middle"
	case PhaseDeath:
		return "death"
	}
	return "unknown"
}

// Player is a canonical, entity-resolved cricketer.
type Player struct {
	// CricsheetID is the identifier from Cricsheet's people register. It is the
	// spine of entity resolution: display names drift between seasons but this
	// does not, and two different players may legitimately share a display name.
	CricsheetID string
	Name        string   // preferred display name (the longest observed variant)
	Aliases     []string // every display name seen for this identifier
	CricinfoID  string   // from the people register, for attribute scraping
}

// Deliveries is the ball-by-ball table, stored column-wise.
//
// Every slice has the same length. Index i refers to one delivery, in the order
// they were bowled, grouped by innings.
type Deliveries struct {
	Innings    []InningsID // index into Store.Inn
	Over       []uint8     // zero-based over index within the innings
	BallInOver []uint8     // zero-based ordinal within the over, counting extras
	LegalBall  []uint8     // zero-based legal ball this delivery attempts (0-5)
	Legal      []bool      // false for wides and no-balls

	Batter     []PlayerID
	NonStriker []PlayerID
	Bowler     []PlayerID

	RunsBat []uint8
	Wides   []uint8
	NoBalls []uint8
	Byes    []uint8
	LegByes []uint8
	Penalty []uint8

	Wicket    []WicketKind
	PlayerOut []PlayerID

	// Innings state immediately BEFORE this delivery. Precomputed here so that
	// feature extraction and aggregation are single-pass scans with no need to
	// replay the innings.
	ScoreBefore      []uint16
	WicketsBefore    []uint8
	LegalBallsBefore []uint8
}

// Len returns the number of deliveries.
func (d *Deliveries) Len() int { return len(d.Innings) }

// TotalRuns returns every run conceded off delivery i, including extras.
func (d *Deliveries) TotalRuns(i int) int {
	return int(d.RunsBat[i]) + int(d.Wides[i]) + int(d.NoBalls[i]) +
		int(d.Byes[i]) + int(d.LegByes[i]) + int(d.Penalty[i])
}

// BowlerRuns returns the runs charged to the bowler off delivery i.
// Byes and leg-byes are not the bowler's fault; wides and no-balls are.
func (d *Deliveries) BowlerRuns(i int) int {
	return int(d.RunsBat[i]) + int(d.Wides[i]) + int(d.NoBalls[i])
}

// Innings is the innings table.
type Innings struct {
	Match       []MatchID
	BattingTeam []TeamID
	BowlingTeam []TeamID
	Target      []uint16 // runs required to win; 0 when batting first
	SuperOver   []bool
	Miscounted  []bool // an umpire miscounted an over; legal-ball indexing is off
	Start       []uint32
	End         []uint32 // exclusive
	Runs        []uint16
	Wickets     []uint8
	LegalBalls  []uint16
}

// Len returns the number of innings.
func (in *Innings) Len() int { return len(in.Match) }

// Matches is the match table.
type Matches struct {
	CricsheetID []uint32 // the numeric id Cricsheet uses as the filename
	Date        []int32  // YYYYMMDD of the first day
	Season      []uint16 // IPL edition year, derived from the date not the label
	Venue       []VenueID
	City        []CityID
	TeamA       []TeamID
	TeamB       []TeamID
	TossWinner  []TeamID
	TossField   []bool // true if the toss winner chose to field
	Winner      []TeamID
	Playoff     []bool
}

// Len returns the number of matches.
func (m *Matches) Len() int { return len(m.CricsheetID) }

// Store is the whole in-memory corpus.
type Store struct {
	D   Deliveries
	Inn Innings
	M   Matches

	Players []Player
	Teams   []string
	Venues  []string
	Cities  []string

	byCricsheetID map[string]PlayerID
}

// PlayerByCricsheetID resolves a Cricsheet person identifier to a PlayerID.
func (s *Store) PlayerByCricsheetID(id string) (PlayerID, bool) {
	if s.byCricsheetID == nil {
		s.byCricsheetID = make(map[string]PlayerID, len(s.Players))
		for i, p := range s.Players {
			s.byCricsheetID[p.CricsheetID] = PlayerID(i)
		}
	}
	p, ok := s.byCricsheetID[id]
	return p, ok
}

// PlayerName returns the display name for a PlayerID, or "?" if out of range.
func (s *Store) PlayerName(id PlayerID) string {
	if int(id) >= len(s.Players) {
		return "?"
	}
	return s.Players[id].Name
}
