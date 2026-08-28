package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"manhattan/internal/corpus"
	"manhattan/internal/engine"
	"manhattan/internal/sim"
)

// The chase half.
//
// Same target, opposite chair. The player bats and chooses the intent for each
// over from a fixed budget of six attacking overs in twenty; the AI captain
// picks the bowlers. Wickets are the hard constraint, and the win probability
// model enforces it without anything special being written here: losing four
// early makes the remaining attack tokens nearly unusable, which is how real
// chases die.

// chaseCaptain picks bowlers for the AI side.
//
// It is the saving policy, because that is the one a competent captain plays
// and the one that makes the player's attack tokens a real decision: the good
// bowlers will be there at the death.
func chaseCaptain(e *engine.Engine, s *sim.State) int {
	legal := s.LegalBowlers()

	death := *s
	death.Over = 18
	death.LegalBalls = 108
	all := make([]int, len(s.Puzzle.Attack))
	for i := range all {
		all[i] = i
	}
	sort.Slice(all, func(i, j int) bool {
		return expectedRuns(e, &death, all[i]) < expectedRuns(e, &death, all[j])
	})

	reserved := map[int]bool{all[0]: true, all[1]: true}
	if s.Over < 15 {
		for _, b := range rankBowlers(e, s) {
			if !reserved[b] {
				return b
			}
		}
	}
	for _, b := range all {
		for _, l := range legal {
			if l == b {
				return b
			}
		}
	}
	return legal[0]
}

func playChase(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, in *bufio.Scanner) (run, error) {
	// The chase is a different innings from the defence, so it draws on its own
	// coordinate space; the key is offset so the two halves do not share luck.
	chaseKey := key
	chaseKey[27] ^= 0xC5

	s := sim.NewPlayerChase(p)
	var r run

	fmt.Printf("\n── the chase ──\n\nSame target: %d. You are batting now.\n", p.Target)
	fmt.Printf("You have %d attacking overs to spend across twenty.\n\n", sim.MaxAttacks)

	for !s.Done {
		before := e.WinProbability(s)
		bowler := chaseCaptain(e, s)

		fmt.Printf("── over %d ──  %d/%d   need %d off %d   winning: %.0f%%   attacks left: %d\n",
			s.Over+1, s.Score, s.Wickets, s.RunsNeeded(), s.BallsLeft(), 100*before, s.AttacksLeft())
		fmt.Printf("   %s to bowl  ·  striker %s (%d)\n",
			p.Attack[bowler].Name, p.Batting[s.Striker].Name, s.BallsFaced[s.Striker])
		fmt.Print("   [b]lock  [r]otate")
		if s.AttacksLeft() > 0 {
			fmt.Print("  [a]ttack")
		}
		fmt.Println()

		intent := sim.Rotate
		chosen := false
		for !chosen {
			fmt.Print("   intent> ")
			if !in.Scan() {
				return r, nil
			}
			switch strings.ToLower(strings.TrimSpace(in.Text())) {
			case "b", "block":
				intent, chosen = sim.Block, true
			case "r", "rotate", "":
				intent, chosen = sim.Rotate, true
			case "a", "attack":
				if s.AttacksLeft() == 0 {
					fmt.Println("   no attacking overs left")
					continue
				}
				intent, chosen = sim.Attack, true
			default:
				fmt.Println("   b, r or a")
			}
		}

		over, err := sim.PlayOver(s, chaseKey, bowler, intent, e)
		if err != nil {
			return r, err
		}
		after := e.WinProbability(s)
		delta := after - before

		r.overs = append(r.overs, over)
		r.deltas = append(r.deltas, delta)

		fmt.Printf("   [%s] %s\n", intent, ballLine(over))
		fmt.Printf("   %d run%s, %d wicket%s   %s %+.0f points\n\n",
			over.Runs, plural(int(over.Runs)), over.Wickets, plural(int(over.Wickets)),
			engine.GradeOver(delta).Emoji(), 100*delta)
	}

	r.result = s.Result()
	return r, nil
}

// autoChase plays the chase with a policy, for measuring whether the intent
// decision carries any weight.
func autoChase(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, choose func(*sim.State, *engine.Engine) sim.Intent) run {
	chaseKey := key
	chaseKey[27] ^= 0xC5

	s := sim.NewPlayerChase(p)
	var r run
	for !s.Done {
		before := e.WinProbability(s)
		bowler := chaseCaptain(e, s)
		intent := choose(s, e)
		if intent == sim.Attack && s.AttacksLeft() == 0 {
			intent = sim.Rotate
		}
		over, err := sim.PlayOver(s, chaseKey, bowler, intent, e)
		if err != nil {
			break
		}
		r.overs = append(r.overs, over)
		r.deltas = append(r.deltas, e.WinProbability(s)-before)
	}
	r.result = s.Result()
	return r
}

// Chase policies, for the same question the bowling ones answer: does the
// decision matter?

// chaseNever spends nothing, rotating the strike for twenty overs.
func chaseNever(*sim.State, *engine.Engine) sim.Intent { return sim.Rotate }

// chaseEarly burns the budget in the first six overs.
func chaseEarly(s *sim.State, _ *engine.Engine) sim.Intent {
	if s.Over < sim.MaxAttacks {
		return sim.Attack
	}
	return sim.Rotate
}

// chaseLate saves the budget for the last six.
func chaseLate(s *sim.State, _ *engine.Engine) sim.Intent {
	if s.Over >= sim.MaxOvers-sim.MaxAttacks {
		return sim.Attack
	}
	return sim.Rotate
}

// chaseNeeded spends an attack whenever the required rate has climbed out of
// reach of ordinary batting, and blocks when four or more are down early.
func chaseNeeded(s *sim.State, _ *engine.Engine) sim.Intent {
	if s.BallsLeft() == 0 {
		return sim.Rotate
	}
	req := 6 * float64(s.RunsNeeded()) / float64(s.BallsLeft())
	switch {
	case s.Wickets >= 6 && req < 11:
		return sim.Block
	case req > 9.5 && s.AttacksLeft() > 0:
		return sim.Attack
	case req < 6.5 && s.Wickets >= 4:
		return sim.Block
	}
	return sim.Rotate
}

func autoChaseRuns(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, n int) {
	policies := []struct {
		name string
		fn   func(*sim.State, *engine.Engine) sim.Intent
	}{
		{"never", chaseNever},
		{"early", chaseEarly},
		{"late", chaseLate},
		{"needed", chaseNeeded},
	}
	fmt.Printf("chase, target %d, %d runs each\n\n", p.Target, n)
	for _, pol := range policies {
		won, totalScore := 0, 0
		for i := range n {
			k := key
			k[0] ^= byte(i)
			k[1] ^= byte(i >> 8)
			r := autoChase(e, p, k, pol.fn)
			if r.result.TargetMet {
				won++
			}
			totalScore += int(r.result.Score)
		}
		fmt.Printf("  %-8s chased %5.1f%%   mean score %.1f\n",
			pol.name, 100*float64(won)/float64(n), float64(totalScore)/float64(n))
	}
}

func writeGrid(w *os.File, label string, r run) {
	fmt.Fprintf(w, "%-7s %s\n", label, r.grid())
}

var _ = corpus.NumOutcomes
