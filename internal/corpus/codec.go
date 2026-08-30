package corpus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// The corpus is serialised as a flat, little-endian, column-oriented blob. The
// ETL writes it once; the server reads it at boot with a single os.ReadFile and
// a pass of pointer-free decoding. There is no JSON, no reflection and no
// per-record allocation on the load path.

const (
	magic         = "MHTNCORP"
	formatVersion = uint32(1)
)

// ErrBadFormat is returned when a corpus file is truncated or not a corpus.
var ErrBadFormat = errors.New("corpus: malformed corpus file")

type encoder struct{ b []byte }

func (e *encoder) u32(v uint32) { e.b = binary.LittleEndian.AppendUint32(e.b, v) }

func (e *encoder) str(s string) {
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
}

func (e *encoder) strs(v []string) {
	e.u32(uint32(len(v)))
	for _, s := range v {
		e.str(s)
	}
}

func encU8[T ~uint8](e *encoder, v []T) {
	e.u32(uint32(len(v)))
	for _, x := range v {
		e.b = append(e.b, byte(x))
	}
}

func encU16[T ~uint16](e *encoder, v []T) {
	e.u32(uint32(len(v)))
	for _, x := range v {
		e.b = binary.LittleEndian.AppendUint16(e.b, uint16(x))
	}
}

func encU32[T ~uint32](e *encoder, v []T) {
	e.u32(uint32(len(v)))
	for _, x := range v {
		e.b = binary.LittleEndian.AppendUint32(e.b, uint32(x))
	}
}

func encI32(e *encoder, v []int32) {
	e.u32(uint32(len(v)))
	for _, x := range v {
		e.b = binary.LittleEndian.AppendUint32(e.b, uint32(x))
	}
}

func encBool(e *encoder, v []bool) {
	e.u32(uint32(len(v)))
	for _, x := range v {
		var b byte
		if x {
			b = 1
		}
		e.b = append(e.b, b)
	}
}

type decoder struct {
	b   []byte
	off int
	err error
}

func (d *decoder) fail() {
	if d.err == nil {
		d.err = ErrBadFormat
	}
}

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || d.off+n > len(d.b) {
		d.fail()
		return nil
	}
	s := d.b[d.off : d.off+n]
	d.off += n
	return s
}

func (d *decoder) u32() uint32 {
	s := d.take(4)
	if s == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(s)
}

func (d *decoder) str() string {
	n := int(d.u32())
	s := d.take(n)
	if s == nil {
		return ""
	}
	return string(s)
}

func (d *decoder) strs() []string {
	n := int(d.u32())
	if d.err != nil || n < 0 || n > len(d.b) {
		d.fail()
		return nil
	}
	v := make([]string, n)
	for i := range v {
		v[i] = d.str()
	}
	return v
}

// n returns a validated element count, rejecting lengths that cannot possibly
// fit in the remaining buffer so a corrupt header cannot force a huge alloc.
func (d *decoder) n(width int) int {
	n := int(d.u32())
	if d.err != nil || n < 0 || n*width > len(d.b)-d.off {
		d.fail()
		return 0
	}
	return n
}

func decU8[T ~uint8](d *decoder) []T {
	n := d.n(1)
	v := make([]T, n)
	s := d.take(n)
	for i := range v {
		v[i] = T(s[i])
	}
	return v
}

func decU16[T ~uint16](d *decoder) []T {
	n := d.n(2)
	v := make([]T, n)
	s := d.take(2 * n)
	for i := range v {
		v[i] = T(binary.LittleEndian.Uint16(s[2*i:]))
	}
	return v
}

func decU32[T ~uint32](d *decoder) []T {
	n := d.n(4)
	v := make([]T, n)
	s := d.take(4 * n)
	for i := range v {
		v[i] = T(binary.LittleEndian.Uint32(s[4*i:]))
	}
	return v
}

func decI32(d *decoder) []int32 {
	n := d.n(4)
	v := make([]int32, n)
	s := d.take(4 * n)
	for i := range v {
		v[i] = int32(binary.LittleEndian.Uint32(s[4*i:]))
	}
	return v
}

func decBool(d *decoder) []bool {
	n := d.n(1)
	v := make([]bool, n)
	s := d.take(n)
	for i := range v {
		v[i] = s[i] != 0
	}
	return v
}

// Encode serialises the store to a byte slice.
func (s *Store) Encode() []byte {
	e := &encoder{b: make([]byte, 0, 24<<20)}
	e.b = append(e.b, magic...)
	e.u32(formatVersion)

	e.u32(uint32(len(s.Players)))
	for _, p := range s.Players {
		e.str(p.CricsheetID)
		e.str(p.Name)
		e.str(p.CricinfoID)
		e.strs(p.Aliases)
	}
	e.strs(s.Teams)
	e.strs(s.Venues)
	e.strs(s.Cities)

	encU32(e, s.M.CricsheetID)
	encI32(e, s.M.Date)
	encU16(e, s.M.Season)
	encU8(e, s.M.Venue)
	encU8(e, s.M.City)
	encU8(e, s.M.TeamA)
	encU8(e, s.M.TeamB)
	encU8(e, s.M.TossWinner)
	encBool(e, s.M.TossField)
	encU8(e, s.M.Winner)
	encBool(e, s.M.Playoff)

	encU16(e, s.Inn.Match)
	encU8(e, s.Inn.BattingTeam)
	encU8(e, s.Inn.BowlingTeam)
	encU16(e, s.Inn.Target)
	encBool(e, s.Inn.SuperOver)
	encBool(e, s.Inn.Miscounted)
	encU32(e, s.Inn.Start)
	encU32(e, s.Inn.End)
	encU16(e, s.Inn.Runs)
	encU8(e, s.Inn.Wickets)
	encU16(e, s.Inn.LegalBalls)

	encU32(e, s.D.Innings)
	encU8(e, s.D.Over)
	encU8(e, s.D.BallInOver)
	encU8(e, s.D.LegalBall)
	encBool(e, s.D.Legal)
	encU16(e, s.D.Batter)
	encU16(e, s.D.NonStriker)
	encU16(e, s.D.Bowler)
	encU8(e, s.D.RunsBat)
	encU8(e, s.D.Wides)
	encU8(e, s.D.NoBalls)
	encU8(e, s.D.Byes)
	encU8(e, s.D.LegByes)
	encU8(e, s.D.Penalty)
	encU8(e, s.D.Wicket)
	encU16(e, s.D.PlayerOut)
	encU16(e, s.D.ScoreBefore)
	encU8(e, s.D.WicketsBefore)
	encU8(e, s.D.LegalBallsBefore)

	return e.b
}

// Decode parses a corpus blob produced by Encode.
func Decode(b []byte) (*Store, error) {
	d := &decoder{b: b}
	if h := d.take(len(magic)); string(h) != magic {
		return nil, fmt.Errorf("%w: bad magic", ErrBadFormat)
	}
	if v := d.u32(); v != formatVersion {
		return nil, fmt.Errorf("corpus: format version %d, want %d (regenerate with pavetl)", v, formatVersion)
	}

	s := &Store{}
	np := d.n(4)
	s.Players = make([]Player, np)
	for i := range s.Players {
		s.Players[i] = Player{
			CricsheetID: d.str(),
			Name:        d.str(),
			CricinfoID:  d.str(),
			Aliases:     d.strs(),
		}
	}
	s.Teams = d.strs()
	s.Venues = d.strs()
	s.Cities = d.strs()

	s.M.CricsheetID = decU32[uint32](d)
	s.M.Date = decI32(d)
	s.M.Season = decU16[uint16](d)
	s.M.Venue = decU8[VenueID](d)
	s.M.City = decU8[CityID](d)
	s.M.TeamA = decU8[TeamID](d)
	s.M.TeamB = decU8[TeamID](d)
	s.M.TossWinner = decU8[TeamID](d)
	s.M.TossField = decBool(d)
	s.M.Winner = decU8[TeamID](d)
	s.M.Playoff = decBool(d)

	s.Inn.Match = decU16[MatchID](d)
	s.Inn.BattingTeam = decU8[TeamID](d)
	s.Inn.BowlingTeam = decU8[TeamID](d)
	s.Inn.Target = decU16[uint16](d)
	s.Inn.SuperOver = decBool(d)
	s.Inn.Miscounted = decBool(d)
	s.Inn.Start = decU32[uint32](d)
	s.Inn.End = decU32[uint32](d)
	s.Inn.Runs = decU16[uint16](d)
	s.Inn.Wickets = decU8[uint8](d)
	s.Inn.LegalBalls = decU16[uint16](d)

	s.D.Innings = decU32[InningsID](d)
	s.D.Over = decU8[uint8](d)
	s.D.BallInOver = decU8[uint8](d)
	s.D.LegalBall = decU8[uint8](d)
	s.D.Legal = decBool(d)
	s.D.Batter = decU16[PlayerID](d)
	s.D.NonStriker = decU16[PlayerID](d)
	s.D.Bowler = decU16[PlayerID](d)
	s.D.RunsBat = decU8[uint8](d)
	s.D.Wides = decU8[uint8](d)
	s.D.NoBalls = decU8[uint8](d)
	s.D.Byes = decU8[uint8](d)
	s.D.LegByes = decU8[uint8](d)
	s.D.Penalty = decU8[uint8](d)
	s.D.Wicket = decU8[WicketKind](d)
	s.D.PlayerOut = decU16[PlayerID](d)
	s.D.ScoreBefore = decU16[uint16](d)
	s.D.WicketsBefore = decU8[uint8](d)
	s.D.LegalBallsBefore = decU8[uint8](d)

	if d.err != nil {
		return nil, d.err
	}
	return s, nil
}

// Save writes the store to path.
func Save(s *Store, path string) error {
	if err := os.WriteFile(path, s.Encode(), 0o644); err != nil {
		return fmt.Errorf("corpus: save %s: %w", path, err)
	}
	return nil
}

// Load reads a corpus from path.
func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("corpus: load %s: %w", path, err)
	}
	s, err := Decode(b)
	if err != nil {
		return nil, fmt.Errorf("corpus: load %s: %w", path, err)
	}
	return s, nil
}
