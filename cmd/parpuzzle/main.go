// Command parpuzzle generates and validates the daily puzzle queue.
//
// It is a batch job. It searches over targets and attack compositions, Monte
// Carlos each candidate against a reference policy, and writes only the ones
// that survive. Nothing here runs at request time: a puzzle generated on demand
// could not be checked, and an unchecked puzzle is how a day arrives where
// everyone defends comfortably and the comparison means nothing.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"manhattan/internal/engine"
	"manhattan/internal/puzzle"
	"manhattan/internal/sim"
)

func main() {
	var (
		out        = flag.String("out", filepath.Join("data", "out", "puzzles.json"), "puzzle queue")
		secret     = flag.String("secret", "manhattan-development-secret", "master secret")
		from       = flag.String("from", "", "first date to fill, YYYY-MM-DD (default tomorrow)")
		days       = flag.Int("days", 7, "how many days to fill")
		candidates = flag.Int("candidates", 12, "candidates to try per day before giving up")
		games      = flag.Int("games", 1200, "simulated games per candidate while screening")
		confirm    = flag.Int("confirm", 4000, "simulated games used to confirm the chosen candidate")
		sweep      = flag.Bool("sweep", false, "evaluate a range of targets and print the results instead of queueing")
		verbose    = flag.Bool("v", false, "log every candidate")
		pool       = flag.Int("pool", 0, "generate this many dateless situations for practice mode instead of daily puzzles")
		poolOut    = flag.String("pool-out", filepath.Join("data", "out", "pool.json"), "practice situation pool")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose || *sweep {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if *sweep {
		if err := runSweep(log, *secret, *games); err != nil {
			log.Error("sweep failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if *pool > 0 {
		if err := runPool(log, *secret, *poolOut, *pool, *games); err != nil {
			log.Error("pool generation failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(log, *out, *secret, *from, *days, *candidates, *games, *confirm); err != nil {
		log.Error("puzzle generation failed", "err", err)
		os.Exit(1)
	}
}

// runSweep prints how a range of targets behaves against one dealt attack, and
// how the same target behaves against different attacks. It exists because a
// search that rejects everything is useless without a way to see what it was
// rejecting.
func runSweep(log *slog.Logger, secret string, games int) error {
	e, err := engine.New(engine.DefaultPaths())
	if err != nil {
		return err
	}
	crit := puzzle.DefaultCriteria
	crit.Games = games

	fmt.Printf("%-8s %-7s %-7s %-9s %-9s %s\n",
		"attack", "target", "defend", "chase", "spread", "verdict")

	for a := range 4 {
		key, err := sim.DeriveDailyKey([]byte(secret), fmt.Sprintf("sweep-%d", a))
		if err != nil {
			return err
		}
		for _, target := range []uint16{150, 165, 180, 195, 210, 225} {
			p, err := e.BuildPuzzle("sweep", key, target)
			if err != nil {
				return err
			}
			c := puzzle.Candidate{Target: target, Attack: p.Attack, Batting: p.Batting, Venue: p.Venue}
			ev := puzzle.Evaluate(e, c, key, crit)
			verdict := "ok"
			if !ev.Accepted {
				verdict = strings.Join(ev.Rejections, "; ")
			}
			fmt.Printf("%-8d %-7d %-7.3f %-9.3f %-9.4f %s\n",
				a, target, ev.DefendRate, ev.ChaseRate, ev.DecisionSpread, verdict)
		}
	}
	return nil
}

// runPool fills the practice pool with validated situations that are not tied
// to a date.
//
// It appends rather than replacing, so the pool can be grown a few situations
// at a time instead of demanding one long run, and so a stopped run keeps
// whatever it managed.
func runPool(log *slog.Logger, secret, out string, want, games int) error {
	e, err := engine.New(engine.DefaultPaths())
	if err != nil {
		return err
	}
	crit := puzzle.DefaultCriteria
	crit.Games = games

	p := &puzzle.Pool{Criteria: crit}
	if existing, err := puzzle.LoadPool(out); err == nil {
		p.Situations = existing.Situations
		log.Info("existing pool loaded", "situations", len(p.Situations))
	}

	added, tried := 0, 0
	for added < want {
		tried++
		id := fmt.Sprintf("p%d-%d", time.Now().UnixNano(), tried)
		key, err := sim.DeriveDailyKey([]byte(secret), id)
		if err != nil {
			return err
		}

		built, err := e.BuildPuzzle(id, key, 180)
		if err != nil {
			return err
		}
		c := puzzle.Candidate{Attack: built.Attack, Batting: built.Batting, Venue: built.Venue}
		c.Target = puzzle.SolveTarget(e, c, key, max(games/6, 120))

		ev := puzzle.Evaluate(e, c, key, crit)
		if !ev.Accepted {
			log.Debug("rejected", "target", c.Target,
				"defend", fmt.Sprintf("%.2f", ev.DefendRate),
				"chase", fmt.Sprintf("%.2f", ev.ChaseRate),
				"spread", fmt.Sprintf("%.4f", ev.DecisionSpread))
			continue
		}

		p.Situations = append(p.Situations, queued(e, id, c, ev))
		added++
		log.Info("situation added",
			"n", len(p.Situations), "target", c.Target,
			"venue", e.Store().Venues[c.Venue],
			"defend", fmt.Sprintf("%.0f%%", 100*ev.DefendRate),
			"chase", fmt.Sprintf("%.0f%%", 100*ev.ChaseRate))

		// Written after every acceptance, so stopping the run early still
		// leaves a usable pool.
		if err := p.Save(out); err != nil {
			return err
		}
	}
	log.Info("pool written", "path", out, "situations", len(p.Situations), "tried", tried)
	return nil
}

func run(log *slog.Logger, out, secret, from string, days, candidates, games, confirm int) error {
	e, err := engine.New(engine.DefaultPaths())
	if err != nil {
		return err
	}

	start := time.Now().UTC().AddDate(0, 0, 1)
	if from != "" {
		start, err = time.Parse("2006-01-02", from)
		if err != nil {
			return fmt.Errorf("bad -from: %w", err)
		}
	}

	crit := puzzle.DefaultCriteria
	crit.Games = games

	q := &puzzle.Queue{Criteria: crit}
	if existing, err := puzzle.LoadQueue(out); err == nil {
		q.Puzzles = existing.Puzzles
	}

	for d := range days {
		date := start.AddDate(0, 0, d).Format("2006-01-02")
		if _, exists := q.For(date); exists {
			log.Info("already queued", "date", date)
			continue
		}

		key, err := sim.DeriveDailyKey([]byte(secret), date)
		if err != nil {
			return err
		}

		started := time.Now()
		chosen, ev, tried, err := search(log, e, date, key, crit, candidates)
		if err != nil {
			log.Warn("no acceptable puzzle found", "date", date, "tried", tried, "err", err)
			continue
		}

		// Confirm the winner on a larger sample. Screening on a small one is
		// cheap and lets the search be wide; shipping on it would mean the
		// published win rates carry more sampling error than the band is wide.
		confirmCrit := crit
		confirmCrit.Games = confirm
		final := puzzle.Evaluate(e, chosen, key, confirmCrit)
		if !final.Accepted {
			log.Warn("candidate failed confirmation, dropping the day",
				"date", date, "screening", fmt.Sprintf("%.3f/%.3f", ev.DefendRate, ev.ChaseRate),
				"confirmed", fmt.Sprintf("%.3f/%.3f", final.DefendRate, final.ChaseRate),
				"why", final.Rejections)
			continue
		}

		q.Puzzles = append(q.Puzzles, queued(e, date, chosen, final))
		log.Info("puzzle queued",
			"date", date,
			"target", chosen.Target,
			"defend", fmt.Sprintf("%.1f%%", 100*final.DefendRate),
			"chase", fmt.Sprintf("%.1f%%", 100*final.ChaseRate),
			"decision_spread", fmt.Sprintf("%.4f", final.DecisionSpread),
			"tried", tried,
			"took", time.Since(started).Round(time.Second))
	}

	if err := q.Save(out); err != nil {
		return err
	}
	log.Info("queue written", "path", out, "puzzles", len(q.Puzzles))
	return nil
}

// search tries candidate days until enough acceptable ones exist to choose from.
func search(log *slog.Logger, e *engine.Engine, date string, key sim.DailyKey,
	crit puzzle.Criteria, budget int) (puzzle.Candidate, puzzle.Evaluation, int, error) {

	type scored struct {
		c  puzzle.Candidate
		ev puzzle.Evaluation
	}
	var accepted []scored
	tried := 0

	for i := range budget {
		// Each attempt varies the key, which changes both the attack dealt and
		// the target, so the search covers the tuple rather than sweeping a
		// score against one fixed attack.
		k := key
		k[8] ^= byte(i)
		k[9] ^= byte(i >> 8)

		// The attack, the chasing side and the ground come from the key; the
		// target is then solved for rather than guessed, because for a fixed
		// tuple the defend rate is monotone in the score and the acceptable
		// band is narrow.
		p, err := e.BuildPuzzle(date, k, 180)
		if err != nil {
			return puzzle.Candidate{}, puzzle.Evaluation{}, tried, err
		}
		c := puzzle.Candidate{Attack: p.Attack, Batting: p.Batting, Venue: p.Venue}
		c.Target = puzzle.SolveTarget(e, c, k, max(crit.Games/6, 120))

		ev := puzzle.Evaluate(e, c, k, crit)
		tried++
		log.Debug("candidate",
			"target", c.Target,
			"defend", fmt.Sprintf("%.3f", ev.DefendRate),
			"chase", fmt.Sprintf("%.3f", ev.ChaseRate),
			"spread", fmt.Sprintf("%.4f", ev.DecisionSpread),
			"accepted", ev.Accepted)

		if ev.Accepted {
			accepted = append(accepted, scored{c, ev})
			if len(accepted) >= 3 {
				break
			}
		}
	}

	if len(accepted) == 0 {
		return puzzle.Candidate{}, puzzle.Evaluation{}, tried,
			fmt.Errorf("no candidate passed in %d attempts", tried)
	}
	sort.Slice(accepted, func(i, j int) bool {
		return accepted[i].ev.Score() > accepted[j].ev.Score()
	})
	return accepted[0].c, accepted[0].ev, tried, nil
}

func queued(e *engine.Engine, date string, c puzzle.Candidate, ev puzzle.Evaluation) puzzle.Queued {
	q := puzzle.Queued{
		Date:       date,
		Target:     c.Target,
		Venue:      e.Store().Venues[c.Venue],
		VenueID:    uint8(c.Venue),
		Evaluation: ev,
		Generated:  time.Now().UTC().Format(time.RFC3339),
	}
	for _, b := range c.Attack {
		q.Attack = append(q.Attack, b.Name)
		q.AttackIDs = append(q.AttackIDs, uint16(b.ID))
	}
	for _, b := range c.Batting {
		q.Batting = append(q.Batting, b.Name)
		q.BattingIDs = append(q.BattingIDs, uint16(b.ID))
	}
	return q
}
