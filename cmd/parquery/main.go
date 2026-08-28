// Command parquery answers questions of the corpus from the command line.
//
// It exists to prove the milestone 2 claim: that the in-memory store and the
// matchup graph answer a real question fast enough that no database is
// warranted. The canonical example is
//
//	parquery dist -bowler "JJ Bumrah" -phase death -vs-hand LHB
//
// which aggregates every ball Bumrah has bowled to a left-hander in the last
// five overs and prints the outcome distribution.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"manhattan/internal/attr"
	"manhattan/internal/corpus"
	"manhattan/internal/graph"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var (
		corpusPath = fs.String("corpus", filepath.Join("data", "out", "corpus.bin"), "corpus binary")
		attrPath   = fs.String("attributes", filepath.Join("data", "attributes", "players.csv"), "player attribute table")
		bowler     = fs.String("bowler", "", "bowler name or Cricsheet id")
		batter     = fs.String("batter", "", "batter name or Cricsheet id")
		phase      = fs.String("phase", "", "powerplay, middle or death")
		overs      = fs.String("overs", "", "over range, e.g. 15-19 (1-based, inclusive)")
		vsHand     = fs.String("vs-hand", "", "restrict the opposing batter to RHB or LHB")
		vsClass    = fs.String("vs-class", "", "restrict the opposing bowler to RAP, LAP, OB, LB, SLA or SLW")
		seasons    = fs.String("seasons", "", "season range, e.g. 2020-2026")
		limit      = fs.Int("limit", 15, "rows to print")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	q := &query{
		bowler: *bowler, batter: *batter, phase: *phase, overs: *overs,
		vsHand: *vsHand, vsClass: *vsClass, seasons: *seasons, limit: *limit,
	}
	if err := q.load(*corpusPath, *attrPath); err != nil {
		fmt.Fprintln(os.Stderr, "parquery:", err)
		os.Exit(1)
	}

	var err error
	switch cmd {
	case "dist":
		err = q.distribution()
	case "matchup":
		err = q.matchup()
	case "worst":
		err = q.worst()
	case "graph":
		err = q.graphStats()
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "parquery: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "parquery:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `parquery answers questions of the Manhattan corpus.

Commands:
  dist      outcome distribution for a filtered set of deliveries
  matchup   one batter against one bowler, from the matchup graph
  worst     the bowlers a batter scores fastest against
  graph     size and shape of the matchup graph

Examples:
  parquery dist -bowler "JJ Bumrah" -phase death -vs-hand LHB
  parquery dist -bowler "R Ashwin" -vs-hand LHB -seasons 2020-2026
  parquery matchup -batter "V Kohli" -bowler "JJ Bumrah"
  parquery worst -batter "N Pooran" -limit 10
  parquery graph

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `  -bowler, -batter      name or Cricsheet id
  -phase                powerplay, middle, death
  -overs                over range, 1-based inclusive, e.g. 15-19
  -vs-hand              RHB or LHB
  -vs-class             RAP, LAP, OB, LB, SLA, SLW
  -seasons              e.g. 2020-2026
  -limit                rows to print
`)
}

type query struct {
	st    *corpus.Store
	attrs attr.Table
	g     *graph.Graph

	bowler, batter  string
	phase, overs    string
	vsHand, vsClass string
	seasons         string
	limit           int

	loadMS, graphMS float64
}

func (q *query) load(corpusPath, attrPath string) error {
	t0 := time.Now()
	st, err := corpus.Load(corpusPath)
	if err != nil {
		return err
	}
	q.st = st
	q.loadMS = float64(time.Since(t0).Microseconds()) / 1000

	q.attrs, err = attr.Load(attrPath)
	if err != nil {
		return err
	}

	t1 := time.Now()
	q.g = graph.Build(st)
	q.graphMS = float64(time.Since(t1).Microseconds()) / 1000
	if err := q.g.Validate(); err != nil {
		return err
	}
	return nil
}

// resolve finds a player by Cricsheet identifier, exact name, or unambiguous
// case-insensitive substring. An ambiguous name is an error rather than a
// silent pick: the corpus contains two cricketers called Harmeet Singh.
func (q *query) resolve(s string) (corpus.PlayerID, error) {
	if s == "" {
		return corpus.NoPlayer, nil
	}
	if id, ok := q.st.PlayerByCricsheetID(s); ok {
		return id, nil
	}

	var exact, partial []corpus.PlayerID
	needle := strings.ToLower(s)
	for i, p := range q.st.Players {
		if p.Name == s {
			exact = append(exact, corpus.PlayerID(i))
			continue
		}
		if strings.Contains(strings.ToLower(p.Name), needle) {
			partial = append(partial, corpus.PlayerID(i))
			continue
		}
		for _, a := range p.Aliases {
			if strings.Contains(strings.ToLower(a), needle) {
				partial = append(partial, corpus.PlayerID(i))
				break
			}
		}
	}
	hits := exact
	if len(hits) == 0 {
		hits = partial
	}

	switch len(hits) {
	case 0:
		return corpus.NoPlayer, fmt.Errorf("no player matches %q", s)
	case 1:
		return hits[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q is ambiguous, matches %d players:", s, len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "\n  %s  %s", q.st.Players[h].CricsheetID, q.st.Players[h].Name)
	}
	return corpus.NoPlayer, fmt.Errorf("%s", b.String())
}

// filter assembles a corpus filter from the command-line flags.
func (q *query) filter() (corpus.Filter, error) {
	f := corpus.NewFilter()

	var err error
	if f.Bowler, err = q.resolve(q.bowler); err != nil {
		return f, err
	}
	if f.Batter, err = q.resolve(q.batter); err != nil {
		return f, err
	}

	switch strings.ToLower(q.phase) {
	case "":
	case "powerplay", "pp":
		f = f.Phase(corpus.PhasePowerplay)
	case "middle", "mid":
		f = f.Phase(corpus.PhaseMiddle)
	case "death":
		f = f.Phase(corpus.PhaseDeath)
	default:
		return f, fmt.Errorf("unknown phase %q; want powerplay, middle or death", q.phase)
	}

	if q.overs != "" {
		lo, hi, err := parseRange(q.overs)
		if err != nil {
			return f, fmt.Errorf("bad -overs: %w", err)
		}
		if lo < 1 || hi > 20 || lo > hi {
			return f, fmt.Errorf("bad -overs %q: want 1-20", q.overs)
		}
		// Flags are 1-based to match how cricket is spoken; the corpus is
		// 0-based.
		f.MinOver, f.MaxOver = uint8(lo-1), uint8(hi-1)
	}

	if q.seasons != "" {
		lo, hi, err := parseRange(q.seasons)
		if err != nil {
			return f, fmt.Errorf("bad -seasons: %w", err)
		}
		f.SeasonFrom, f.SeasonTo = uint16(lo), uint16(hi)
	}

	if q.vsHand != "" {
		want := attr.ParseHand(q.vsHand)
		if want == attr.HandUnknown {
			return f, fmt.Errorf("unknown -vs-hand %q; want RHB or LHB", q.vsHand)
		}
		f.BatterIn = q.st.MaskOf(func(id string) bool {
			h, ok := q.attrs.BatOf(id)
			return ok && h == want
		})
	}
	if q.vsClass != "" {
		want := attr.ParseBowlClass(q.vsClass)
		if want == attr.BowlUnknown {
			return f, fmt.Errorf("unknown -vs-class %q; want RAP, LAP, OB, LB, SLA or SLW", q.vsClass)
		}
		f.BowlerIn = q.st.MaskOf(func(id string) bool {
			c, ok := q.attrs.BowlOf(id)
			return ok && c == want
		})
	}
	return f, nil
}

func parseRange(s string) (lo, hi int, err error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q is not a range like 15-19", s)
	}
	if _, err = fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &lo); err != nil {
		return 0, 0, fmt.Errorf("%q is not a range like 15-19", s)
	}
	if _, err = fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &hi); err != nil {
		return 0, 0, fmt.Errorf("%q is not a range like 15-19", s)
	}
	return lo, hi, nil
}

func (q *query) describe(f corpus.Filter) string {
	var parts []string
	if f.Bowler != corpus.NoPlayer {
		parts = append(parts, "bowler "+q.st.PlayerName(f.Bowler))
	}
	if f.Batter != corpus.NoPlayer {
		parts = append(parts, "batter "+q.st.PlayerName(f.Batter))
	}
	if f.MinOver != 0 || f.MaxOver != 255 {
		parts = append(parts, fmt.Sprintf("overs %d-%d", f.MinOver+1, f.MaxOver+1))
	}
	if q.vsHand != "" {
		parts = append(parts, "vs "+strings.ToUpper(q.vsHand))
	}
	if q.vsClass != "" {
		parts = append(parts, "vs "+strings.ToUpper(q.vsClass))
	}
	if f.SeasonFrom != 0 {
		parts = append(parts, fmt.Sprintf("seasons %d-%d", f.SeasonFrom, f.SeasonTo))
	}
	if len(parts) == 0 {
		return "all deliveries"
	}
	return strings.Join(parts, ", ")
}

func (q *query) distribution() error {
	f, err := q.filter()
	if err != nil {
		return err
	}

	t0 := time.Now()
	d := q.st.Aggregate(f)
	took := time.Since(t0)

	fmt.Printf("%s\n\n", q.describe(f))
	if d.Deliveries == 0 {
		fmt.Println("no deliveries match")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "outcome\tballs\tshare")
	for o := range corpus.NumOutcomes {
		n := d.Outcomes[o]
		fmt.Fprintf(w, "%s\t%d\t%.2f%%\n", corpus.Outcome(o), n, 100*float64(n)/float64(d.Deliveries))
	}
	fmt.Fprintf(w, "\t\t\n")
	fmt.Fprintf(w, "legal balls\t%d\t\n", d.LegalBalls)
	fmt.Fprintf(w, "runs\t%d\t(%d off the bat, %d extras)\n", d.TotalRuns, d.RunsOffBat, d.Extras)
	fmt.Fprintf(w, "wickets\t%d\t(%d to the bowler)\n", d.Wickets, d.BowlerWkts)
	fmt.Fprintf(w, "economy\t%.2f\t\n", d.Economy())
	fmt.Fprintf(w, "strike rate\t%.1f\t\n", d.StrikeRate())
	fmt.Fprintf(w, "dot rate\t%.1f%%\t\n", 100*d.DotRate())
	fmt.Fprintf(w, "boundary rate\t%.1f%%\t\n", 100*d.BoundaryRate())
	if bpw := d.BallsPerWicket(); bpw > 0 {
		fmt.Fprintf(w, "balls per wicket\t%.1f\t\n", bpw)
	}
	w.Flush()

	fmt.Printf("\nscanned %d deliveries in %.2fms (corpus load %.1fms, graph build %.1fms)\n",
		q.st.D.Len(), float64(took.Microseconds())/1000, q.loadMS, q.graphMS)
	return nil
}

func (q *query) matchup() error {
	bat, err := q.resolve(q.batter)
	if err != nil {
		return err
	}
	bowl, err := q.resolve(q.bowler)
	if err != nil {
		return err
	}
	if bat == corpus.NoPlayer || bowl == corpus.NoPlayer {
		return fmt.Errorf("matchup needs both -batter and -bowler")
	}

	t0 := time.Now()
	e, ok := q.g.Matchup(bat, bowl)
	took := time.Since(t0)

	fmt.Printf("%s vs %s\n\n", q.st.PlayerName(bat), q.st.PlayerName(bowl))
	if !ok {
		fmt.Println("these two have never met")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "balls\t%d\n", e.Balls)
	fmt.Fprintf(w, "runs\t%d\n", e.Runs)
	fmt.Fprintf(w, "dismissals\t%d\n", e.Dismissals)
	fmt.Fprintf(w, "strike rate\t%.1f\n", e.StrikeRate())
	if avg, ok := e.Average(); ok {
		fmt.Fprintf(w, "average\t%.1f\n", avg)
	} else {
		fmt.Fprintf(w, "average\tnever out\n")
	}
	fmt.Fprintf(w, "dots / 4s / 6s\t%d / %d / %d\n", e.Dots, e.Fours, e.Sixes)
	w.Flush()

	fmt.Printf("\ngraph lookup in %.3fms over %d edges\n", float64(took.Nanoseconds())/1e6, q.g.Edges())
	return nil
}

// worst walks one batter's row of the matchup graph, which is the traversal the
// CSR layout exists to make cheap.
func (q *query) worst() error {
	bat, err := q.resolve(q.batter)
	if err != nil {
		return err
	}
	if bat == corpus.NoPlayer {
		return fmt.Errorf("worst needs -batter")
	}

	t0 := time.Now()
	bowlers, edges := q.g.BowlersFaced(bat)
	type row struct {
		id corpus.PlayerID
		e  graph.Edge
	}
	var rows []row
	for i, b := range bowlers {
		if edges[i].Balls >= 30 {
			rows = append(rows, row{b, edges[i]})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].e.StrikeRate() > rows[j].e.StrikeRate() })
	took := time.Since(t0)

	fmt.Printf("%s: bowlers scored fastest against, minimum 30 balls\n\n", q.st.PlayerName(bat))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "bowler\tclass\tballs\truns\touts\tSR")
	for i, r := range rows {
		if i >= q.limit {
			break
		}
		class := ""
		if c, ok := q.attrs.BowlOf(q.st.Players[r.id].CricsheetID); ok {
			class = c.String()
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%.1f\n",
			q.st.PlayerName(r.id), class, r.e.Balls, r.e.Runs, r.e.Dismissals, r.e.StrikeRate())
	}
	w.Flush()

	fmt.Printf("\ntraversed %d matchups in %.3fms\n", len(bowlers), float64(took.Nanoseconds())/1e6)
	return nil
}

func (q *query) graphStats() error {
	var maxBat, maxBow int
	var maxBatP, maxBowP corpus.PlayerID
	for i := range q.st.Players {
		b, w := q.g.Degree(corpus.PlayerID(i))
		if b > maxBat {
			maxBat, maxBatP = b, corpus.PlayerID(i)
		}
		if w > maxBow {
			maxBow, maxBowP = w, corpus.PlayerID(i)
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "deliveries\t%d\n", q.st.D.Len())
	fmt.Fprintf(w, "innings\t%d\n", q.st.Inn.Len())
	fmt.Fprintf(w, "matches\t%d\n", q.st.M.Len())
	fmt.Fprintf(w, "player nodes\t%d\n", q.g.Nodes())
	fmt.Fprintf(w, "matchup edges\t%d\n", q.g.Edges())
	fmt.Fprintf(w, "density\t%.4f%%\n", 100*float64(q.g.Edges())/float64(q.g.Nodes()*q.g.Nodes()))
	fmt.Fprintf(w, "attribute rows\t%d\n", len(q.attrs))
	fmt.Fprintf(w, "most bowlers faced\t%s (%d)\n", q.st.PlayerName(maxBatP), maxBat)
	fmt.Fprintf(w, "most batters bowled to\t%s (%d)\n", q.st.PlayerName(maxBowP), maxBow)
	fmt.Fprintf(w, "corpus load\t%.1fms\n", q.loadMS)
	fmt.Fprintf(w, "graph build\t%.1fms\n", q.graphMS)
	w.Flush()
	return nil
}
