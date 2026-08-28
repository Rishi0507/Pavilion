package sim

import "testing"

// bruteForceCompletable answers the same question by exhaustive search: is
// there any ordering of the remaining overs in which nobody bowls twice in a
// row, given who bowled last?
func bruteForceCompletable(remaining []int, last int) bool {
	total := 0
	for _, r := range remaining {
		total += r
	}
	if total == 0 {
		return true
	}
	for i, r := range remaining {
		if r == 0 || i == last {
			continue
		}
		remaining[i]--
		ok := bruteForceCompletable(remaining, i)
		remaining[i]++
		if ok {
			return true
		}
	}
	return false
}

// TestCompletableMatchesBruteForce checks the closed-form rule against exhaustive
// search over every state a five-bowler attack can reach.
//
// The rule decides whether a bowler may be offered, so an error in it either
// strands a player with no legal move or forbids a choice that was fine. It is
// small enough to verify completely, so it is verified completely rather than
// argued about.
func TestCompletableMatchesBruteForce(t *testing.T) {
	const bowlers = 5
	remaining := make([]int, bowlers)

	var walk func(i int)
	checked, disagreed := 0, 0

	check := func(last int) {
		probe := append([]int(nil), remaining...)
		want := bruteForceCompletable(probe, last)
		got := completable(remaining, last)
		checked++
		if got != want {
			disagreed++
			if disagreed <= 10 {
				t.Errorf("completable(%v, last=%d) = %v, brute force says %v",
					remaining, last, got, want)
			}
		}
	}

	walk = func(i int) {
		if i == bowlers {
			for last := -1; last < bowlers; last++ {
				check(last)
			}
			return
		}
		for n := 0; n <= MaxOversPerBowler; n++ {
			remaining[i] = n
			walk(i + 1)
		}
		remaining[i] = 0
	}
	walk(0)

	t.Logf("checked %d states, %d disagreements", checked, disagreed)
}
