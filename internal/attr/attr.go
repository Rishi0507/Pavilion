// Package attr holds player attributes that the ball-by-ball corpus does not
// carry: batting handedness and bowling type.
//
// Cricsheet records neither, and the matchup model depends on both. The
// attribute table is therefore a separate, checked-in artifact with explicit
// provenance on every row, so that a sourced fact and a guess can never be
// confused for one another.
package attr

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Hand is a batter's handedness.
type Hand uint8

const (
	HandUnknown Hand = iota
	RightHandBat
	LeftHandBat
)

func (h Hand) String() string {
	switch h {
	case RightHandBat:
		return "RHB"
	case LeftHandBat:
		return "LHB"
	}
	return ""
}

// ParseHand reads a canonical hand code.
func ParseHand(s string) Hand {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "RHB":
		return RightHandBat
	case "LHB":
		return LeftHandBat
	}
	return HandUnknown
}

// BowlClass is the bowling taxonomy the matchup model uses.
//
// The six classes are the distinctions that actually change how a batter plays:
// which arm the ball comes from, and whether it turns away from or into the
// bat. Finer gradations (fast versus fast-medium) are kept in the raw style
// string but do not form separate classes, because the corpus cannot support
// reliable rates at that resolution.
type BowlClass uint8

const (
	BowlUnknown     BowlClass = iota
	RightArmPace              // right-arm fast through medium
	LeftArmPace               // left-arm fast through medium
	OffBreak                  // right-arm finger spin
	LegBreak                  // right-arm wrist spin
	LeftArmOrthodox           // left-arm finger spin
	LeftArmWrist              // left-arm wrist spin, the chinaman
)

var bowlCodes = map[BowlClass]string{
	RightArmPace:    "RAP",
	LeftArmPace:     "LAP",
	OffBreak:        "OB",
	LegBreak:        "LB",
	LeftArmOrthodox: "SLA",
	LeftArmWrist:    "SLW",
}

func (b BowlClass) String() string { return bowlCodes[b] }

// IsSpin reports whether the class is a spin type.
func (b BowlClass) IsSpin() bool {
	switch b {
	case OffBreak, LegBreak, LeftArmOrthodox, LeftArmWrist:
		return true
	}
	return false
}

// IsPace reports whether the class is a seam or swing type.
func (b BowlClass) IsPace() bool { return b == RightArmPace || b == LeftArmPace }

// ParseBowlClass reads a canonical class code.
func ParseBowlClass(s string) BowlClass {
	s = strings.ToUpper(strings.TrimSpace(s))
	for k, v := range bowlCodes {
		if v == s {
			return k
		}
	}
	return BowlUnknown
}

// Provenance records where a row's values came from. It is never empty and
// never inferred by omission.
type Provenance string

const (
	// Sourced means the value was read from an external reference.
	Sourced Provenance = "sourced"
	// Manual means a human entered or corrected the value by hand. Manual rows
	// always win over sourced ones.
	Manual Provenance = "manual"
	// Inferred means the value was derived from delivery signatures. Inferred
	// values are written to the review file only and never to the main table.
	Inferred Provenance = "inferred"
)

// Player is one row of the attribute table.
type Player struct {
	CricsheetID string
	Name        string
	CricinfoID  string
	Bat         Hand
	Bowl        BowlClass
	BatRaw      string // verbatim source string, so normalisation stays auditable
	BowlRaw     string
	Provenance  Provenance
	Source      string // e.g. "wikipedia:Jasprit Bumrah"
	Retrieved   string // RFC3339 date
}

var csvHeader = []string{
	"cricsheet_id", "name", "cricinfo_id",
	"batting_hand", "bowling_class",
	"batting_raw", "bowling_raw",
	"provenance", "source", "retrieved",
}

// Table is the attribute table, keyed by Cricsheet person identifier.
type Table map[string]Player

// Bat returns a player's handedness and whether it is known.
func (t Table) BatOf(cricsheetID string) (Hand, bool) {
	p, ok := t[cricsheetID]
	return p.Bat, ok && p.Bat != HandUnknown
}

// BowlOf returns a player's bowling class and whether it is known.
func (t Table) BowlOf(cricsheetID string) (BowlClass, bool) {
	p, ok := t[cricsheetID]
	return p.Bowl, ok && p.Bowl != BowlUnknown
}

// Load reads an attribute CSV. A missing file is not an error: it yields an
// empty table, so a first run needs no seeding.
func Load(path string) (Table, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Table{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("attr: open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	head, err := r.Read()
	if errors.Is(err, io.EOF) {
		return Table{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("attr: read %s header: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(h)] = i
	}
	need := []string{"cricsheet_id", "batting_hand", "bowling_class", "provenance"}
	for _, n := range need {
		if _, ok := col[n]; !ok {
			return nil, fmt.Errorf("attr: %s missing required column %q", path, n)
		}
	}
	get := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	t := Table{}
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("attr: read %s line %d: %w", path, line, err)
		}
		id := get(rec, "cricsheet_id")
		if id == "" {
			return nil, fmt.Errorf("attr: %s line %d: empty cricsheet_id", path, line)
		}
		prov := Provenance(get(rec, "provenance"))
		switch prov {
		case Sourced, Manual, Inferred:
		default:
			return nil, fmt.Errorf("attr: %s line %d: provenance %q must be sourced, manual or inferred",
				path, line, prov)
		}
		t[id] = Player{
			CricsheetID: id,
			Name:        get(rec, "name"),
			CricinfoID:  get(rec, "cricinfo_id"),
			Bat:         ParseHand(get(rec, "batting_hand")),
			Bowl:        ParseBowlClass(get(rec, "bowling_class")),
			BatRaw:      get(rec, "batting_raw"),
			BowlRaw:     get(rec, "bowling_raw"),
			Provenance:  prov,
			Source:      get(rec, "source"),
			Retrieved:   get(rec, "retrieved"),
		}
	}
	return t, nil
}

// Save writes an attribute CSV, sorted by identifier so the checked-in file has
// a stable diff.
func Save(t Table, path string) error {
	ids := make([]string, 0, len(t))
	for id := range t {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("attr: create %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return fmt.Errorf("attr: write %s: %w", path, err)
	}
	for _, id := range ids {
		p := t[id]
		if err := w.Write([]string{
			p.CricsheetID, p.Name, p.CricinfoID,
			p.Bat.String(), p.Bowl.String(),
			p.BatRaw, p.BowlRaw,
			string(p.Provenance), p.Source, p.Retrieved,
		}); err != nil {
			return fmt.Errorf("attr: write %s: %w", path, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("attr: flush %s: %w", path, err)
	}
	return nil
}

// All returns the table's rows in identifier order.
func (t Table) All() []Player {
	ids := make([]string, 0, len(t))
	for id := range t {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Player, 0, len(t))
	for _, id := range ids {
		out = append(out, t[id])
	}
	return out
}
