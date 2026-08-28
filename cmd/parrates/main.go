// Command parrates fits the hierarchical rate table and prints the leaderboards
// that tell you whether the shrinkage is doing its job.
//
// The check the brief asks for is simple and unforgiving: sort bowlers by
// shrunk death-overs economy. If the top five are players nobody has heard of,
// the shrinkage is too weak and the game will feel random.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"manhattan/internal/attr"
	"manhattan/internal/corpus"
	"manhattan/internal/rates"
)

func main() {
	var (
		corpusPath = flag.String("corpus", filepath.Join("data", "out", "corpus.bin"), "corpus binary")
		attrPath   = flag.String("attributes", filepath.Join("data", "attributes", "players.csv"), "player attribute table")
		outPath    = flag.String("out", filepath.Join("data", "out", "rates.bin"), "fitted rate table")
		reportPath = flag.String("report", filepath.Join("data", "out", "rates.md"), "human-readable leaderboards")
		minBalls   = flag.Int("min-balls", 200, "minimum deliveries in the phase to appear on a leaderboard")
		top        = flag.Int("top", 15, "leaderboard length")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *corpusPath, *attrPath, *outPath, *reportPath, *minBalls, *top); err != nil {
		log.Error("rate fitting failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, corpusPath, attrPath, outPath, reportPath string, minBalls, top int) error {
	st, err := corpus.Load(corpusPath)
	if err != nil {
		return err
	}
	attrs, err := attr.Load(attrPath)
	if err != nil {
		return err
	}
	if len(attrs) == 0 {
		return fmt.Errorf("no attribute table at %s; run parattr first", attrPath)
	}

	started := time.Now()
	t := rates.Build(st, attrs)
	log.Info("rate table fitted",
		"players", len(t.Players), "cells", rates.NumCells,
		"took", time.Since(started).Round(time.Millisecond))

	for c := range rates.NumCells {
		s := t.Cells[c]
		log.Info("cell fitted",
			"cell", rates.CellName(c),
			"deliveries", s.Deliveries,
			"players", s.Players,
			"kappa", fmt.Sprintf("%.0f", s.Kappa),
			"half_weight_at", fmt.Sprintf("%.0f balls", s.Kappa))
	}

	if err := rates.Save(t, outPath); err != nil {
		return err
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("stat rate table: %w", err)
	}
	log.Info("rate table written", "path", outPath, "bytes", info.Size())

	// Only players the game can actually deal belong on a leaderboard.
	batKnown := func(id string) bool { _, ok := attrs.BatOf(id); return ok }
	bowlKnown := func(id string) bool { _, ok := attrs.BowlOf(id); return ok }
	_, bowlers, batters, _ := st.Dealable(corpus.DefaultEligibility, batKnown, bowlKnown)

	rep := newReport(st, t, minBalls, top)
	rep.bowling("Death overs", corpus.PhaseDeath, bowlers)
	rep.bowling("Powerplay", corpus.PhasePowerplay, bowlers)
	rep.bowling("Middle overs", corpus.PhaseMiddle, bowlers)
	rep.batting("Death overs", corpus.PhaseDeath, batters)
	rep.batting("Powerplay", corpus.PhasePowerplay, batters)
	rep.shrinkageEffect(bowlers)

	if err := os.WriteFile(reportPath, []byte(rep.String()), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	log.Info("leaderboards written", "path", reportPath)

	// Print the one leaderboard that decides whether this model is any good.
	fmt.Print(rep.sanity())
	return nil
}

type row struct {
	id corpus.PlayerID
	r  rates.PhaseRate
}

type report struct {
	st       *corpus.Store
	t        *rates.Table
	minBalls int
	top      int
	b        []byte
	sanityB  []byte
}

func newReport(st *corpus.Store, t *rates.Table, minBalls, top int) *report {
	r := &report{st: st, t: t, minBalls: minBalls, top: top}
	r.p("# Manhattan shrunk player rates\n")
	r.p("Generated %s.\n", time.Now().UTC().Format(time.RFC3339))
	r.p("Every rate below is a posterior mean under a Dirichlet-multinomial whose")
	r.p("prior concentration was fitted per cell by marginal likelihood. The")
	r.p("`weight` column is the share of the estimate that comes from the player's")
	r.p("own record rather than from the population, so a low weight means the")
	r.p("number is mostly an assumption.\n")
	r.p("## Fitted concentrations\n")
	r.p("`kappa` is in units of deliveries: a player with exactly kappa balls in a")
	r.p("cell sits halfway between their own record and the population mean.\n")
	r.p("| Cell | Deliveries | Players | kappa |")
	r.p("|---|---:|---:|---:|")
	for c := range rates.NumCells {
		s := t.Cells[c]
		r.p("| %s | %d | %d | %.0f |", rates.CellName(c), s.Deliveries, s.Players, s.Kappa)
	}
	r.p("")
	return r
}

func (r *report) p(format string, a ...any) {
	r.b = append(r.b, fmt.Sprintf(format+"\n", a...)...)
}

func (r *report) String() string { return string(r.b) }
func (r *report) sanity() string { return string(r.sanityB) }

func (r *report) collect(players []corpus.PlayerID, role rates.Role, phase corpus.Phase) []row {
	var out []row
	for _, p := range players {
		pr := r.t.Phase(int(p), role, phase)
		if pr.Deliveries < r.minBalls {
			continue
		}
		out = append(out, row{p, pr})
	}
	return out
}

func (r *report) bowling(title string, phase corpus.Phase, bowlers []corpus.PlayerID) {
	rows := r.collect(bowlers, rates.Bowling, phase)
	sort.Slice(rows, func(i, j int) bool { return rows[i].r.Economy < rows[j].r.Economy })

	r.p("## %s: best bowling economy (shrunk)\n", title)
	r.p("Minimum %d deliveries in the phase.\n", r.minBalls)
	r.p("| # | Bowler | Balls | Shrunk econ | Raw econ | Balls/wkt | Weight |")
	r.p("|---:|---|---:|---:|---:|---:|---:|")
	for i, x := range rows {
		if i >= r.top {
			break
		}
		r.p("| %d | %s | %d | **%.2f** | %.2f | %.1f | %.2f |",
			i+1, r.st.PlayerName(x.id), x.r.Deliveries, x.r.Economy, x.r.RawEconomy, x.r.BallsPerWkt, x.r.Weight)
	}
	r.p("")

	if phase == corpus.PhaseDeath {
		r.sanityB = append(r.sanityB, r.deathTable(rows)...)
	}
}

// deathTable renders the check the brief calls for, to stdout as well as to the
// report, because it is the one result worth looking at every time.
func (r *report) deathTable(rows []row) []byte {
	var b []byte
	w := tabwriter.NewWriter(&sliceWriter{&b}, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\nDeath-overs economy, shrunk (minimum %d balls)\n\n", r.minBalls)
	fmt.Fprintln(w, "#\tbowler\tballs\tshrunk\traw\tballs/wkt\tweight")
	for i, x := range rows {
		if i >= r.top {
			break
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%.2f\t%.2f\t%.1f\t%.2f\n",
			i+1, r.st.PlayerName(x.id), x.r.Deliveries, x.r.Economy, x.r.RawEconomy, x.r.BallsPerWkt, x.r.Weight)
	}
	w.Flush()
	return b
}

type sliceWriter struct{ b *[]byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	*s.b = append(*s.b, p...)
	return len(p), nil
}

func (r *report) batting(title string, phase corpus.Phase, batters []corpus.PlayerID) {
	rows := r.collect(batters, rates.Batting, phase)
	sort.Slice(rows, func(i, j int) bool { return rows[i].r.StrikeRate > rows[j].r.StrikeRate })

	r.p("## %s: best batting strike rate (shrunk)\n", title)
	r.p("Minimum %d deliveries in the phase.\n", r.minBalls)
	r.p("| # | Batter | Balls | Shrunk SR | Raw SR | Balls/out | Weight |")
	r.p("|---:|---|---:|---:|---:|---:|---:|")
	for i, x := range rows {
		if i >= r.top {
			break
		}
		r.p("| %d | %s | %d | **%.1f** | %.1f | %.1f | %.2f |",
			i+1, r.st.PlayerName(x.id), x.r.Deliveries, x.r.StrikeRate, x.r.RawStrike, x.r.BallsPerWkt, x.r.Weight)
	}
	r.p("")
}

// shrinkageEffect shows the model earning its keep: the players whose raw
// record most overstates them, which are exactly the players a naive rate table
// would have promoted to the top of the leaderboard.
func (r *report) shrinkageEffect(bowlers []corpus.PlayerID) {
	type moved struct {
		id    corpus.PlayerID
		r     rates.PhaseRate
		delta float64
	}
	var all []moved
	for _, p := range bowlers {
		pr := r.t.Phase(int(p), rates.Bowling, corpus.PhaseDeath)
		if pr.Deliveries < 30 || pr.RawEconomy == 0 {
			continue
		}
		all = append(all, moved{p, pr, pr.Economy - pr.RawEconomy})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].delta > all[j].delta })

	r.p("## Where the shrinkage bites\n")
	r.p("Death-overs bowlers whose raw record most overstates them, minimum 30")
	r.p("balls. These are the players a naive rate table would have put at the top")
	r.p("of the leaderboard on the strength of a handful of good overs.\n")
	r.p("| Bowler | Balls | Raw econ | Shrunk econ | Correction | Weight |")
	r.p("|---|---:|---:|---:|---:|---:|")
	for i, m := range all {
		if i >= r.top {
			break
		}
		r.p("| %s | %d | %.2f | %.2f | +%.2f | %.2f |",
			r.st.PlayerName(m.id), m.r.Deliveries, m.r.RawEconomy, m.r.Economy, m.delta, m.r.Weight)
	}
	r.p("")
}
