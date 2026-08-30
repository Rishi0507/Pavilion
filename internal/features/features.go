// Package features turns a match situation into the vector the outcome model
// consumes.
//
// There is exactly one implementation, in Go, and both the training exporter
// and the live server call it. That is deliberate: the classic way to ship a
// model that works in the notebook and fails in production is to compute
// features twice, once in Python for training and once in the serving language,
// and let the two drift.
//
// Everything derived from history is supplied to the extractor rather than
// looked up inside it, because what counts as "history" differs between
// training and serving. Career-to-date matchup totals contain the very delivery
// being predicted; the first version of this model scored 39% of its feature
// importance on exactly that leak and came out worse than predicting the
// population average.
package features

import (
	"pavilion/internal/attr"
	"pavilion/internal/corpus"
	"pavilion/internal/graph"
	"pavilion/internal/rates"
)

// Names lists the features in the order Extract writes them. The model artifact
// records this list, and loading refuses a model whose feature order disagrees.
var Names = []string{
	"innings",
	"over",
	"balls_remaining",
	"wickets_in_hand",
	"score",
	"current_run_rate",
	"chasing",
	"runs_required",
	"required_rate",
	"striker_balls",
	"new_batter",
	"partnership_runs",
	"partnership_balls",
	"batter_left_handed",
	"bowler_spin",
	"moves_away",
	"bat_exp_runs",
	"bat_dot_rate",
	"bat_boundary_rate",
	"bat_wicket_rate",
	"bat_weight",
	"bowl_exp_runs",
	"bowl_dot_rate",
	"bowl_boundary_rate",
	"bowl_wicket_rate",
	"bowl_weight",
	"matchup_balls",
	"matchup_strike_rate",
	"matchup_out_rate",
	"venue_run_rate",
}

// Dim is the number of features.
var Dim = len(Names)

// State is everything known before a delivery is bowled. It is the same shape
// whether reconstructed from history or held by the simulator mid-over.
type State struct {
	Innings          uint8  // 1 or 2
	Over             uint8  // zero-based
	Score            uint16 // runs so far this innings
	Wickets          uint8  // wickets lost
	LegalBallsBowled uint16 // legal balls bowled this innings
	Target           uint16 // runs needed to win; 0 when batting first

	StrikerBalls     uint16 // balls the striker has faced this innings
	PartnershipRuns  uint16
	PartnershipBalls uint16

	Batter      corpus.PlayerID
	Bowler      corpus.PlayerID
	BatterHand  attr.Hand
	BowlerClass attr.BowlClass
	Venue       corpus.VenueID

	// Matchup is the head-to-head record as it stood strictly before this
	// match. The caller supplies it: the server reads it from the graph built
	// at boot, and training uses an expanding window.
	Matchup Matchup
}

// Matchup is one head-to-head record.
type Matchup struct {
	Balls uint32
	Runs  uint32
	Outs  uint32
}

// MatchupOf reads a head-to-head record out of a matchup graph, which is what
// the server does at serving time.
func MatchupOf(g *graph.Graph, bat, bowl corpus.PlayerID) Matchup {
	if g == nil {
		return Matchup{}
	}
	if e, ok := g.Matchup(bat, bowl); ok {
		return Matchup{Balls: e.Balls, Runs: e.Runs, Outs: e.Dismissals}
	}
	return Matchup{}
}

// Context holds the fitted quantities the extractor needs. It is built once,
// bounded to the seasons it is allowed to have seen, and is then read-only.
type Context struct {
	rates        *rates.Table
	venueRunRate []float32
	meanRunRate  float32
}

// NewContext assembles the extraction context. MaxSeason bounds every derived
// quantity, so features used on a later season contain nothing from it.
func NewContext(s *corpus.Store, a attr.Table, maxSeason uint16) *Context {
	c := &Context{rates: rates.Build(s, a, rates.Options{MaxSeason: maxSeason})}

	runs := make([]float64, len(s.Venues))
	balls := make([]float64, len(s.Venues))
	var allRuns, allBalls float64
	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if s.Inn.SuperOver[inn] {
			continue
		}
		m := s.Inn.Match[inn]
		if maxSeason != 0 && s.M.Season[m] > maxSeason {
			continue
		}
		v := s.M.Venue[m]
		r := float64(s.D.TotalRuns(i))
		runs[v] += r
		allRuns += r
		if s.D.Legal[i] {
			balls[v]++
			allBalls++
		}
	}
	if allBalls > 0 {
		c.meanRunRate = float32(allRuns / allBalls)
	}
	c.venueRunRate = make([]float32, len(s.Venues))
	for v := range c.venueRunRate {
		// A venue with almost no history is not evidence about that venue, so
		// it takes the global rate rather than a number built from ten balls.
		if balls[v] >= 600 {
			c.venueRunRate[v] = float32(runs[v] / balls[v])
		} else {
			c.venueRunRate[v] = c.meanRunRate
		}
	}
	return c
}

// Rates exposes the fitted rate table, which the simulator also needs.
func (c *Context) Rates() *rates.Table { return c.rates }

// VenueRunRate returns the historical runs per ball at a venue.
func (c *Context) VenueRunRate(v corpus.VenueID) float32 {
	if int(v) < len(c.venueRunRate) {
		return c.venueRunRate[v]
	}
	return c.meanRunRate
}

// cellSummary reduces a player's posterior in one cell to the four numbers the
// outcome model uses, plus how much of it is that player's own record.
func (c *Context) cellSummary(player corpus.PlayerID, cell int) (expRuns, dot, boundary, wicket, weight float32) {
	if c.rates == nil || int(player) >= len(c.rates.Players) {
		return 0, 0, 0, 0, 0
	}
	cr := c.rates.Players[player].Cells[cell]
	st := c.rates.Cells[cell]
	return float32(cr.ExpectedRuns(st)),
		float32(cr.Posterior[corpus.Dot]),
		float32(cr.Posterior[corpus.Four] + cr.Posterior[corpus.Six]),
		float32(cr.Posterior[corpus.Wicket]),
		float32(cr.Weight)
}

// Extract writes the feature vector for a state into dst, which must have
// length Dim.
func (c *Context) Extract(s State, dst []float32) {
	phase := corpus.PhaseOf(s.Over)

	batOpp := rates.VsPace
	if s.BowlerClass.IsSpin() {
		batOpp = rates.VsSpin
	}
	bowlOpp := rates.VsRightHand
	if s.BatterHand == attr.LeftHandBat {
		bowlOpp = rates.VsLeftHand
	}

	batExp, batDot, batBnd, batWkt, batW := c.cellSummary(s.Batter, rates.CellIndex(rates.Batting, phase, batOpp))
	bowlExp, bowlDot, bowlBnd, bowlWkt, bowlW := c.cellSummary(s.Bowler, rates.CellIndex(rates.Bowling, phase, bowlOpp))

	// The head-to-head goes in as a rate plus the ball count it rests on,
	// rather than as raw totals. Raw totals grow without bound over a career
	// and let a tree identify particular pairings instead of learning what a
	// good matchup looks like; a rate does not, and the ball count still tells
	// the model how much to trust it.
	var mSR, mOutRate float32
	mBalls := float32(s.Matchup.Balls)
	if s.Matchup.Balls > 0 {
		mSR = 100 * float32(s.Matchup.Runs) / mBalls
		mOutRate = float32(s.Matchup.Outs) / mBalls
	}

	ballsRemaining := float32(120 - int(s.LegalBallsBowled))
	if ballsRemaining < 0 {
		ballsRemaining = 0
	}

	var crr float32
	if s.LegalBallsBowled > 0 {
		crr = 6 * float32(s.Score) / float32(s.LegalBallsBowled)
	}

	var chasing, required, reqRate float32
	if s.Target > 0 {
		chasing = 1
		need := int(s.Target) - int(s.Score)
		if need < 0 {
			need = 0
		}
		required = float32(need)
		if ballsRemaining > 0 {
			reqRate = 6 * required / ballsRemaining
		}
	}

	var newBatter float32
	if s.StrikerBalls < 6 {
		newBatter = 1
	}
	var leftHanded float32
	if s.BatterHand == attr.LeftHandBat {
		leftHanded = 1
	}
	var spin float32
	if s.BowlerClass.IsSpin() {
		spin = 1
	}
	var away float32
	if attr.MovesAway(s.BowlerClass, s.BatterHand) {
		away = 1
	}

	venue := c.meanRunRate
	if int(s.Venue) < len(c.venueRunRate) {
		venue = c.venueRunRate[s.Venue]
	}

	copy(dst, []float32{
		float32(s.Innings),
		float32(s.Over),
		ballsRemaining,
		float32(10 - int(s.Wickets)),
		float32(s.Score),
		crr,
		chasing,
		required,
		reqRate,
		float32(s.StrikerBalls),
		newBatter,
		float32(s.PartnershipRuns),
		float32(s.PartnershipBalls),
		leftHanded,
		spin,
		away,
		batExp, batDot, batBnd, batWkt, batW,
		bowlExp, bowlDot, bowlBnd, bowlWkt, bowlW,
		mBalls, mSR, mOutRate,
		venue,
	})
}

// Row is one training example.
type Row struct {
	X      []float32
	Y      corpus.Outcome
	Season uint16
}

// pairKey identifies one batter-bowler pairing in the expanding window.
type pairKey struct{ bat, bowl corpus.PlayerID }

// Walk emits a training row for every delivery, featurised only with what was
// knowable before the match it belongs to.
//
// This is an expanding window, not a single pass. For each season the rate table
// is refitted on every season strictly before it, and head-to-head records
// accumulate match by match. The naive alternative, fitting once over all of
// history and featurising everything with it, leaks the target into the
// features and produces a model that validates beautifully and then loses to
// the population average on data it has not seen.
//
// Deliveries where either player's attributes are unknown are skipped: the
// matchup features would be fabricated, and a row whose most informative
// columns are invented is worse than no row.
func Walk(s *corpus.Store, a attr.Table, newContext func(maxSeason uint16) *Context, fn func(Row)) {
	hand := make([]attr.Hand, len(s.Players))
	class := make([]attr.BowlClass, len(s.Players))
	for i, p := range s.Players {
		hand[i], _ = a.BatOf(p.CricsheetID)
		class[i], _ = a.BowlOf(p.CricsheetID)
	}

	// Head-to-head records as of the start of the match being processed.
	history := map[pairKey]*Matchup{}
	// Records from the match in progress, folded in only once it is finished,
	// so that a delivery is never featurised with its own match's results.
	pending := map[pairKey]*Matchup{}

	x := make([]float32, Dim)
	var ctx *Context
	var ctxSeason uint16
	var currentMatch corpus.MatchID
	first := true

	flush := func() {
		for k, v := range pending {
			h := history[k]
			if h == nil {
				h = &Matchup{}
				history[k] = h
			}
			h.Balls += v.Balls
			h.Runs += v.Runs
			h.Outs += v.Outs
		}
		clear(pending)
	}

	for inn := range s.Inn.Len() {
		if s.Inn.SuperOver[inn] {
			continue
		}
		m := s.Inn.Match[inn]
		season := s.M.Season[m]

		if first || m != currentMatch {
			flush()
			currentMatch, first = m, false
		}
		// Innings are stored in chronological order, so a change of season is
		// the moment to refit on everything that came before it.
		if ctx == nil || season != ctxSeason {
			ctx = newContext(season - 1)
			ctxSeason = season
		}

		venue := s.M.Venue[m]
		target := s.Inn.Target[inn]
		innings := uint8(1)
		if target > 0 {
			innings = 2
		}

		strikerBalls := map[corpus.PlayerID]uint16{}
		var partnershipRuns, partnershipBalls uint16

		for j := s.Inn.Start[inn]; j < s.Inn.End[inn]; j++ {
			i := int(j)
			bat, bowl := s.D.Batter[i], s.D.Bowler[i]
			legal := s.D.Legal[i]
			runs := uint16(s.D.TotalRuns(i))
			wicket := s.D.Wicket[i]

			if bat != corpus.NoPlayer && bowl != corpus.NoPlayer &&
				hand[bat] != attr.HandUnknown && class[bowl] != attr.BowlUnknown {

				var mu Matchup
				if h := history[pairKey{bat, bowl}]; h != nil {
					mu = *h
				}
				ctx.Extract(State{
					Innings:          innings,
					Over:             s.D.Over[i],
					Score:            s.D.ScoreBefore[i],
					Wickets:          s.D.WicketsBefore[i],
					LegalBallsBowled: uint16(s.D.LegalBallsBefore[i]),
					Target:           target,
					StrikerBalls:     strikerBalls[bat],
					PartnershipRuns:  partnershipRuns,
					PartnershipBalls: partnershipBalls,
					Batter:           bat,
					Bowler:           bowl,
					BatterHand:       hand[bat],
					BowlerClass:      class[bowl],
					Venue:            venue,
					Matchup:          mu,
				}, x)

				row := Row{X: make([]float32, Dim), Y: s.D.Classify(i), Season: season}
				copy(row.X, x)
				fn(row)
			}

			// Accumulate into the pending match, never into history, and only
			// after the row for this ball has been emitted.
			if bat != corpus.NoPlayer && bowl != corpus.NoPlayer {
				k := pairKey{bat, bowl}
				pm := pending[k]
				if pm == nil {
					pm = &Matchup{}
					pending[k] = pm
				}
				if legal {
					pm.Balls++
				}
				pm.Runs += uint32(s.D.RunsBat[i])
				if wicket.CreditedToBowler() && s.D.PlayerOut[i] == bat {
					pm.Outs++
				}
			}

			if legal && bat != corpus.NoPlayer {
				strikerBalls[bat]++
			}
			if legal {
				partnershipBalls++
			}
			partnershipRuns += runs
			if wicket.CostsWicket() {
				partnershipRuns, partnershipBalls = 0, 0
			}
		}
	}
	flush()
}
