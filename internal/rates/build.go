package rates

import (
	"encoding/gob"
	"fmt"
	"os"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
)

// Role is which side of the contest a rate describes.
type Role uint8

const (
	Bowling Role = iota
	Batting
)

func (r Role) String() string {
	if r == Bowling {
		return "bowling"
	}
	return "batting"
}

// Opp classifies the opposition, and is what makes these matchup rates rather
// than career averages.
//
// For a bowler the opposition is the batter's handedness, because that is what
// decides whether the ball turns or swings into the bat or away from it. For a
// batter it is pace against spin, which is the split that actually changes how
// they play. Finer opposition classes exist, but the corpus cannot support
// per-player rates at that resolution.
type Opp uint8

const (
	VsRightHand Opp = iota // bowling role
	VsLeftHand
)

const (
	VsPace Opp = iota // batting role
	VsSpin
)

// NumOpps is the number of opposition classes in either role.
const NumOpps = 2

func (o Opp) String(r Role) string {
	if r == Bowling {
		if o == VsRightHand {
			return "vs RHB"
		}
		return "vs LHB"
	}
	if o == VsPace {
		return "vs pace"
	}
	return "vs spin"
}

// NumCells is the number of (role, phase, opposition) cells.
const NumCells = 2 * corpus.NumPhases * NumOpps

// CellIndex packs a cell into a dense index.
func CellIndex(r Role, p corpus.Phase, o Opp) int {
	return (int(r)*corpus.NumPhases+int(p))*NumOpps + int(o)
}

// UnpackCell is the inverse of CellIndex.
func UnpackCell(i int) (Role, corpus.Phase, Opp) {
	o := Opp(i % NumOpps)
	i /= NumOpps
	p := corpus.Phase(i % corpus.NumPhases)
	return Role(i / corpus.NumPhases), p, o
}

// CellName renders a cell for a report.
func CellName(i int) string {
	r, p, o := UnpackCell(i)
	return fmt.Sprintf("%s %s %s", r, p, o.String(r))
}

// CellStats describes one cell's population, shared by every player in it.
type CellStats struct {
	Prior      [corpus.NumOutcomes]float64 // pooled population distribution
	Kappa      float64                     // fitted prior concentration
	Deliveries int
	Players    int // players with at least one delivery in this cell

	// MeanRuns is the average runs conceded on a delivery of each outcome
	// class. Most are exact by definition; the wicket, wide and no-ball classes
	// are not, because a wicket ball can concede runs and a wide can go to the
	// boundary, so they are measured rather than assumed.
	MeanRuns [corpus.NumOutcomes]float64
}

// CellRates is one player's rates in one cell.
type CellRates struct {
	Deliveries int
	Counts     [corpus.NumOutcomes]int
	Posterior  [corpus.NumOutcomes]float64

	// Weight is the share of the posterior that comes from this player's own
	// record rather than from the population. It travels with the rate so that
	// a consumer can always tell a measurement from an assumption.
	Weight float64
}

// ExpectedRuns returns the mean runs conceded per delivery under the posterior.
func (c CellRates) ExpectedRuns(s CellStats) float64 {
	total := 0.0
	for k := range corpus.NumOutcomes {
		total += c.Posterior[k] * s.MeanRuns[k]
	}
	return total
}

// legalShare returns the expected fraction of deliveries that are legal balls.
// Wides and no-balls concede runs without consuming one.
func (c CellRates) legalShare() float64 {
	return 1 - c.Posterior[corpus.Wide] - c.Posterior[corpus.NoBall]
}

// Economy returns runs conceded per over of legal balls.
func (c CellRates) Economy(s CellStats) float64 {
	legal := c.legalShare()
	if legal <= 0 {
		return 0
	}
	return 6 * c.ExpectedRuns(s) / legal
}

// StrikeRate returns runs per hundred legal balls, the batting view of the same
// posterior.
func (c CellRates) StrikeRate(s CellStats) float64 {
	legal := c.legalShare()
	if legal <= 0 {
		return 0
	}
	return 100 * c.ExpectedRuns(s) / legal
}

// BallsPerWicket returns legal balls per dismissal.
func (c CellRates) BallsPerWicket() float64 {
	w := c.Posterior[corpus.Wicket]
	if w <= 0 {
		return 0
	}
	return c.legalShare() / w
}

// PlayerRates holds every cell for one player.
type PlayerRates struct {
	CricsheetID string
	Name        string
	Cells       [NumCells]CellRates
}

// Table is the fitted rate table: the artifact the simulator consumes.
type Table struct {
	Cells   [NumCells]CellStats
	Players []PlayerRates // indexed by corpus.PlayerID
}

// Options bounds what the fit is allowed to see.
type Options struct {
	// MaxSeason excludes every match after this edition year. Zero means no
	// bound.
	//
	// This exists to stop the rate table leaking the future into a held-out
	// evaluation. Rates fitted over every season and then used as features on
	// the last two seasons would already encode the answer, and the model would
	// look far better than it is.
	MaxSeason uint16
}

// Build fits the rate table from the corpus and the attribute table.
//
// A delivery only contributes to a cell when the opposing player's attribute is
// known, because an unclassified matchup is not evidence about a matchup. This
// is why the attribute work had to come first.
func Build(s *corpus.Store, a attr.Table, opt Options) *Table {
	t := &Table{Players: make([]PlayerRates, len(s.Players))}
	for i, p := range s.Players {
		t.Players[i].CricsheetID = p.CricsheetID
		t.Players[i].Name = p.Name
	}

	// Resolve attributes once per player rather than per delivery.
	hand := make([]attr.Hand, len(s.Players))
	spin := make([]bool, len(s.Players))
	knownClass := make([]bool, len(s.Players))
	for i, p := range s.Players {
		hand[i], _ = a.BatOf(p.CricsheetID)
		if c, ok := a.BowlOf(p.CricsheetID); ok {
			knownClass[i] = true
			spin[i] = c.IsSpin()
		}
	}

	var runsIn [NumCells][corpus.NumOutcomes]float64

	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if s.Inn.SuperOver[inn] {
			continue
		}
		if opt.MaxSeason != 0 && s.M.Season[s.Inn.Match[inn]] > opt.MaxSeason {
			continue
		}
		bat, bowl := s.D.Batter[i], s.D.Bowler[i]
		if bat == corpus.NoPlayer || bowl == corpus.NoPlayer {
			continue
		}
		phase := corpus.PhaseOf(s.D.Over[i])
		outcome := s.D.Classify(i)
		runs := float64(s.D.TotalRuns(i))

		// The bowler's cell is keyed by the batter's handedness.
		if h := hand[bat]; h != attr.HandUnknown {
			o := VsRightHand
			if h == attr.LeftHandBat {
				o = VsLeftHand
			}
			c := CellIndex(Bowling, phase, o)
			t.Players[bowl].Cells[c].Counts[outcome]++
			t.Players[bowl].Cells[c].Deliveries++
			t.Cells[c].Deliveries++
			runsIn[c][outcome] += runs
		}

		// The batter's cell is keyed by whether the bowler turns it or seams it.
		if knownClass[bowl] {
			o := VsPace
			if spin[bowl] {
				o = VsSpin
			}
			c := CellIndex(Batting, phase, o)
			t.Players[bat].Cells[c].Counts[outcome]++
			t.Players[bat].Cells[c].Deliveries++
			t.Cells[c].Deliveries++
			runsIn[c][outcome] += runs
		}
	}

	// Nominal run values, used only where a cell never produced an outcome
	// class and so has no measurement to average.
	nominal := [corpus.NumOutcomes]float64{0, 1, 2, 3, 4, 6, 0, 1, 1}

	for c := range NumCells {
		counts := make([][]int, 0, len(t.Players))
		index := make([]int, 0, len(t.Players))
		var pooled [corpus.NumOutcomes]int

		for i := range t.Players {
			cr := &t.Players[i].Cells[c]
			if cr.Deliveries == 0 {
				continue
			}
			row := make([]int, corpus.NumOutcomes)
			for k := range corpus.NumOutcomes {
				row[k] = cr.Counts[k]
				pooled[k] += cr.Counts[k]
			}
			counts = append(counts, row)
			index = append(index, i)
		}
		t.Cells[c].Players = len(counts)

		for k := range corpus.NumOutcomes {
			if pooled[k] > 0 {
				t.Cells[c].MeanRuns[k] = runsIn[c][k] / float64(pooled[k])
			} else {
				t.Cells[c].MeanRuns[k] = nominal[k]
			}
		}

		post, prior, kappa := FitAndShrink(counts, corpus.NumOutcomes)
		copy(t.Cells[c].Prior[:], prior)
		t.Cells[c].Kappa = kappa

		for j, i := range index {
			cr := &t.Players[i].Cells[c]
			copy(cr.Posterior[:], post[j])
			cr.Weight = ShrinkageWeight(cr.Deliveries, kappa)
		}
		// A player with no deliveries in this cell still needs a usable rate:
		// the population mean, carrying zero weight so the caller can see that
		// nothing about this player informed it.
		for i := range t.Players {
			if t.Players[i].Cells[c].Deliveries == 0 {
				t.Players[i].Cells[c].Posterior = t.Cells[c].Prior
				t.Players[i].Cells[c].Weight = 0
			}
		}
	}
	return t
}

// Save writes the fitted table.
//
// gob rather than a hand-rolled codec: this artifact is around a megabyte and
// is read once at boot, so the encoding is not on any hot path, and the schema
// changes more often than the corpus does.
func Save(t *Table, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("rates: create %s: %w", path, err)
	}
	defer f.Close()
	if err := gob.NewEncoder(f).Encode(t); err != nil {
		return fmt.Errorf("rates: encode %s: %w", path, err)
	}
	return nil
}

// Load reads a fitted table.
func Load(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("rates: open %s: %w", path, err)
	}
	defer f.Close()
	var t Table
	if err := gob.NewDecoder(f).Decode(&t); err != nil {
		return nil, fmt.Errorf("rates: decode %s: %w", path, err)
	}
	return &t, nil
}

// PhaseRate is a player's rate for one role and phase, combined across both
// opposition classes and weighted by how much data each holds.
type PhaseRate struct {
	Deliveries  int
	Weight      float64 // share of the estimate coming from this player's record
	Economy     float64 // shrunk, runs per over
	RawEconomy  float64 // what the unshrunk record alone would say
	StrikeRate  float64 // shrunk, runs per hundred balls
	RawStrike   float64
	BallsPerWkt float64
}

// Phase combines a player's two opposition cells for one role and phase.
//
// Reporting the raw figure alongside the shrunk one is deliberate: the gap
// between them is the whole point of the model, and hiding it would make the
// shrinkage impossible to sanity-check.
func (t *Table) Phase(player int, role Role, phase corpus.Phase) PhaseRate {
	var pr PhaseRate
	if player < 0 || player >= len(t.Players) {
		return pr
	}

	var expRuns, legal, wickets float64
	var rawRuns, rawLegal float64
	var weighted float64

	for o := range NumOpps {
		c := CellIndex(role, phase, Opp(o))
		cr := t.Players[player].Cells[c]
		st := t.Cells[c]
		if cr.Deliveries == 0 {
			continue
		}
		n := float64(cr.Deliveries)

		expRuns += n * cr.ExpectedRuns(st)
		legal += n * cr.legalShare()
		wickets += n * cr.Posterior[corpus.Wicket]
		weighted += n * cr.Weight
		pr.Deliveries += cr.Deliveries

		for k := range corpus.NumOutcomes {
			rawRuns += float64(cr.Counts[k]) * st.MeanRuns[k]
		}
		rawLegal += n - float64(cr.Counts[corpus.Wide]) - float64(cr.Counts[corpus.NoBall])
	}

	if pr.Deliveries == 0 {
		return pr
	}
	pr.Weight = weighted / float64(pr.Deliveries)
	if legal > 0 {
		pr.Economy = 6 * expRuns / legal
		pr.StrikeRate = 100 * expRuns / legal
	}
	if rawLegal > 0 {
		pr.RawEconomy = 6 * rawRuns / rawLegal
		pr.RawStrike = 100 * rawRuns / rawLegal
	}
	if wickets > 0 {
		pr.BallsPerWkt = legal / wickets
	}
	return pr
}
