// Package graph holds the player matchup network.
//
// This is the part of the project where a graph genuinely earns its place. The
// ball-by-ball corpus is a columnar scan problem and lives in package corpus;
// the matchup network is a real sparse graph, and it is what turns "Pooran
// murders spin" from a hardcoded string into a computed fact.
//
// The representation is CSR: an offsets slice and a neighbours slice, built
// once and never mutated. There is no locking because there is nothing to lock,
// and a row lookup is a slice bound plus a binary search.
package graph

import (
	"fmt"
	"sort"

	"manhattan/internal/corpus"
)

// Edge is one batter-versus-bowler matchup, aggregated over every delivery
// between them.
type Edge struct {
	Balls      uint32 // legal deliveries
	Runs       uint32 // runs off the bat
	Dismissals uint32 // times the bowler dismissed this batter
	// Dots counts balls that scored nothing and did not take a wicket. A
	// wicket outranks a dot in the outcome taxonomy, so a wicket-taking dot
	// ball is counted once, as a dismissal.
	Dots  uint32
	Fours uint32
	Sixes uint32
}

// StrikeRate returns runs per hundred balls in this matchup.
func (e Edge) StrikeRate() float64 {
	if e.Balls == 0 {
		return 0
	}
	return 100 * float64(e.Runs) / float64(e.Balls)
}

// Average returns runs per dismissal, and whether the batter has ever been out
// to this bowler. An average is meaningless without a dismissal, so the caller
// is forced to notice.
func (e Edge) Average() (float64, bool) {
	if e.Dismissals == 0 {
		return 0, false
	}
	return float64(e.Runs) / float64(e.Dismissals), true
}

// csr is one direction of the adjacency structure.
type csr struct {
	offsets []uint32          // len = nodes + 1
	neigh   []corpus.PlayerID // sorted within each row
	edges   []Edge
}

// row returns the neighbours and edges for one node.
func (c *csr) row(n corpus.PlayerID) ([]corpus.PlayerID, []Edge) {
	if int(n)+1 >= len(c.offsets) {
		return nil, nil
	}
	lo, hi := c.offsets[n], c.offsets[n+1]
	return c.neigh[lo:hi], c.edges[lo:hi]
}

// find locates one neighbour by binary search within a row.
func (c *csr) find(from, to corpus.PlayerID) (Edge, bool) {
	neigh, edges := c.row(from)
	i := sort.Search(len(neigh), func(i int) bool { return neigh[i] >= to })
	if i < len(neigh) && neigh[i] == to {
		return edges[i], true
	}
	return Edge{}, false
}

// Graph is the immutable matchup network, held in both directions so that
// "who has this batter faced" and "who has this bowler bowled to" are both a
// single row lookup.
type Graph struct {
	nodes int
	byBat csr // batter -> bowlers faced
	byBow csr // bowler -> batters bowled to
}

// Nodes returns the number of player nodes.
func (g *Graph) Nodes() int { return g.nodes }

// Edges returns the number of distinct matchups.
func (g *Graph) Edges() int { return len(g.byBat.edges) }

// pair keys one matchup during construction.
type pair struct {
	bat, bowl corpus.PlayerID
}

// Options bounds what the graph is allowed to see.
type Options struct {
	// MaxSeason excludes every match after this edition year. Zero means no
	// bound. As with the rate table, this exists so that matchup features used
	// on a held-out season do not already contain that season's results.
	MaxSeason uint16
}

// Build constructs the matchup graph from the corpus.
//
// Super overs are excluded, consistent with every other aggregation: a one-over
// shootout would inflate death-overs matchups that never really happened.
func Build(s *corpus.Store, opt Options) *Graph {
	acc := make(map[pair]*Edge, 1<<16)

	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if s.Inn.SuperOver[inn] {
			continue
		}
		if opt.MaxSeason != 0 && s.M.Season[s.Inn.Match[inn]] > opt.MaxSeason {
			continue
		}
		bat, bowl := s.D.Batter[i], s.D.Bowler[i]
		if bat == corpus.NoPlayer || bowl == corpus.NoPlayer {
			continue
		}

		k := pair{bat, bowl}
		e := acc[k]
		if e == nil {
			e = &Edge{}
			acc[k] = e
		}
		if s.D.Legal[i] {
			e.Balls++
		}
		e.Runs += uint32(s.D.RunsBat[i])

		// Only dismissals credited to the bowler belong on this edge. A run
		// out says nothing about the matchup.
		if s.D.Wicket[i].CreditedToBowler() && s.D.PlayerOut[i] == bat {
			e.Dismissals++
		}
		switch s.D.Classify(i) {
		case corpus.Dot:
			e.Dots++
		case corpus.Four:
			e.Fours++
		case corpus.Six:
			e.Sixes++
		}
	}

	g := &Graph{nodes: len(s.Players)}
	g.byBat = buildCSR(acc, g.nodes, func(p pair) (corpus.PlayerID, corpus.PlayerID) { return p.bat, p.bowl })
	g.byBow = buildCSR(acc, g.nodes, func(p pair) (corpus.PlayerID, corpus.PlayerID) { return p.bowl, p.bat })
	return g
}

// buildCSR lays the accumulated edges out in compressed sparse row order,
// keyed by whichever endpoint the caller nominates as the row.
func buildCSR(acc map[pair]*Edge, nodes int, key func(pair) (from, to corpus.PlayerID)) csr {
	type entry struct {
		from, to corpus.PlayerID
		edge     Edge
	}
	list := make([]entry, 0, len(acc))
	for p, e := range acc {
		from, to := key(p)
		list = append(list, entry{from, to, *e})
	}
	// Sorting by (row, column) is what makes the row contiguous and the
	// within-row binary search valid. It also makes the build deterministic
	// despite the map iteration above.
	sort.Slice(list, func(i, j int) bool {
		if list[i].from != list[j].from {
			return list[i].from < list[j].from
		}
		return list[i].to < list[j].to
	})

	c := csr{
		offsets: make([]uint32, nodes+1),
		neigh:   make([]corpus.PlayerID, len(list)),
		edges:   make([]Edge, len(list)),
	}
	for i, e := range list {
		c.neigh[i] = e.to
		c.edges[i] = e.edge
		c.offsets[e.from+1]++
	}
	for i := 1; i <= nodes; i++ {
		c.offsets[i] += c.offsets[i-1]
	}
	return c
}

// Matchup returns the aggregate of every ball this bowler has bowled to this
// batter.
func (g *Graph) Matchup(bat, bowl corpus.PlayerID) (Edge, bool) {
	return g.byBat.find(bat, bowl)
}

// BowlersFaced returns every bowler this batter has faced, with the matchup,
// in ascending PlayerID order.
func (g *Graph) BowlersFaced(bat corpus.PlayerID) ([]corpus.PlayerID, []Edge) {
	return g.byBat.row(bat)
}

// BattersFaced returns every batter this bowler has bowled to.
func (g *Graph) BattersFaced(bowl corpus.PlayerID) ([]corpus.PlayerID, []Edge) {
	return g.byBow.row(bowl)
}

// Degree returns how many distinct bowlers a batter has faced and how many
// distinct batters a bowler has bowled to.
func (g *Graph) Degree(p corpus.PlayerID) (asBatter, asBowler int) {
	b, _ := g.byBat.row(p)
	w, _ := g.byBow.row(p)
	return len(b), len(w)
}

// Validate checks the structural invariants CSR relies on. It is cheap enough
// to run at boot and catches a malformed build before any query trusts it.
func (g *Graph) Validate() error {
	for _, c := range []struct {
		name string
		c    *csr
	}{{"batter", &g.byBat}, {"bowler", &g.byBow}} {
		if len(c.c.offsets) != g.nodes+1 {
			return fmt.Errorf("graph: %s offsets has %d entries, want %d", c.name, len(c.c.offsets), g.nodes+1)
		}
		if len(c.c.neigh) != len(c.c.edges) {
			return fmt.Errorf("graph: %s has %d neighbours but %d edges", c.name, len(c.c.neigh), len(c.c.edges))
		}
		if int(c.c.offsets[g.nodes]) != len(c.c.neigh) {
			return fmt.Errorf("graph: %s final offset %d does not match %d neighbours",
				c.name, c.c.offsets[g.nodes], len(c.c.neigh))
		}
		for n := range g.nodes {
			neigh, _ := c.c.row(corpus.PlayerID(n))
			for i := 1; i < len(neigh); i++ {
				if neigh[i-1] >= neigh[i] {
					return fmt.Errorf("graph: %s row %d is not strictly ascending at %d", c.name, n, i)
				}
			}
		}
	}
	return nil
}
