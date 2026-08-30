// Package engine wires the corpus, the fitted rates, the graph and the two
// models into something that can actually be played.
//
// It is the only place that knows about all of them at once. The simulator
// below it stays a pure function of state and decision; the server above it
// deals in runs and decisions and never touches a model.
package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
	"pavilion/internal/features"
	"pavilion/internal/graph"
	"pavilion/internal/model"
	"pavilion/internal/rates"
	"pavilion/internal/sim"
)

// Engine holds everything loaded at boot. It is read-only once built, so it is
// safe to share across requests without locking.
type Engine struct {
	store   *corpus.Store
	attrs   attr.Table
	ctx     *features.Context
	graph   *graph.Graph
	outcome *model.Outcome
	winprob *model.WinProb

	dealable []corpus.PlayerID
	bowlers  []corpus.PlayerID
	batters  []corpus.PlayerID

	// Career labels, computed once at boot because they need a full scan of the
	// delivery table and never change afterwards.
	teams []string
	years []string

	recentVenues []corpus.VenueID
}

// Paths locates every artifact the engine needs.
type Paths struct {
	Corpus     string
	Attributes string
	Model      string
	ModelMeta  string
	WinProb    string
	WinMeta    string
}

// DefaultPaths returns the layout the make targets produce.
func DefaultPaths() Paths {
	return Paths{
		Corpus:     filepath.Join("data", "out", "corpus.bin"),
		Attributes: filepath.Join("data", "attributes", "players.csv"),
		Model:      filepath.Join("data", "models", "outcome.txt"),
		ModelMeta:  filepath.Join("data", "models", "outcome.json"),
		WinProb:    filepath.Join("data", "models", "winprob.txt"),
		WinMeta:    filepath.Join("data", "models", "winprob.json"),
	}
}

// New loads the engine.
func New(p Paths) (*Engine, error) {
	st, err := corpus.Load(p.Corpus)
	if err != nil {
		return nil, err
	}
	attrs, err := attr.Load(p.Attributes)
	if err != nil {
		return nil, err
	}
	if len(attrs) == 0 {
		return nil, fmt.Errorf("engine: no attribute table at %s; run pavattr", p.Attributes)
	}
	out, err := model.Load(p.Model, p.ModelMeta, features.Names)
	if err != nil {
		return nil, err
	}
	wp, err := model.LoadWinProb(p.WinProb, p.WinMeta)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		store:   st,
		attrs:   attrs,
		outcome: out,
		winprob: wp,
		// Serving uses everything the corpus knows, unlike training, which had
		// to hold seasons back to measure itself honestly.
		ctx:   features.NewContext(st, attrs, 0),
		graph: graph.Build(st, graph.Options{}),
	}

	batKnown := func(id string) bool { _, ok := attrs.BatOf(id); return ok }
	bowlKnown := func(id string) bool { _, ok := attrs.BowlOf(id); return ok }
	e.dealable, e.bowlers, e.batters, _ = st.Dealable(corpus.DefaultEligibility, batKnown, bowlKnown)
	e.labelCareers()
	return e, nil
}

// Store exposes the corpus, which the puzzle builder needs.
func (e *Engine) Store() *corpus.Store { return e.store }

// player converts a corpus player into the engine's view of one.
func (e *Engine) player(id corpus.PlayerID) sim.Player {
	p := e.store.Players[id]
	hand, _ := e.attrs.BatOf(p.CricsheetID)
	class, _ := e.attrs.BowlOf(p.CricsheetID)
	pl := sim.Player{ID: id, Name: p.Name, Hand: hand, Class: class}
	if int(id) < len(e.teams) {
		pl.Team, pl.Years = e.teams[id], e.years[id]
	}
	return pl
}

// labelCareers records, for each player, the side they are best known for and
// the years they played.
//
// "Best known for" is the most recent side by preference and the longest spell
// as a tiebreak, which is how a supporter would answer: Chris Gayle is a
// Bangalore batter even though he finished at Punjab, because that is where the
// innings everyone remembers were played. Where the two disagree by a wide
// margin the longer spell wins.
func (e *Engine) labelCareers() {
	careers := e.store.Careers()
	e.teams = make([]string, len(careers))
	e.years = make([]string, len(careers))

	for id, spells := range careers {
		if len(spells) == 0 {
			continue
		}
		// Fold renames together before choosing, so eight years at Kings XI and
		// two at Punjab Kings count as ten at one club.
		type agg struct {
			innings int
			last    uint16
		}
		byClub := map[string]*agg{}
		first, last := spells[0].FirstSeason, spells[0].LastSeason
		for _, sp := range spells {
			name := CanonicalTeam(e.store.Teams[sp.Team])
			a, ok := byClub[name]
			if !ok {
				a = &agg{}
				byClub[name] = a
			}
			a.innings += sp.Innings
			if sp.LastSeason > a.last {
				a.last = sp.LastSeason
			}
			first = min(first, sp.FirstSeason)
			last = max(last, sp.LastSeason)
		}

		bestName, best := "", &agg{}
		for name, a := range byClub {
			// A spell twice as long as the most recent one is the one people
			// name; otherwise recency wins.
			switch {
			case bestName == "",
				a.innings > 2*best.innings,
				a.last > best.last && 2*a.innings > best.innings:
				bestName, best = name, a
			}
		}
		e.teams[id] = ShortTeam(bestName)

		if first == last {
			e.years[id] = itoa(int(first))
		} else {
			e.years[id] = itoa(int(first)) + "–" + itoa(int(last))
		}
	}
}

// Probabilities implements sim.Predictor.
//
// The head-to-head comes from the graph built at boot, which is the serving
// analogue of the expanding window used in training: history as it stood before
// the game began.
func (e *Engine) Probabilities(s features.State, dst []float64) error {
	s.Matchup = features.MatchupOf(e.graph, s.Batter, s.Bowler)
	x := make([]float32, features.Dim)
	e.ctx.Extract(s, x)
	e.outcome.PredictFloat32(x, dst)
	e.applyQuality(s, dst)
	return nil
}

// WinProbability returns the chasing side's chance from the current state.
func (e *Engine) WinProbability(s *sim.State) float64 {
	return e.winprob.Predict(model.Situation{
		RunsRequired:   s.RunsNeeded(),
		BallsRemaining: s.BallsLeft(),
		WicketsInHand:  sim.Wickets - int(s.Wickets),
		Target:         int(s.Puzzle.Target),
		VenueRunRate:   float64(e.ctx.VenueRunRate(s.Puzzle.Venue)),
	})
}

// DefenceProbability is the same number from the bowling side's point of view,
// which is what the defend half is scored on.
func (e *Engine) DefenceProbability(s *sim.State) float64 {
	return 1 - e.WinProbability(s)
}

// ChooseIntent picks the batting side's approach for the next over by one-ply
// expectimax over the win probability model.
//
// For each intent it works out what the over is expected to cost in wickets and
// return in runs, walks the state forward by that much, and asks the model what
// the chase would then be worth. The batting side takes whichever intent leaves
// it best placed. This is deliberately shallow: a deeper search would model an
// opponent who is not there, since the human has already chosen the bowler.
func (e *Engine) ChooseIntent(s *sim.State) (sim.Intent, error) {
	best, bestWP := sim.Rotate, -1.0

	base := make([]float64, corpus.NumOutcomes)
	tilted := make([]float64, corpus.NumOutcomes)

	bat := s.Puzzle.Batting[s.Striker]
	bowl := s.Puzzle.Attack[max(s.LastBowler, 0)]
	st := features.State{
		Innings:          2,
		Over:             s.Over,
		Score:            s.Score,
		Wickets:          s.Wickets,
		LegalBallsBowled: s.LegalBalls,
		Target:           s.Puzzle.Target,
		StrikerBalls:     s.BallsFaced[s.Striker],
		PartnershipRuns:  s.PartnershipRuns,
		PartnershipBalls: s.PartnershipBalls,
		Batter:           bat.ID,
		Bowler:           bowl.ID,
		BatterHand:       bat.Hand,
		BowlerClass:      bowl.Class,
		Venue:            s.Puzzle.Venue,
	}
	if err := e.Probabilities(st, base); err != nil {
		return sim.Rotate, err
	}

	for _, intent := range []sim.Intent{sim.Block, sim.Rotate, sim.Attack} {
		// The budget binds whoever is batting, so an intent that cannot be
		// played is not an option to be compared. Without this the AI side
		// would keep choosing an over the rules would then refuse.
		if intent == sim.Attack && s.LimitAttacks && s.AttacksLeft() <= 0 {
			continue
		}
		sim.TiltFor(base, intent, s.RequiredRate(), tilted)

		expRuns, pWicket := 0.0, tilted[corpus.Wicket]
		for k := range corpus.NumOutcomes {
			expRuns += tilted[k] * runValue(corpus.Outcome(k))
		}

		// Six balls of this intent, in expectation.
		runs := expRuns * 6
		wkts := pWicket * 6

		needed := float64(s.RunsNeeded()) - runs
		balls := float64(s.BallsLeft() - 6)
		hand := float64(sim.Wickets-int(s.Wickets)) - wkts

		if balls < 0 {
			balls = 0
		}
		if hand < 0 {
			hand = 0
		}

		wp := e.winprob.Predict(model.Situation{
			RunsRequired:   int(needed + 0.5),
			BallsRemaining: int(balls),
			WicketsInHand:  int(hand + 0.5),
			Target:         int(s.Puzzle.Target),
			VenueRunRate:   float64(e.ctx.VenueRunRate(s.Puzzle.Venue)),
		})
		if wp > bestWP {
			best, bestWP = intent, wp
		}
	}
	return best, nil
}

func runValue(o corpus.Outcome) float64 {
	switch o {
	case corpus.One, corpus.Wide, corpus.NoBall:
		return 1
	case corpus.Two:
		return 2
	case corpus.Three:
		return 3
	case corpus.Four:
		return 4
	case corpus.Six:
		return 6
	}
	return 0
}

// Grade is how well one over went, on the scale the share grid uses.
type Grade uint8

const (
	Bad Grade = iota
	Neutral
	Good
)

func (g Grade) Emoji() string {
	switch g {
	case Good:
		return "\U0001F7E9" // green
	case Bad:
		return "\U0001F7E5" // red
	}
	return "\U0001F7E8" // amber
}

// GradeOver turns a win probability change into a square of the grid.
//
// The thresholds are in probability points, and they are what make the strip
// mean something: an over that moved the game two points either way was not
// really a decision, and colouring it green would tell the player they did
// something when they did not.
func GradeOver(delta float64) Grade {
	switch {
	case delta > 0.03:
		return Good
	case delta < -0.03:
		return Bad
	}
	return Neutral
}

// pickTop returns the n highest-volume players from a pool, most recent first,
// which is how a puzzle gets names a player will recognise.
func (e *Engine) pickTop(pool []corpus.PlayerID, sinceSeason uint16, n int, by func(corpus.Volume) int) []corpus.PlayerID {
	vols := e.store.Volumes()
	cand := make([]corpus.PlayerID, 0, len(pool))
	for _, p := range pool {
		if vols[p].LastSeason >= sinceSeason && by(vols[p]) > 0 {
			cand = append(cand, p)
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		a, b := by(vols[cand[i]]), by(vols[cand[j]])
		if a != b {
			return a > b
		}
		return cand[i] < cand[j]
	})
	if len(cand) > n {
		cand = cand[:n]
	}
	return cand
}

// SituationFor builds the feature state for the next ball if a given bowler
// were to bowl it. It lets a caller compare bowlers without playing an over.
func (e *Engine) SituationFor(s *sim.State, bowler int) features.State {
	bat := s.Puzzle.Batting[s.Striker]
	bowl := s.Puzzle.Attack[bowler]
	return features.State{
		Innings:          2,
		Over:             s.Over,
		Score:            s.Score,
		Wickets:          s.Wickets,
		LegalBallsBowled: s.LegalBalls,
		Target:           s.Puzzle.Target,
		StrikerBalls:     s.BallsFaced[s.Striker],
		PartnershipRuns:  s.PartnershipRuns,
		PartnershipBalls: s.PartnershipBalls,
		Batter:           bat.ID,
		Bowler:           bowl.ID,
		BatterHand:       bat.Hand,
		BowlerClass:      bowl.Class,
		Venue:            s.Puzzle.Venue,
	}
}

// Rates exposes the fitted rate table for inspection tooling.
func (e *Engine) Rates() *rates.Table { return e.ctx.Rates() }

// Name returns the grade as a stable identifier for templates and JSON.
func (g Grade) Name() string {
	switch g {
	case Good:
		return "good"
	case Bad:
		return "bad"
	}
	return "level"
}

// VenueName returns the ground's name.
func (e *Engine) VenueName(v corpus.VenueID) string {
	if int(v) < len(e.store.Venues) {
		return e.store.Venues[v]
	}
	return "unknown ground"
}

// ClassName renders a bowling class for display.
func ClassName(c attr.BowlClass) string { return className(c) }

// HandName renders a batting hand for display.
func HandName(h attr.Hand) string {
	switch h {
	case attr.LeftHandBat:
		return "left-hand bat"
	case attr.RightHandBat:
		return "right-hand bat"
	}
	return ""
}

// CaptainPick chooses the bowler for the AI side during the chase half.
//
// It plays the same tactic the reference policy does: hold the two best death
// bowlers back until the last five overs. That is what makes the player's
// attacking budget a real decision, because the good bowlers will still be
// there at the end.
func (e *Engine) CaptainPick(s *sim.State) int {
	legal := s.LegalBowlers()
	if len(legal) == 0 {
		return 0
	}
	probs := make([]float64, corpus.NumOutcomes)
	cost := func(st *sim.State, b int) float64 {
		if err := e.Probabilities(e.SituationFor(st, b), probs); err != nil {
			return 0
		}
		total := 0.0
		for k := range corpus.NumOutcomes {
			switch corpus.Outcome(k) {
			case corpus.One, corpus.Wide, corpus.NoBall:
				total += probs[k]
			case corpus.Two:
				total += 2 * probs[k]
			case corpus.Three:
				total += 3 * probs[k]
			case corpus.Four:
				total += 4 * probs[k]
			case corpus.Six:
				total += 6 * probs[k]
			case corpus.Wicket:
				total -= 2 * probs[k]
			}
		}
		return total
	}

	death := *s
	death.Over = 18
	death.LegalBalls = 108
	order := make([]int, len(s.Puzzle.Attack))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return cost(&death, order[i]) < cost(&death, order[j]) })

	reserved := map[int]bool{order[0]: true}
	if len(order) > 1 {
		reserved[order[1]] = true
	}
	if s.Over < 15 {
		best, bestCost := -1, 1e9
		for _, b := range legal {
			if reserved[b] {
				continue
			}
			if c := cost(s, b); c < bestCost {
				best, bestCost = b, c
			}
		}
		if best >= 0 {
			return best
		}
	}
	for _, b := range order {
		for _, l := range legal {
			if l == b {
				return b
			}
		}
	}
	return legal[0]
}
