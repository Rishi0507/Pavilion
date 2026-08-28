package rates

import (
	"math"
	"math/rand/v2"
	"testing"
)

func sum(v []float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t
}

func TestShrinkEndpoints(t *testing.T) {
	prior := []float64{0.5, 0.3, 0.2}

	t.Run("no data yields exactly the prior", func(t *testing.T) {
		got := Shrink([]int{0, 0, 0}, prior, 100)
		for k := range prior {
			if math.Abs(got[k]-prior[k]) > 1e-12 {
				t.Errorf("category %d: got %.6f, want the prior %.6f", k, got[k], prior[k])
			}
		}
	})

	t.Run("overwhelming data yields almost exactly the observation", func(t *testing.T) {
		counts := []int{900_000, 50_000, 50_000}
		got := Shrink(counts, prior, 100)
		want := []float64{0.9, 0.05, 0.05}
		for k := range want {
			if math.Abs(got[k]-want[k]) > 1e-3 {
				t.Errorf("category %d: got %.6f, want about %.6f", k, got[k], want[k])
			}
		}
	})

	t.Run("a posterior is always a distribution", func(t *testing.T) {
		for _, counts := range [][]int{{0, 0, 0}, {1, 0, 0}, {5, 5, 5}, {1000, 1, 0}} {
			got := Shrink(counts, prior, 50)
			if math.Abs(sum(got)-1) > 1e-12 {
				t.Errorf("counts %v: posterior sums to %.12f, want 1", counts, sum(got))
			}
			for k, v := range got {
				if v < 0 || v > 1 {
					t.Errorf("counts %v: category %d is %.6f, outside [0,1]", counts, k, v)
				}
			}
		}
	})
}

// TestShrinkIsMonotoneInSampleSize is the property the whole package exists
// for: the more a player has been observed, the closer their rate sits to what
// they actually did, and the less it borrows from the population.
func TestShrinkIsMonotoneInSampleSize(t *testing.T) {
	prior := []float64{0.5, 0.5}
	const kappa = 100

	// A player who has done nothing but category 0, at growing sample sizes.
	prev := prior[0]
	for _, n := range []int{1, 10, 50, 100, 500, 5000, 100_000} {
		got := Shrink([]int{n, 0}, prior, kappa)[0]
		if got <= prev {
			t.Errorf("n=%d: posterior %.6f did not move further from the prior than %.6f", n, got, prev)
		}
		if got > 1 {
			t.Fatalf("n=%d: posterior %.6f exceeds 1", n, got)
		}
		prev = got
	}
	if prev < 0.99 {
		t.Errorf("with 100k observations the posterior is still only %.4f", prev)
	}
}

func TestShrinkageWeight(t *testing.T) {
	tests := []struct {
		n     int
		kappa float64
		want  float64
	}{
		{0, 100, 0},      // nothing known: entirely prior
		{100, 100, 0.5},  // n == kappa: exactly halfway
		{300, 100, 0.75}, // three times the prior weight
		{0, 0, 0},        // degenerate, must not divide by zero
	}
	for _, tc := range tests {
		if got := ShrinkageWeight(tc.n, tc.kappa); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("ShrinkageWeight(%d, %.0f) = %.6f, want %.6f", tc.n, tc.kappa, got, tc.want)
		}
	}
}

func TestPool(t *testing.T) {
	counts := [][]int{{10, 0}, {0, 10}, {5, 5}}
	got := Pool(counts, 2)
	if math.Abs(got[0]-0.5) > 1e-12 || math.Abs(got[1]-0.5) > 1e-12 {
		t.Errorf("Pool = %v, want [0.5 0.5]", got)
	}

	t.Run("an empty cell falls back to uniform", func(t *testing.T) {
		got := Pool(nil, 4)
		for k, v := range got {
			if math.Abs(v-0.25) > 1e-12 {
				t.Errorf("category %d = %.6f, want 0.25", k, v)
			}
		}
	})
}

// gammaSample draws from Gamma(shape, 1) by Marsaglia and Tsang's method,
// which the Dirichlet sampler below needs.
func gammaSample(r *rand.Rand, shape float64) float64 {
	if shape < 1 {
		return gammaSample(r, shape+1) * math.Pow(r.Float64(), 1/shape)
	}
	d := shape - 1.0/3.0
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if u < 1-0.0331*x*x*x*x {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

func dirichletSample(r *rand.Rand, alpha []float64) []float64 {
	out := make([]float64, len(alpha))
	total := 0.0
	for k, a := range alpha {
		out[k] = gammaSample(r, a)
		total += out[k]
	}
	for k := range out {
		out[k] /= total
	}
	return out
}

func multinomialSample(r *rand.Rand, n int, p []float64) []int {
	out := make([]int, len(p))
	for range n {
		u := r.Float64()
		acc := 0.0
		for k, pk := range p {
			acc += pk
			if u <= acc {
				out[k]++
				break
			}
		}
	}
	return out
}

// TestFitConcentrationRecoversTruth generates data from a known
// Dirichlet-multinomial and checks that the estimator finds the concentration
// it was generated with. This is the test that makes the shrinkage defensible:
// without it, kappa is just a number that happens to look reasonable.
func TestFitConcentrationRecoversTruth(t *testing.T) {
	prior := []float64{0.55, 0.25, 0.15, 0.05}

	for _, trueKappa := range []float64{20, 200, 2000} {
		t.Run("kappa="+itoa(trueKappa), func(t *testing.T) {
			r := rand.New(rand.NewPCG(0x5eed, uint64(trueKappa)))

			alpha := make([]float64, len(prior))
			for k := range prior {
				alpha[k] = trueKappa * prior[k]
			}

			// 400 players of 600 balls each: comparable to a real cell.
			counts := make([][]int, 400)
			for i := range counts {
				counts[i] = multinomialSample(r, 600, dirichletSample(r, alpha))
			}

			got := FitConcentration(counts, Pool(counts, len(prior)))

			// Concentration is only weakly identified, so the bar is that the
			// estimate lands within a factor of two, which is more than enough
			// to get the shrinkage weight right.
			ratio := got / trueKappa
			if ratio < 0.5 || ratio > 2.0 {
				t.Errorf("fitted kappa %.1f, want within a factor of 2 of %.1f", got, trueKappa)
			}
		})
	}
}

func itoa(f float64) string {
	switch {
	case f < 100:
		return "20"
	case f < 1000:
		return "200"
	default:
		return "2000"
	}
}

// TestFitConcentrationRespondsToSpread checks the estimator's whole purpose:
// when players genuinely differ it must trust them, and when their apparent
// differences are only sampling noise it must not.
func TestFitConcentrationRespondsToSpread(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	// Identical players: every apparent difference is noise, so the estimator
	// should pool hard.
	same := make([][]int, 300)
	for i := range same {
		same[i] = multinomialSample(r, 400, []float64{0.5, 0.5})
	}
	kSame := FitConcentration(same, Pool(same, 2))

	// Genuinely different players: half are good, half are bad.
	diff := make([][]int, 300)
	for i := range diff {
		p := []float64{0.2, 0.8}
		if i%2 == 0 {
			p = []float64{0.8, 0.2}
		}
		diff[i] = multinomialSample(r, 400, p)
	}
	kDiff := FitConcentration(diff, Pool(diff, 2))

	if kSame <= kDiff {
		t.Errorf("homogeneous players fitted kappa %.1f, heterogeneous %.1f; "+
			"the homogeneous cell must pool harder", kSame, kDiff)
	}
	if kDiff > 100 {
		t.Errorf("heterogeneous players fitted kappa %.1f, expected strong trust in individual records", kDiff)
	}

	// With a hard-pooling cell, a 400-ball player should still be pulled most
	// of the way to the population mean.
	if w := ShrinkageWeight(400, kSame); w > 0.5 {
		t.Errorf("shrinkage weight %.2f in a noise-only cell; expected the population to dominate", w)
	}
}

func TestFitConcentrationHandlesDegenerateCells(t *testing.T) {
	prior := []float64{0.5, 0.5}

	t.Run("no players", func(t *testing.T) {
		if got := FitConcentration(nil, prior); got != maxKappa {
			t.Errorf("kappa = %.1f, want the maximum %.1f", got, maxKappa)
		}
	})
	t.Run("one player cannot reveal between-player spread", func(t *testing.T) {
		if got := FitConcentration([][]int{{10, 5}}, prior); got != maxKappa {
			t.Errorf("kappa = %.1f, want the maximum %.1f", got, maxKappa)
		}
	})
	t.Run("players with no observations are ignored", func(t *testing.T) {
		if got := FitConcentration([][]int{{0, 0}, {0, 0}}, prior); got != maxKappa {
			t.Errorf("kappa = %.1f, want the maximum %.1f", got, maxKappa)
		}
	})
}

// TestZeroPriorCategoriesAreSkipped guards a numerical trap: a category the
// population never produces has zero prior mass, and taking log Gamma of zero
// would poison the whole likelihood.
func TestZeroPriorCategoriesAreSkipped(t *testing.T) {
	prior := []float64{0.6, 0.4, 0.0}
	counts := [][]int{{6, 4, 0}, {3, 7, 0}, {5, 5, 0}}

	ll := logMarginal(counts, prior, 100)
	if math.IsNaN(ll) || math.IsInf(ll, 0) {
		t.Fatalf("log marginal is %v with a zero-probability category", ll)
	}
	k := FitConcentration(counts, prior)
	if math.IsNaN(k) || k <= 0 {
		t.Fatalf("fitted kappa is %v", k)
	}
	post := Shrink(counts[0], prior, k)
	if post[2] != 0 {
		t.Errorf("a category with no prior mass and no observations became %.6f", post[2])
	}
	if math.Abs(sum(post)-1) > 1e-12 {
		t.Errorf("posterior sums to %.12f", sum(post))
	}
}

func TestFitAndShrink(t *testing.T) {
	counts := [][]int{
		{80, 20},
		{20, 80},
		{50, 50},
	}
	post, prior, kappa := FitAndShrink(counts, 2)
	if len(post) != 3 {
		t.Fatalf("got %d posteriors, want 3", len(post))
	}
	if math.Abs(sum(prior)-1) > 1e-12 {
		t.Errorf("prior sums to %.12f", sum(prior))
	}
	if kappa < minKappa || kappa > maxKappa {
		t.Errorf("kappa %.2f outside its bounds", kappa)
	}
	for i, p := range post {
		if math.Abs(sum(p)-1) > 1e-12 {
			t.Errorf("player %d posterior sums to %.12f", i, sum(p))
		}
	}
	// The extreme players must stay on their side of the population mean.
	if post[0][0] <= prior[0] {
		t.Error("the player who mostly produced category 0 was shrunk past the mean")
	}
	if post[1][0] >= prior[0] {
		t.Error("the player who mostly produced category 1 was shrunk past the mean")
	}
}
