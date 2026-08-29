package engine

import "testing"

// TestDraftPoolIsPlayable checks the two things a selection screen must
// guarantee: that some legal squad exists, and that the budget still forces a
// choice.
//
// The first version failed the first test. It sorted by price and kept the top
// slice, so the pool held only expensive players and the cheapest five bowlers
// cost more than the entire budget. Nothing on the screen could be completed.
func TestDraftPoolIsPlayable(t *testing.T) {
	e := testEngine(t)
	bowl, bat := e.DraftPool(28, 28)

	for _, tc := range []struct {
		name   string
		pool   []Rated
		squad  int
		budget int
	}{
		{"bowlers", bowl, SquadBowlers, BowlerBudget},
		{"batters", bat, SquadBatters, BatterBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.pool) < tc.squad {
				t.Fatalf("pool has %d, a squad needs %d", len(tc.pool), tc.squad)
			}

			costs := make([]int, 0, len(tc.pool))
			seen := map[string]bool{}
			for _, r := range tc.pool {
				if r.Name == "" {
					t.Error("a pool entry has no name")
				}
				if seen[r.Name] {
					t.Errorf("%s appears twice in the pool", r.Name)
				}
				seen[r.Name] = true
				if r.Cost < MinCost || r.Cost > MaxCost {
					t.Errorf("%s costs %d, outside [%d, %d]", r.Name, r.Cost, MinCost, MaxCost)
				}
				costs = append(costs, r.Cost)
			}

			cheapest, dearest := 0, 0
			sortInts(costs)
			for i := range tc.squad {
				cheapest += costs[i]
				dearest += costs[len(costs)-1-i]
			}

			// A legal squad has to exist.
			if cheapest > tc.budget {
				t.Errorf("the cheapest %d cost %d credits, over the %d budget: no legal squad exists",
					tc.squad, cheapest, tc.budget)
			}
			// And the best squad has to be out of reach, or there is no choice.
			if dearest <= tc.budget {
				t.Errorf("the best %d cost %d credits, inside the %d budget: the budget forces nothing",
					tc.squad, dearest, tc.budget)
			}
			// There must also be real room between the two, or only one side is
			// legal and the screen is a formality rather than a decision.
			if slack := tc.budget - cheapest; slack < 8 {
				t.Errorf("only %d credits between the cheapest legal squad and the budget; "+
					"there is barely a choice to make", slack)
			}
			t.Logf("%d on offer, %d to %d credits; cheapest squad %d, best squad %d, budget %d",
				len(tc.pool), costs[0], costs[len(costs)-1], cheapest, dearest, tc.budget)
		})
	}
}

func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
