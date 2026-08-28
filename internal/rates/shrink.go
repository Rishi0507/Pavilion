// Package rates turns raw per-player counts into rates a simulator can trust.
//
// The problem this solves is the difference between a game that feels fair and
// one that feels random. A bowler with forty recorded death-over deliveries and
// a bowler with four thousand cannot be rated on the same scale: the first
// player's observed rate is mostly noise, and feeding it to the simulator
// straight would make a nobody with one good over the best death bowler in the
// game.
//
// The fix is partial pooling. Every rate is a posterior mean under a
// Dirichlet-multinomial model whose prior is estimated from the population, so
// a player's estimate is pulled toward the population mean in proportion to how
// little is known about them. With no data at all the estimate is exactly the
// population mean; with a great deal it is almost exactly the observed rate.
package rates

import (
	"math"
)

// Shrink returns the posterior mean of a Dirichlet-multinomial with prior
// concentration kappa around prior.
//
//	theta_k = (x_k + kappa * prior_k) / (n + kappa)
//
// kappa is measured in the same units as the observation count, which makes it
// directly interpretable: it is the number of imaginary prior observations. A
// player with n == kappa sits exactly halfway between the population mean and
// their own record.
func Shrink(counts []int, prior []float64, kappa float64) []float64 {
	out := make([]float64, len(prior))
	n := 0
	for _, c := range counts {
		n += c
	}
	denom := float64(n) + kappa
	if denom == 0 {
		copy(out, prior)
		return out
	}
	for k := range prior {
		x := 0.0
		if k < len(counts) {
			x = float64(counts[k])
		}
		out[k] = (x + kappa*prior[k]) / denom
	}
	return out
}

// ShrinkageWeight reports the share of the posterior that comes from the
// player's own record, n / (n + kappa). It is reported alongside every rate so
// that a consumer can tell an observation from an assumption.
func ShrinkageWeight(n int, kappa float64) float64 {
	if float64(n)+kappa == 0 {
		return 0
	}
	return float64(n) / (float64(n) + kappa)
}

// Pool returns the population distribution over outcomes, summing every
// player's counts. It is the prior that individual players are shrunk toward.
func Pool(counts [][]int, k int) []float64 {
	total := make([]float64, k)
	n := 0.0
	for _, c := range counts {
		for j := 0; j < k && j < len(c); j++ {
			total[j] += float64(c[j])
			n += float64(c[j])
		}
	}
	if n == 0 {
		// A cell with no observations at all: fall back to uniform rather than
		// returning zeros, which would be an invalid distribution.
		for j := range total {
			total[j] = 1 / float64(k)
		}
		return total
	}
	for j := range total {
		total[j] /= n
	}
	return total
}

// logMarginal is the Dirichlet-multinomial log marginal likelihood of every
// player's counts under a prior of kappa * prior.
//
// Categories the population never produces are skipped: their prior mass is
// zero, so no player can have observed one, and the terms cancel.
func logMarginal(counts [][]int, prior []float64, kappa float64) float64 {
	if kappa <= 0 || math.IsInf(kappa, 0) || math.IsNaN(kappa) {
		return math.Inf(-1)
	}
	lgKappa, _ := math.Lgamma(kappa)

	// Precompute the per-category prior terms, which do not vary by player.
	alpha := make([]float64, len(prior))
	lgAlpha := make([]float64, len(prior))
	for k := range prior {
		alpha[k] = kappa * prior[k]
		if alpha[k] > 0 {
			lgAlpha[k], _ = math.Lgamma(alpha[k])
		}
	}

	total := 0.0
	for _, c := range counts {
		n := 0
		for _, x := range c {
			n += x
		}
		if n == 0 {
			continue
		}
		lgN, _ := math.Lgamma(float64(n) + kappa)
		ll := lgKappa - lgN
		for k := range prior {
			if alpha[k] <= 0 {
				continue
			}
			x := 0.0
			if k < len(c) {
				x = float64(c[k])
			}
			lg, _ := math.Lgamma(x + alpha[k])
			ll += lg - lgAlpha[k]
		}
		total += ll
	}
	return total
}

// Concentration bounds. The lower bound keeps the prior from vanishing on a
// cell where players genuinely differ; the upper bound stops a cell where they
// do not from shrinking everyone onto the population mean exactly.
const (
	minKappa = 1.0
	maxKappa = 1e6
)

// FitConcentration estimates the prior concentration by maximising the
// Dirichlet-multinomial marginal likelihood over the observed players.
//
// This is the empirical-Bayes step, and it is what makes the shrinkage adaptive
// rather than a tuned constant. In a cell where players really do differ, the
// likelihood prefers a small kappa and individual records dominate. In a cell
// where the observed spread is no wider than sampling noise, it prefers a large
// kappa and everyone is pulled hard toward the population mean.
//
// The search is golden-section over log kappa, which is unimodal here and needs
// no derivatives.
func FitConcentration(counts [][]int, prior []float64) float64 {
	// A cell needs at least two players with data before "how much do players
	// differ" is even a question.
	withData := 0
	for _, c := range counts {
		n := 0
		for _, x := range c {
			n += x
		}
		if n > 0 {
			withData++
		}
	}
	if withData < 2 {
		return maxKappa
	}

	lo, hi := math.Log(minKappa), math.Log(maxKappa)
	const phi = 0.6180339887498949 // 1/golden ratio

	f := func(logK float64) float64 { return logMarginal(counts, prior, math.Exp(logK)) }

	c := hi - phi*(hi-lo)
	d := lo + phi*(hi-lo)
	fc, fd := f(c), f(d)

	// Around sixty iterations narrows the bracket well past the precision that
	// matters for a shrinkage weight.
	for range 60 {
		if fc > fd {
			hi, d, fd = d, c, fc
			c = hi - phi*(hi-lo)
			fc = f(c)
		} else {
			lo, c, fc = c, d, fd
			d = lo + phi*(hi-lo)
			fd = f(d)
		}
		if hi-lo < 1e-6 {
			break
		}
	}
	return math.Exp((lo + hi) / 2)
}

// FitAndShrink estimates the concentration for a cell and returns each player's
// posterior mean, the population prior, and the fitted concentration.
func FitAndShrink(counts [][]int, k int) (posterior [][]float64, prior []float64, kappa float64) {
	prior = Pool(counts, k)
	kappa = FitConcentration(counts, prior)
	posterior = make([][]float64, len(counts))
	for i, c := range counts {
		posterior[i] = Shrink(c, prior, kappa)
	}
	return posterior, prior, kappa
}
