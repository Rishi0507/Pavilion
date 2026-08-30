// Command pavplay is the game, played at a terminal.
//
// It is deliberately ugly. The point of this stage is to find out whether
// choosing the seventeenth over is interesting, and that question is answered
// by the decision, not by the presentation. If it is not fun here, no amount of
// design later will rescue it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"pavilion/internal/corpus"
	"pavilion/internal/engine"
	"pavilion/internal/puzzle"
	"pavilion/internal/sim"
)

// loadPuzzle prefers the approved queue, which is the only path that has been
// Monte Carloed. An explicit target overrides it, and an unqueued date falls
// back to generating one, both of which exist for development rather than for
// anyone actually playing: a puzzle nobody checked can easily be a day where
// everyone wins.
func loadPuzzle(e *engine.Engine, queuePath, date string, key sim.DailyKey, target uint16) (*sim.Puzzle, error) {
	if target == 0 {
		q, err := puzzle.LoadQueue(queuePath)
		if err == nil {
			if entry, ok := q.For(date); ok {
				p, err := e.FromQueued(date, entry.Target, corpus.VenueID(entry.VenueID),
					entry.AttackIDs, entry.BattingIDs)
				if err != nil {
					return nil, err
				}
				fmt.Printf("(validated puzzle: reference play defends %.0f%%, chases %.0f%%)\n",
					100*entry.Evaluation.DefendRate, 100*entry.Evaluation.ChaseRate)
				return p, nil
			}
		}
		target = 190
		fmt.Fprintf(os.Stderr,
			"pavplay: no queued puzzle for %s, generating an unvalidated one at %d\n", date, target)
	}
	return e.BuildPuzzle(date, key, target)
}

func main() {
	var (
		date   = flag.String("date", "2026-08-28", "puzzle date")
		secret = flag.String("secret", "manhattan-development-secret", "master secret")
		target = flag.Int("target", 0, "target to defend; 0 uses the queued puzzle for the date")
		queue  = flag.String("queue", filepath.Join("data", "out", "puzzles.json"), "approved puzzle queue")
		auto   = flag.Int("auto", 0, "play N automated runs instead of one interactive one")
		policy = flag.String("policy", "best", "automated policy: best, worst, random, saveBest")
		chase  = flag.Bool("chase", false, "measure the chase half instead of the defend half")
	)
	flag.Parse()

	e, err := engine.New(engine.DefaultPaths())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pavplay:", err)
		os.Exit(1)
	}
	key, err := sim.DeriveDailyKey([]byte(*secret), *date)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pavplay:", err)
		os.Exit(1)
	}
	pz, err := loadPuzzle(e, *queue, *date, key, uint16(*target))
	if err != nil {
		fmt.Fprintln(os.Stderr, "pavplay:", err)
		os.Exit(1)
	}
	puzzle := pz

	if *auto > 0 {
		if *chase {
			autoChaseRuns(e, puzzle, key, *auto)
		} else {
			autoRuns(e, puzzle, key, *auto, *policy)
		}
		return
	}
	in := bufio.NewScanner(os.Stdin)

	defend, err := playDefend(e, puzzle, key, in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pavplay:", err)
		os.Exit(1)
	}
	chaseRun, err := playChase(e, puzzle, key, in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pavplay:", err)
		os.Exit(1)
	}
	fmt.Print(shareCard(puzzle, defend, chaseRun))
}

// run is one completed innings, with the win probability trace that the share
// grid is drawn from.
type run struct {
	overs  []sim.Over
	deltas []float64
	result sim.Result
}

// grid renders the share strip: one square per over, coloured by how far that
// over moved the win probability.
func (r run) grid() string {
	var b strings.Builder
	for _, d := range r.deltas {
		b.WriteString(engine.GradeOver(d).Emoji())
	}
	return b.String()
}

// decisionScore is the sum of win probability the player's decisions earned.
// It rewards playing well rather than winning, which is the only honest way to
// compare two people who met different luck.
func (r run) decisionScore() float64 {
	total := 0.0
	for _, d := range r.deltas {
		total += d
	}
	return total
}

func playDefend(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, in *bufio.Scanner) (run, error) {
	s := sim.NewChase(p)
	var r run

	fmt.Printf("\nPar %s\n\n%s\n", p.Date, e.Describe(p))
	fmt.Println("You are defending. Choose a bowler for each over.")
	fmt.Println("Five bowlers, four overs each, twenty overs. Nobody bowls twice in a row.")
	fmt.Println()

	for !s.Done {
		before := e.DefenceProbability(s)
		legal := s.LegalBowlers()

		fmt.Printf("── over %d ──  %d/%d   need %d off %d   defending: %.0f%%\n",
			s.Over+1, s.Score, s.Wickets, s.RunsNeeded(), s.BallsLeft(), 100*before)
		fmt.Printf("   striker %s (%d)\n", p.Batting[s.Striker].Name, s.BallsFaced[s.Striker])
		for _, i := range legal {
			fmt.Printf("   [%d] %-22s %d left\n", i+1, p.Attack[i].Name,
				sim.MaxOversPerBowler-int(s.OversBowled[i]))
		}

		choice := -1
		for choice < 0 {
			fmt.Print("   bowler> ")
			if !in.Scan() {
				return r, nil
			}
			n, err := strconv.Atoi(strings.TrimSpace(in.Text()))
			if err != nil {
				fmt.Println("   a number, please")
				continue
			}
			n--
			ok := false
			for _, i := range legal {
				if i == n {
					ok = true
				}
			}
			if !ok {
				fmt.Println("   not available")
				continue
			}
			choice = n
		}

		intent, err := e.ChooseIntent(s)
		if err != nil {
			return r, err
		}
		over, err := sim.PlayOver(s, key, choice, intent, e)
		if err != nil {
			return r, err
		}
		after := e.DefenceProbability(s)
		delta := after - before

		r.overs = append(r.overs, over)
		r.deltas = append(r.deltas, delta)

		fmt.Printf("   %s to %s [%s]: %s\n", over.Bowler.Name,
			p.Batting[s.Striker].Name, intent, ballLine(over))
		fmt.Printf("   %d run%s, %d wicket%s   %s %+.0f points\n\n",
			over.Runs, plural(int(over.Runs)), over.Wickets, plural(int(over.Wickets)),
			engine.GradeOver(delta).Emoji(), 100*delta)
	}

	r.result = s.Result()
	res := r.result
	if res.Defended {
		fmt.Printf("── defended. They finished %d/%d, %d short. ──\n\n", res.Score, res.Wickets, res.MarginRuns)
	} else {
		fmt.Printf("── chased down, %d wickets in hand. ──\n\n", res.MarginWkts)
	}
	return r, nil
}

func ballLine(o sim.Over) string {
	var parts []string
	for _, d := range o.Deliveries {
		parts = append(parts, d.Outcome.String())
	}
	return strings.Join(parts, " ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// shareCard is the artifact the whole game exists to produce: two rows of
// twenty squares, one per over, coloured by how far that over moved the win
// probability. The same object is the progress bar while you play, the score
// when you finish, and the thing you paste into a group chat.
func shareCard(p *sim.Puzzle, defend, chase run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n── result ──\n\n")
	fmt.Fprintf(&b, "Pavilion %s · target %d\n", p.Date, p.Target)

	fmt.Fprintf(&b, "Defend  %s  ", defend.grid())
	if defend.result.Defended {
		fmt.Fprintf(&b, "won by %d\n", defend.result.MarginRuns)
	} else {
		fmt.Fprintf(&b, "lost by %d wickets\n", defend.result.MarginWkts)
	}

	fmt.Fprintf(&b, "Chase   %s  ", chase.grid())
	if chase.result.TargetMet {
		fmt.Fprintf(&b, "won by %d wickets\n", chase.result.MarginWkts)
	} else {
		fmt.Fprintf(&b, "lost by %d\n", chase.result.MarginRuns)
	}

	// The decision score is the honest comparison between two people who met
	// different luck: it sums the win probability their choices earned, not
	// whether the coin landed for them.
	fmt.Fprintf(&b, "\ndecision score  defend %+.1f   chase %+.1f   total %+.1f\n",
		100*defend.decisionScore(), 100*chase.decisionScore(),
		100*(defend.decisionScore()+chase.decisionScore()))
	return b.String()
}

// Automated play.
//
// The interesting question at this stage is not whether a bot can win but
// whether the choice of bowler matters at all. If bowling the best available
// bowler every over is no better than bowling the worst, the decision is
// decoration and the game does not work.

func autoRuns(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, n int, policyName string) {
	policies := map[string]func(*engine.Engine, *sim.State) int{
		"best":     pickBest,
		"worst":    pickWorst,
		"random":   pickFirst,
		"saveBest": pickSaveBest,
	}
	chosen, ok := policies[policyName]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown policy %q\n", policyName)
		os.Exit(2)
	}

	fmt.Printf("%s, target %d, %d runs\n\n", p.Date, p.Target, n)

	defended, totalScore, totalDelta := 0, 0, 0.0
	for i := range n {
		// Each run uses its own day key so that the sample spans different luck
		// rather than replaying one match.
		k := key
		k[0] ^= byte(i)
		k[1] ^= byte(i >> 8)

		r := playAuto(e, p, k, chosen)
		if r.result.Defended {
			defended++
		}
		totalScore += int(r.result.Score)
		totalDelta += r.decisionScore()

		if i < 3 {
			fmt.Printf("  %s  %d/%d\n", r.grid(), r.result.Score, r.result.Wickets)
		}
	}
	fmt.Printf("\npolicy %-9s defended %5.1f%%   mean score %.1f   mean decision score %+.1f\n",
		policyName, 100*float64(defended)/float64(n), float64(totalScore)/float64(n),
		100*totalDelta/float64(n))
}

func playAuto(e *engine.Engine, p *sim.Puzzle, key sim.DailyKey, choose func(*engine.Engine, *sim.State) int) run {
	s := sim.NewChase(p)
	var r run
	for !s.Done {
		before := e.DefenceProbability(s)
		b := choose(e, s)
		intent, err := e.ChooseIntent(s)
		if err != nil {
			break
		}
		over, err := sim.PlayOver(s, key, b, intent, e)
		if err != nil {
			break
		}
		r.overs = append(r.overs, over)
		r.deltas = append(r.deltas, e.DefenceProbability(s)-before)
	}
	r.result = s.Result()
	return r
}

// expectedRuns estimates what an over from this bowler would cost right now.
func expectedRuns(e *engine.Engine, s *sim.State, bowler int) float64 {
	probs := make([]float64, corpus.NumOutcomes)
	st := e.SituationFor(s, bowler)
	if err := e.Probabilities(st, probs); err != nil {
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
		}
	}
	// A wicket is worth roughly a dozen runs at the death; this is only used to
	// rank bowlers against each other, so the exact figure matters little.
	return 6 * (total - 2.0*probs[corpus.Wicket])
}

func rankBowlers(e *engine.Engine, s *sim.State) []int {
	legal := s.LegalBowlers()
	sort.Slice(legal, func(i, j int) bool {
		return expectedRuns(e, s, legal[i]) < expectedRuns(e, s, legal[j])
	})
	return legal
}

// pickBest bowls whoever is cheapest right now, with no thought for later.
func pickBest(e *engine.Engine, s *sim.State) int { return rankBowlers(e, s)[0] }

// pickWorst is the control: the same game played as badly as the rules allow.
func pickWorst(e *engine.Engine, s *sim.State) int {
	r := rankBowlers(e, s)
	return r[len(r)-1]
}

func pickFirst(_ *engine.Engine, s *sim.State) int { return s.LegalBowlers()[0] }

// pickSaveBest holds the two best death bowlers back entirely until over 16.
//
// This is the tactic the whole game is supposed to be about, so it is worth
// testing directly rather than approximating. Because five bowlers of four
// overs is exactly twenty, every bowler bowls his full allocation whatever
// happens; the only question is which overs each one gets. Reserving the best
// two for the death is the sharpest form of that choice.
func pickSaveBest(e *engine.Engine, s *sim.State) int {
	legal := s.LegalBowlers()
	byDeath := deathRank(e, s)

	reserved := map[int]bool{}
	for i, b := range byDeath {
		if i < 2 {
			reserved[b] = true
		}
	}

	if s.Over < 15 {
		// Prefer anyone not being held back, cheapest first for this phase.
		for _, b := range rankBowlers(e, s) {
			if !reserved[b] {
				return b
			}
		}
	}
	// At the death, or when the reserves are all that is left, take the best.
	for _, b := range byDeath {
		for _, l := range legal {
			if l == b {
				return b
			}
		}
	}
	return legal[0]
}

// deathRank orders the whole attack by how they bowl at the death, regardless
// of the current over, which is the ranking a captain plans around.
func deathRank(e *engine.Engine, s *sim.State) []int {
	death := *s
	death.Over = 18
	death.LegalBalls = 108
	probs := make([]float64, corpus.NumOutcomes)
	_ = probs

	all := make([]int, len(s.Puzzle.Attack))
	for i := range all {
		all[i] = i
	}
	sort.Slice(all, func(i, j int) bool {
		return expectedRuns(e, &death, all[i]) < expectedRuns(e, &death, all[j])
	})
	return all
}
