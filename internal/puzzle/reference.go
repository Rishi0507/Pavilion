package puzzle

import (
	"sort"

	"manhattan/internal/corpus"
	"manhattan/internal/engine"
	"manhattan/internal/sim"
)

// The reference policy.
//
// It is deliberately competent rather than optimal. A puzzle tuned so that
// perfect play wins half the time would be brutal for everyone else; a puzzle
// tuned against a bot that plays badly would be trivial. What the win rates
// need to describe is the experience of someone who knows what they are doing
// and is not solving the game, which is who the daily comparison is for.
//
// This code never runs in production. It exists only to decide whether a
// candidate day is worth putting in front of anyone.

// expectedOverRuns estimates what an over from a bowler would concede now.
func expectedOverRuns(e *engine.Engine, s *sim.State, bowler int, buf []float64) float64 {
	if err := e.Probabilities(e.SituationFor(s, bowler), buf); err != nil {
		return 0
	}
	total := 0.0
	for k := range corpus.NumOutcomes {
		switch corpus.Outcome(k) {
		case corpus.One, corpus.Wide, corpus.NoBall:
			total += buf[k]
		case corpus.Two:
			total += 2 * buf[k]
		case corpus.Three:
			total += 3 * buf[k]
		case corpus.Four:
			total += 4 * buf[k]
		case corpus.Six:
			total += 6 * buf[k]
		case corpus.Wicket:
			// A wicket is worth roughly two overs of pressure at the death.
			// The figure only ranks bowlers against one another.
			total -= 2 * buf[k]
		}
	}
	return 6 * total
}

// deathOrder ranks the whole attack by how it bowls at the death, which is the
// ranking a captain plans the innings around.
func deathOrder(e *engine.Engine, s *sim.State, buf []float64) []int {
	probe := *s
	probe.Over = 18
	probe.LegalBalls = 108

	all := make([]int, len(s.Puzzle.Attack))
	for i := range all {
		all[i] = i
	}
	sort.Slice(all, func(i, j int) bool {
		return expectedOverRuns(e, &probe, all[i], buf) < expectedOverRuns(e, &probe, all[j], buf)
	})
	return all
}

// referenceBowler holds the two best death bowlers back until the last five
// overs, which is the tactic the defend half is built around.
func referenceBowler(e *engine.Engine, s *sim.State, buf []float64) int {
	legal := s.LegalBowlers()
	if len(legal) == 0 {
		return 0
	}
	order := deathOrder(e, s, buf)
	reserved := map[int]bool{order[0]: true}
	if len(order) > 1 {
		reserved[order[1]] = true
	}

	if s.Over < 15 {
		best, bestRuns := -1, 1e9
		for _, b := range legal {
			if reserved[b] {
				continue
			}
			if r := expectedOverRuns(e, s, b, buf); r < bestRuns {
				best, bestRuns = b, r
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

// referenceIntent spends the attacking budget rather than hoarding it, and
// leans on it late.
//
// An earlier version attacked only when the required rate had climbed past 9.5
// and blocked whenever several wickets were down. Measured against two simpler
// policies it was the worst of the three, chasing 40.7% where spending the
// budget in the first six overs managed 46.3% and spending it in the last six
// managed 46.0%. Using it as the reference made every chase look harder than it
// was, which pushed the target search toward scores that only the defending
// half could win, and starved the queue.
//
// The rule now is the one the measurement supports: the budget exists to be
// spent, the last overs are where it is worth most, and blocking is reserved
// for a genuine collapse rather than used as a default.
func referenceIntent(s *sim.State) sim.Intent {
	if s.BallsLeft() == 0 {
		return sim.Rotate
	}
	required := 6 * float64(s.RunsNeeded()) / float64(s.BallsLeft())
	oversLeft := (s.BallsLeft() + 5) / 6

	switch {
	// A collapse: survival is worth more than the rate for one over.
	case s.Wickets >= 8 && required < 12:
		return sim.Block

	// Nothing left to save the budget for.
	case s.AttacksLeft() >= oversLeft && s.AttacksLeft() > 0:
		return sim.Attack

	// The chase is getting away; spend now rather than never.
	case s.AttacksLeft() > 0 && required > 9.0:
		return sim.Attack

	// The death, where an attacking over is worth most.
	case s.AttacksLeft() > 0 && s.Over >= 15:
		return sim.Attack
	}
	return sim.Rotate
}

// simulateDefend plays the half where the player bowls, and measures how much
// the bowling choice was worth along the way.
//
// The spread is the range of win probability across the bowlers legally
// available in an over. It is the number that says whether the decision has any
// weight: a puzzle where every choice leads to the same place is not a puzzle,
// whatever its win rate looks like.
func simulateDefend(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey) (defended bool, spread float64, overs int) {
	s := sim.NewChase(p)
	buf := make([]float64, corpus.NumOutcomes)

	for !s.Done {
		legal := s.LegalBowlers()
		if len(legal) == 0 {
			break
		}
		if len(legal) > 1 {
			lo, hi := 1e9, -1e9
			for _, b := range legal {
				r := expectedOverRuns(e, s, b, buf)
				lo, hi = min(lo, r), max(hi, r)
			}
			// Convert a spread in runs per over into one in win probability,
			// which is the scale the acceptance threshold is expressed on and
			// the one the share grid uses.
			spread += winSwingOf(e, s, hi-lo)
			overs++
		}

		bowler := referenceBowler(e, s, buf)
		intent, err := e.ChooseIntent(s)
		if err != nil {
			break
		}
		if _, err := sim.PlayOver(s, key, bowler, intent, e); err != nil {
			break
		}
	}
	return s.Result().Defended, spread, overs
}

// winSwingOf converts a difference in runs conceded into the win probability it
// would move, at the current state.
func winSwingOf(e *engine.Engine, s *sim.State, runs float64) float64 {
	if runs <= 0 {
		return 0
	}
	half := int(runs/2 + 0.5)
	if half < 1 {
		half = 1
	}
	cheap := *s
	cheap.Score = uint16(max(int(s.Score)-half, 0))
	dear := *s
	dear.Score = s.Score + uint16(half)
	return absf(e.WinProbability(&dear) - e.WinProbability(&cheap))
}

// simulateChase plays the half where the player bats.
func simulateChase(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey) (chased bool, score int) {
	// The two halves must not share luck, or a player would meet the same
	// deliveries twice in one day.
	k := key
	k[27] ^= 0xC5

	s := sim.NewPlayerChase(p)
	buf := make([]float64, corpus.NumOutcomes)
	for !s.Done {
		bowler := referenceBowler(e, s, buf)
		if _, err := sim.PlayOver(s, k, bowler, referenceIntent(s), e); err != nil {
			break
		}
	}
	r := s.Result()
	return r.TargetMet, int(r.Score)
}
