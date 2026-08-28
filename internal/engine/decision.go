package engine

import (
	"manhattan/internal/corpus"
	"manhattan/internal/model"
	"manhattan/internal/sim"
)

// Scoring the decision rather than the result.
//
// The obvious metric, summing how much the win probability moved over each
// over, does not work, and it fails in a way that is easy to miss: the sum
// telescopes. Every intermediate value cancels, leaving nothing but the final
// probability minus the starting one, which is determined entirely by whether
// the player won. Dressed up as a decision score it would say precisely what
// the scoreboard already says, and a player who won on luck after three bad
// calls would outrank one who lost narrowly having played it perfectly.
//
// What is wanted is the value of the choice, measured before the dice are
// thrown: how much better was the option taken than the options available. That
// is computable, because the engine can estimate what each choice is worth
// without playing it, and it is luck-free by construction, since every option is
// evaluated against the same situation.

// DecisionValue returns how much better the chosen option was than the average
// option available, in win probability, from the deciding side's point of view.
//
// Zero means an ordinary choice. Positive means the player found something
// better than the alternatives. It is bounded by how much the choice could
// possibly matter, so a day where every bowler is equivalent cannot produce a
// large score in either direction, which is correct: there was no decision to
// get right.
func (e *Engine) DecisionValue(s *sim.State, defending bool, chosenBowler int, chosenIntent sim.Intent) float64 {
	if defending {
		return e.bowlerDecisionValue(s, chosenBowler)
	}
	return e.intentDecisionValue(s, chosenIntent)
}

// projected estimates the win probability after an over that concedes the given
// expected runs and wickets.
func (e *Engine) projected(s *sim.State, runs, wickets float64) float64 {
	needed := float64(s.RunsNeeded()) - runs
	balls := max(float64(s.BallsLeft()-6), 0)
	hand := max(float64(sim.Wickets-int(s.Wickets))-wickets, 0)

	return e.winprob.Predict(model.Situation{
		RunsRequired:   int(needed + 0.5),
		BallsRemaining: int(balls),
		WicketsInHand:  int(hand + 0.5),
		Target:         int(s.Puzzle.Target),
		VenueRunRate:   float64(e.ctx.VenueRunRate(s.Puzzle.Venue)),
	})
}

// overCost estimates the runs and wickets an over would produce.
func (e *Engine) overCost(s *sim.State, bowler int, intent sim.Intent, buf, tilted []float64) (runs, wickets float64) {
	if err := e.Probabilities(e.SituationFor(s, bowler), buf); err != nil {
		return 0, 0
	}
	sim.TiltFor(buf, intent, tilted)
	for k := range corpus.NumOutcomes {
		runs += tilted[k] * runValue(corpus.Outcome(k))
	}
	return runs * 6, tilted[corpus.Wicket] * 6
}

// bowlerDecisionValue scores the defending side's choice of bowler.
func (e *Engine) bowlerDecisionValue(s *sim.State, chosen int) float64 {
	legal := s.LegalBowlers()
	if len(legal) < 2 {
		// No choice was available, so no credit and no blame.
		return 0
	}
	intent, err := e.ChooseIntent(s)
	if err != nil {
		return 0
	}

	buf := make([]float64, corpus.NumOutcomes)
	tilted := make([]float64, corpus.NumOutcomes)

	var total, chosenValue float64
	found := false
	for _, b := range legal {
		runs, wkts := e.overCost(s, b, intent, buf, tilted)
		// The defending side wants the chaser's probability low.
		v := 1 - e.projected(s, runs, wkts)
		total += v
		if b == chosen {
			chosenValue, found = v, true
		}
	}
	if !found {
		return 0
	}
	return chosenValue - total/float64(len(legal))
}

// intentDecisionValue scores the batting side's choice of intent.
func (e *Engine) intentDecisionValue(s *sim.State, chosen sim.Intent) float64 {
	options := []sim.Intent{sim.Block, sim.Rotate}
	if s.AttacksLeft() > 0 {
		options = append(options, sim.Attack)
	}
	if len(options) < 2 {
		return 0
	}

	bowler := e.CaptainPick(s)
	buf := make([]float64, corpus.NumOutcomes)
	tilted := make([]float64, corpus.NumOutcomes)

	var total, chosenValue float64
	found := false
	for _, in := range options {
		runs, wkts := e.overCost(s, bowler, in, buf, tilted)
		v := e.projected(s, runs, wkts)
		total += v
		if in == chosen {
			chosenValue, found = v, true
		}
	}
	if !found {
		return 0
	}
	return chosenValue - total/float64(len(options))
}
