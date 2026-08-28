package corpus

// Filtered aggregation over the delivery table.
//
// Every question the game asks of history has the same shape: take a subset of
// deliveries and count what happened. "Bumrah at the death against
// left-handers" is a filter and a tally, not a traversal, so it is served by a
// linear scan over the typed columns rather than by an index.

// Outcome is the ball-by-ball outcome space.
//
// These nine classes are what the outcome model predicts, so the enum is
// defined here rather than in the model package: the corpus, the simulator and
// the model must all agree on what a ball can do.
type Outcome uint8

const (
	Dot Outcome = iota
	One
	Two
	Three
	Four
	Six
	Wicket
	Wide
	NoBall
	numOutcomes
)

// NumOutcomes is the size of the outcome space.
const NumOutcomes = int(numOutcomes)

var outcomeNames = [numOutcomes]string{"0", "1", "2", "3", "4", "6", "W", "wd", "nb"}

func (o Outcome) String() string {
	if int(o) >= NumOutcomes {
		return "?"
	}
	return outcomeNames[o]
}

// Classify reduces delivery i to a single outcome.
//
// A wicket dominates: when a ball both concedes runs and takes a wicket, the
// wicket is the event that matters to every model downstream. Wides and
// no-balls come next, because they do not consume a legal ball and so cannot be
// scored as one. Everything else is classified by runs off the bat, since that
// is what the batter did; byes and leg-byes are fielding events and are counted
// separately in the distribution's run total.
func (d *Deliveries) Classify(i int) Outcome {
	if d.Wicket[i].CostsWicket() {
		return Wicket
	}
	if d.Wides[i] > 0 {
		return Wide
	}
	if d.NoBalls[i] > 0 {
		return NoBall
	}
	switch r := d.RunsBat[i]; {
	case r == 0:
		return Dot
	case r == 1:
		return One
	case r == 2:
		return Two
	case r == 3:
		return Three
	case r >= 6:
		return Six
	default:
		// Four, and the rare five off the bat from an overthrow.
		return Four
	}
}

// Filter selects a subset of deliveries. The zero value matches everything
// except super overs.
type Filter struct {
	// Bowler and Batter restrict to one player. NoPlayer matches any.
	Bowler PlayerID
	Batter PlayerID

	// MinOver and MaxOver bound the over index inclusively. The zero value of
	// MaxOver is treated as "no upper bound", so the zero Filter is unbounded.
	MinOver uint8
	MaxOver uint8

	// SeasonFrom and SeasonTo bound the edition year inclusively. Zero means
	// unbounded on that side.
	SeasonFrom uint16
	SeasonTo   uint16

	// BatterIn and BowlerIn are membership masks indexed by PlayerID. A nil
	// mask matches any player. Masks rather than predicates so that a caller
	// can filter on attributes, such as batting handedness, without this
	// package depending on the attribute table.
	BatterIn []bool
	BowlerIn []bool

	// IncludeSuperOvers is off by default: a one-over shootout has none of the
	// phase structure the game models.
	IncludeSuperOvers bool
}

// NewFilter returns a Filter matching every delivery outside a super over.
func NewFilter() Filter {
	return Filter{Bowler: NoPlayer, Batter: NoPlayer, MaxOver: 255}
}

// Phase restricts the filter to one innings phase.
func (f Filter) Phase(p Phase) Filter {
	switch p {
	case PhasePowerplay:
		f.MinOver, f.MaxOver = 0, 5
	case PhaseMiddle:
		f.MinOver, f.MaxOver = 6, 14
	case PhaseDeath:
		f.MinOver, f.MaxOver = 15, 19
	}
	return f
}

// Distribution is the tally a filtered scan produces.
type Distribution struct {
	Deliveries int // every ball matched, legal or not
	LegalBalls int
	Outcomes   [NumOutcomes]int

	RunsOffBat int
	Extras     int
	TotalRuns  int
	Wickets    int // dismissals that cost the batting side a wicket
	BowlerWkts int // of those, the ones credited to the bowler
}

// Economy returns runs conceded per over, charged to the bowler.
func (d Distribution) Economy() float64 {
	if d.LegalBalls == 0 {
		return 0
	}
	return 6 * float64(d.RunsOffBat+d.Extras) / float64(d.LegalBalls)
}

// StrikeRate returns runs per hundred balls faced.
func (d Distribution) StrikeRate() float64 {
	if d.LegalBalls == 0 {
		return 0
	}
	return 100 * float64(d.RunsOffBat) / float64(d.LegalBalls)
}

// BoundaryRate returns the share of legal balls hit for four or six.
func (d Distribution) BoundaryRate() float64 {
	if d.LegalBalls == 0 {
		return 0
	}
	return float64(d.Outcomes[Four]+d.Outcomes[Six]) / float64(d.LegalBalls)
}

// DotRate returns the share of legal balls that were dots.
func (d Distribution) DotRate() float64 {
	if d.LegalBalls == 0 {
		return 0
	}
	return float64(d.Outcomes[Dot]) / float64(d.LegalBalls)
}

// BallsPerWicket returns the strike rate in the bowling sense.
func (d Distribution) BallsPerWicket() float64 {
	if d.BowlerWkts == 0 {
		return 0
	}
	return float64(d.LegalBalls) / float64(d.BowlerWkts)
}

// Aggregate scans the delivery table and tallies everything the filter matches.
func (s *Store) Aggregate(f Filter) Distribution {
	var d Distribution

	// Hoist the innings-level and match-level lookups: they are indexed by
	// innings, not by delivery, so recomputing them per ball is wasted work.
	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if !f.IncludeSuperOvers && s.Inn.SuperOver[inn] {
			continue
		}
		if over := s.D.Over[i]; over < f.MinOver || over > f.MaxOver {
			continue
		}
		if f.Bowler != NoPlayer && s.D.Bowler[i] != f.Bowler {
			continue
		}
		if f.Batter != NoPlayer && s.D.Batter[i] != f.Batter {
			continue
		}
		if f.BowlerIn != nil {
			b := s.D.Bowler[i]
			if int(b) >= len(f.BowlerIn) || !f.BowlerIn[b] {
				continue
			}
		}
		if f.BatterIn != nil {
			b := s.D.Batter[i]
			if int(b) >= len(f.BatterIn) || !f.BatterIn[b] {
				continue
			}
		}
		if f.SeasonFrom != 0 || f.SeasonTo != 0 {
			season := s.M.Season[s.Inn.Match[inn]]
			if f.SeasonFrom != 0 && season < f.SeasonFrom {
				continue
			}
			if f.SeasonTo != 0 && season > f.SeasonTo {
				continue
			}
		}

		d.Deliveries++
		if s.D.Legal[i] {
			d.LegalBalls++
		}
		d.Outcomes[s.D.Classify(i)]++
		d.RunsOffBat += int(s.D.RunsBat[i])
		d.Extras += s.D.TotalRuns(i) - int(s.D.RunsBat[i])
		d.TotalRuns += s.D.TotalRuns(i)
		if s.D.Wicket[i].CostsWicket() {
			d.Wickets++
			if s.D.Wicket[i].CreditedToBowler() {
				d.BowlerWkts++
			}
		}
	}
	return d
}

// MaskOf builds a membership mask over PlayerIDs from a predicate on the
// player's Cricsheet identifier.
func (s *Store) MaskOf(pred func(cricsheetID string) bool) []bool {
	m := make([]bool, len(s.Players))
	for i, p := range s.Players {
		m[i] = pred(p.CricsheetID)
	}
	return m
}
