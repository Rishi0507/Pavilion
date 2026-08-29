package engine

import (
	"math"
	"sort"

	"manhattan/internal/corpus"
	"manhattan/internal/rates"
)

// Player ratings and what they cost to pick.
//
// The draft mode exists so that choosing a side is itself a decision, which it
// only is if you cannot have everybody. So each player carries a price derived
// from how good they actually are, and the budget is deliberately too small for
// five of the best. Picking Bumrah means finding four overs somewhere cheaper,
// which is the same shape of problem as the game itself.
//
// Prices come from the shrunk rate table rather than raw career figures, so a
// bowler with one good season is not priced like one with ten. That is the
// whole reason the shrinkage exists.

// Rated is a player offered in the draft.
type Rated struct {
	ID     corpus.PlayerID `json:"id"`
	Name   string          `json:"name"`
	Style  string          `json:"style"`
	Hand   string          `json:"hand"`
	Cost   int             `json:"cost"`
	Rating float64         `json:"rating"` // 0 to 100, higher is better
	Note   string          `json:"note"`   // a plain-language reason for the price
}

// Draft budgets.
//
// Each is set between the cheapest possible squad and the best possible one,
// with real room in between. A budget equal to the cheapest squad leaves
// exactly one legal side and therefore no decision, which is how the batting
// budget was first set; a budget that reaches the best side is not a
// constraint at all. Measured against the current pool the bowling squads run
// 49 to 74 credits and the batting squads 66 to 99, so the budgets sit near the
// lower third of each: enough to afford one or two of the best, never five.
const (
	BowlerBudget = 62
	BatterBudget = 90
	SquadBowlers = 5
	SquadBatters = 6
	MinCost      = 3
	MaxCost      = 20
)

// bowlerQuality scores a bowler between 0 and 1, higher being better.
//
// Death overs carry the most weight because that is where the game is decided
// and where bowlers differ most; the powerplay carries the least because the
// measured spread between bowlers there is barely a run an over.
func (e *Engine) bowlerQuality(id corpus.PlayerID) (float64, string) {
	t := e.ctx.Rates()
	if t == nil || int(id) >= len(t.Players) {
		return 0, ""
	}
	death := t.Phase(int(id), rates.Bowling, corpus.PhaseDeath)
	middle := t.Phase(int(id), rates.Bowling, corpus.PhaseMiddle)
	power := t.Phase(int(id), rates.Bowling, corpus.PhasePowerplay)

	econ := 0.0
	weight := 0.0
	for _, p := range []struct {
		r rates.PhaseRate
		w float64
	}{{death, 0.5}, {middle, 0.3}, {power, 0.2}} {
		if p.r.Economy > 0 {
			econ += p.r.Economy * p.w
			weight += p.w
		}
	}
	if weight == 0 {
		return 0, ""
	}
	econ /= weight

	// Around 7.5 an over is excellent and 11 is expensive, so the scale is set
	// between them and clamped.
	q := (11.0 - econ) / 3.5
	q = math.Max(0, math.Min(1, q))

	note := "goes for " + oneDecimal(death.Economy) + " an over at the death"
	if death.Deliveries < 150 {
		note = "little death-overs record; priced cautiously"
	}
	return q, note
}

// batterQuality scores a batter between 0 and 1.
//
// Strike rate and survival are both required: a batter who scores quickly and
// is out every twelve balls is not more valuable than one who does both, and a
// chase needs wickets in hand as much as runs.
func (e *Engine) batterQuality(id corpus.PlayerID) (float64, string) {
	t := e.ctx.Rates()
	if t == nil || int(id) >= len(t.Players) {
		return 0, ""
	}
	death := t.Phase(int(id), rates.Batting, corpus.PhaseDeath)
	middle := t.Phase(int(id), rates.Batting, corpus.PhaseMiddle)
	power := t.Phase(int(id), rates.Batting, corpus.PhasePowerplay)

	sr, weight := 0.0, 0.0
	for _, p := range []struct {
		r rates.PhaseRate
		w float64
	}{{death, 0.4}, {middle, 0.35}, {power, 0.25}} {
		if p.r.StrikeRate > 0 {
			sr += p.r.StrikeRate * p.w
			weight += p.w
		}
	}
	if weight == 0 {
		return 0, ""
	}
	sr /= weight

	// 110 is a slow innings by modern standards and 165 is very quick.
	scoring := math.Max(0, math.Min(1, (sr-110)/55))

	survival := 0.5
	if bpw := middle.BallsPerWkt; bpw > 0 {
		// Twelve balls an innings is fragile, thirty is dependable.
		survival = math.Max(0, math.Min(1, (bpw-12)/18))
	}

	q := 0.7*scoring + 0.3*survival
	return q, "strikes at " + oneDecimal(sr)
}

func oneDecimal(f float64) string {
	whole := int(f)
	frac := int(math.Round((f - float64(whole)) * 10))
	if frac == 10 {
		whole, frac = whole+1, 0
	}
	return itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// costOf maps a quality score onto a price.
func costOf(q float64) int {
	c := int(math.Round(float64(MinCost) + q*float64(MaxCost-MinCost)))
	return max(MinCost, min(MaxCost, c))
}

// Draft eligibility.
//
// Clearing the corpus-wide threshold is not the same as being a batter. A
// spinner who has faced three hundred balls at number nine over a long career
// qualifies as "eligible" and is emphatically not someone anyone would pick to
// chase a total, which is why Amit Mishra was appearing among the batting
// options. The draft therefore asks for a real record in the role, not merely a
// countable one.
const (
	draftMinBallsFaced  = 500
	draftMinBallsBowled = 400
	draftMinBatQuality  = 0.20

	// The clearest signal of whether someone is a batter is how long they last
	// when they get in. A top-order player faces twenty-five or thirty balls an
	// innings; a bowler who bats at nine faces three or four, however many they
	// accumulate over a decade. Career totals cannot tell the two apart, and
	// that is how Chawla and Mishra reached the batting screen.
	draftMinBallsPerInnings = 11.0
)

// DraftPool returns the bowlers and batters a player may choose between.
//
// Everyone with a real record in the role is offered, sorted by price, so the
// choice is genuinely open rather than a short list somebody else drew up.
func (e *Engine) DraftPool(bowlers, batters int) (bowl, bat []Rated) {
	vols := e.store.Volumes()

	for _, id := range e.bowlers {
		if vols[id].LastSeason < recentSince || vols[id].BallsBowled < draftMinBallsBowled {
			continue
		}
		q, note := e.bowlerQuality(id)
		if q <= 0 {
			continue
		}
		p := e.player(id)
		bowl = append(bowl, Rated{
			ID: id, Name: p.Name, Style: ClassName(p.Class),
			Cost: costOf(q), Rating: math.Round(q * 100), Note: note,
		})
	}
	for _, id := range e.batters {
		v := vols[id]
		if v.LastSeason < recentSince || v.BallsFaced < draftMinBallsFaced {
			continue
		}
		if v.Innings == 0 || float64(v.BallsFaced)/float64(v.Innings) < draftMinBallsPerInnings {
			continue
		}
		q, note := e.batterQuality(id)
		// A low score here means a tailender rather than a cheap option, and a
		// side of tailenders is not a budget decision, it is a broken screen.
		if q < draftMinBatQuality {
			continue
		}
		p := e.player(id)
		bat = append(bat, Rated{
			ID: id, Name: p.Name, Hand: HandName(p.Hand),
			Cost: costOf(q), Rating: math.Round(q * 100), Note: note,
		})
	}

	byCost := func(r []Rated) {
		sort.Slice(r, func(i, j int) bool {
			if r[i].Cost != r[j].Cost {
				return r[i].Cost > r[j].Cost
			}
			return r[i].Name < r[j].Name
		})
	}
	byCost(bowl)
	byCost(bat)

	// A cap of zero means everyone who qualifies.
	if bowlers > 0 {
		bowl = spread(bowl, bowlers, SquadBowlers, BowlerBudget)
	} else {
		bowl = affordable(bowl, SquadBowlers, BowlerBudget)
	}
	if batters > 0 {
		bat = spread(bat, batters, SquadBatters, BatterBudget)
	} else {
		bat = affordable(bat, SquadBatters, BatterBudget)
	}
	return bowl, bat
}

// spread trims a price-sorted list to n entries while keeping its whole range.
//
// Taking the first n instead, which is what this did at first, keeps only the
// most expensive players: the cheapest five bowlers on offer then cost 65
// credits against a budget of 55, and no legal side existed at all. A draft
// whose budget cannot be met is not a hard choice, it is a broken screen.
//
// Sampling evenly across the sorted list keeps the stars at one end and the
// part-timers at the other, which is what makes the budget bite. The result is
// then checked: if the cheapest possible squad still does not fit, the cheapest
// players are added back until it does.
func spread(all []Rated, n, squad, budget int) []Rated {
	if len(all) <= n {
		return affordable(all, squad, budget)
	}
	out := make([]Rated, 0, n)
	for i := range n {
		out = append(out, all[i*(len(all)-1)/(n-1)])
	}
	return affordable(out, squad, budget)
}

// affordable guarantees that some legal squad exists.
func affordable(pool []Rated, squad, budget int) []Rated {
	cheapest := func(p []Rated) int {
		costs := make([]int, len(p))
		for i, r := range p {
			costs[i] = r.Cost
		}
		sort.Ints(costs)
		total := 0
		for i := 0; i < squad && i < len(costs); i++ {
			total += costs[i]
		}
		return total
	}
	if len(pool) >= squad && cheapest(pool) <= budget {
		return pool
	}
	// Should not happen with a healthy corpus, but a screen that cannot be
	// completed is worse than a slightly cheaper one, so the floor is lowered
	// rather than the player being stuck.
	for i := range pool {
		if pool[i].Cost > MinCost {
			pool[i].Cost--
		}
	}
	if cheapest(pool) > budget {
		return affordable(pool, squad, budget)
	}
	return pool
}
