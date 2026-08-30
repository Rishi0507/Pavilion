package engine

import (
	"encoding/binary"
	"fmt"
	"sort"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
	"pavilion/internal/rates"
	"pavilion/internal/sim"
)

// Puzzle construction.
//
// Everything here is derived from the day's key, so the puzzle is identical for
// every player in the world and cannot be guessed before release. Nothing is
// random at request time.
//
// The pool is restricted to players active in recent seasons. That is a
// recognition decision rather than a statistical one: the game is about
// cricketers people have watched, and a 2011 net bowler with a defensible
// sample is still not someone anyone wants to see dealt.
const recentSince = 2022

// BuildPuzzle assembles one day's problem from the day's key.
func (e *Engine) BuildPuzzle(date string, key sim.DailyKey, target uint16) (*sim.Puzzle, error) {
	// A small deterministic stream, seeded by the day, used only for selection.
	// It never touches the match itself, which draws from the coordinate scheme.
	pick := newPicker(key)

	bowlPool := e.pickTop(e.bowlers, recentSince, 140, func(v corpus.Volume) int { return v.BallsBowled })
	batPool := e.pickTop(e.batters, recentSince, 60, func(v corpus.Volume) int { return v.BallsFaced })
	if len(bowlPool) < 5 {
		return nil, fmt.Errorf("engine: only %d eligible bowlers", len(bowlPool))
	}
	if len(batPool) < 11 {
		return nil, fmt.Errorf("engine: only %d eligible batters", len(batPool))
	}

	p := &sim.Puzzle{Date: date, Target: target}

	attack := e.dealAttack(pick, bowlPool)
	for _, id := range attack {
		p.Attack = append(p.Attack, e.player(id))
	}

	batting := pick.choose(batPool, 11)
	// Order by how much they have batted, which approximates a real top order.
	vols := e.store.Volumes()
	sort.Slice(batting, func(i, j int) bool {
		return vols[batting[i]].BallsFaced > vols[batting[j]].BallsFaced
	})
	for _, id := range batting {
		p.Batting = append(p.Batting, e.player(id))
	}

	// A venue with enough history for its run rate to mean something.
	p.Venue = pick.venue(e.store)
	return p, nil
}

// dealAttack builds the five bowlers the player must get twenty overs out of.
//
// The composition is the whole puzzle. An attack of five elite internationals
// is not a decision: they are interchangeable, and measurement bears that out,
// with barely a run an over between the best and worst of them. A real T20 side
// has three or four bowlers it trusts and a fifth who is a batter who bowls a
// bit, and hiding that fifth bowler's four overs is the oldest captaincy
// problem in the format.
//
// So the attack is stratified by shrunk death-overs economy: two from the top
// third, one from the middle, and two from the bottom. That guarantees a real
// range of quality, which is what makes "who bowls the seventeenth" a question
// rather than a formality. At least one spinner and one seamer are included,
// because an attack of one type makes every over the same decision too.
func (e *Engine) dealAttack(pick *picker, pool []corpus.PlayerID) []corpus.PlayerID {
	t := e.ctx.Rates()
	death := func(id corpus.PlayerID) float64 {
		return t.Phase(int(id), rates.Bowling, corpus.PhaseDeath).Economy
	}

	ranked := append([]corpus.PlayerID(nil), pool...)
	sort.Slice(ranked, func(i, j int) bool { return death(ranked[i]) < death(ranked[j]) })

	third := len(ranked) / 3
	if third < 2 {
		return pick.choose(pool, min(5, len(pool)))
	}
	tiers := [][]corpus.PlayerID{
		ranked[:third],          // the ones you want at the death
		ranked[third : 2*third], // honest workhorses
		ranked[2*third:],        // the fifth bowler nobody wants to give an over
	}
	want := []int{2, 1, 2}

	var out []corpus.PlayerID
	taken := map[corpus.PlayerID]bool{}
	for i, n := range want {
		for _, id := range pick.choose(tiers[i], len(tiers[i])) {
			if n == 0 {
				break
			}
			if !taken[id] {
				out = append(out, id)
				taken[id] = true
				n--
			}
		}
	}

	// Ensure both a seamer and a spinner are present, swapping the least
	// consequential pick if not.
	isSpin := func(id corpus.PlayerID) bool {
		c, _ := e.attrs.BowlOf(e.store.Players[id].CricsheetID)
		return c.IsSpin()
	}
	spins := 0
	for _, id := range out {
		if isSpin(id) {
			spins++
		}
	}
	if spins == 0 || spins == len(out) {
		wantSpin := spins == 0
		for _, id := range pick.choose(pool, len(pool)) {
			if taken[id] || isSpin(id) != wantSpin {
				continue
			}
			// Replace the middle-tier pick, the one that matters least.
			out[2] = id
			break
		}
	}

	sort.Slice(out, func(i, j int) bool { return death(out[i]) < death(out[j]) })
	return out
}

// picker is a small deterministic chooser seeded by the day's key.
type picker struct {
	state uint64
}

func newPicker(key sim.DailyKey) *picker {
	return &picker{state: binary.LittleEndian.Uint64(key[:8]) | 1}
}

// next is a splitmix64 step: short, deterministic and good enough for choosing
// eleven names out of sixty.
func (p *picker) next() uint64 {
	p.state += 0x9E3779B97F4A7C15
	z := p.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (p *picker) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(p.next() % uint64(n))
}

// choose takes n distinct entries from a pool.
func (p *picker) choose(pool []corpus.PlayerID, n int) []corpus.PlayerID {
	c := append([]corpus.PlayerID(nil), pool...)
	for i := len(c) - 1; i > 0; i-- {
		j := p.intn(i + 1)
		c[i], c[j] = c[j], c[i]
	}
	if len(c) > n {
		c = c[:n]
	}
	return c
}

// chooseSpread takes n entries while insisting on at least one of each group,
// so an attack always contains both pace and spin.
func (p *picker) chooseSpread(pool []corpus.PlayerID, n int, group func(corpus.PlayerID) int) []corpus.PlayerID {
	groups := map[int][]corpus.PlayerID{}
	for _, id := range pool {
		g := group(id)
		groups[g] = append(groups[g], id)
	}

	var out []corpus.PlayerID
	taken := map[corpus.PlayerID]bool{}

	keys := make([]int, 0, len(groups))
	for g := range groups {
		keys = append(keys, g)
	}
	sort.Ints(keys)

	// At least two of each group, so neither pace nor spin is a token presence.
	for _, g := range keys {
		for _, id := range p.choose(groups[g], min(2, len(groups[g]))) {
			if !taken[id] && len(out) < n {
				out = append(out, id)
				taken[id] = true
			}
		}
	}
	for _, id := range p.choose(pool, len(pool)) {
		if len(out) >= n {
			break
		}
		if !taken[id] {
			out = append(out, id)
			taken[id] = true
		}
	}
	return out
}

// venue chooses a ground with a meaningful amount of history.
func (p *picker) venue(s *corpus.Store) corpus.VenueID {
	balls := make([]int, len(s.Venues))
	for i := range s.D.Innings {
		if s.D.Legal[i] {
			balls[s.M.Venue[s.Inn.Match[s.D.Innings[i]]]]++
		}
	}
	var busy []corpus.VenueID
	for v, n := range balls {
		if n >= 5000 {
			busy = append(busy, corpus.VenueID(v))
		}
	}
	if len(busy) == 0 {
		return 0
	}
	return busy[p.intn(len(busy))]
}

// Describe renders a puzzle's attack for display.
func (e *Engine) Describe(p *sim.Puzzle) string {
	out := fmt.Sprintf("target %d at %s\n", p.Target, e.store.Venues[p.Venue])
	out += "attack:\n"
	for i, b := range p.Attack {
		out += fmt.Sprintf("  %d %-22s %s\n", i+1, b.Name, className(b.Class))
	}
	return out
}

func className(c attr.BowlClass) string {
	switch c {
	case attr.RightArmPace:
		return "right-arm pace"
	case attr.LeftArmPace:
		return "left-arm pace"
	case attr.OffBreak:
		return "off break"
	case attr.LegBreak:
		return "leg break"
	case attr.LeftArmOrthodox:
		return "left-arm orthodox"
	case attr.LeftArmWrist:
		return "left-arm wrist spin"
	}
	return "unknown"
}

// FromQueued rebuilds a puzzle from an approved queue entry.
//
// The queue stores player identifiers rather than a re-derivation recipe, so a
// day that was validated is the day that gets played. Regenerating it from the
// key would work only as long as nothing about selection ever changed, and the
// first change to the pool would silently invalidate every queued day.
func (e *Engine) FromQueued(date string, target uint16, venue corpus.VenueID,
	attackIDs, battingIDs []uint16) (*sim.Puzzle, error) {

	if len(attackIDs) != 5 {
		return nil, fmt.Errorf("engine: queued puzzle has %d bowlers, want 5", len(attackIDs))
	}
	if len(battingIDs) < 2 {
		return nil, fmt.Errorf("engine: queued puzzle has %d batters", len(battingIDs))
	}
	p := &sim.Puzzle{Date: date, Target: target, Venue: venue}
	for _, id := range attackIDs {
		if int(id) >= len(e.store.Players) {
			return nil, fmt.Errorf("engine: queued bowler id %d is not in the corpus", id)
		}
		p.Attack = append(p.Attack, e.player(corpus.PlayerID(id)))
	}
	for _, id := range battingIDs {
		if int(id) >= len(e.store.Players) {
			return nil, fmt.Errorf("engine: queued batter id %d is not in the corpus", id)
		}
		p.Batting = append(p.Batting, e.player(corpus.PlayerID(id)))
	}
	return p, nil
}
