package engine

import (
	"math"
	"testing"

	"manhattan/internal/corpus"
	"manhattan/internal/sim"
)

// TestApplyQualityHitsItsTarget checks the Newton solve against a slow but
// obviously correct bisection. The fast path is on the hot loop, so it needs a
// reference implementation to be trusted rather than assumed.
func TestApplyQualityHitsItsTarget(t *testing.T) {
	base := []float64{0.32, 0.36, 0.06, 0.004, 0.11, 0.05, 0.05, 0.04, 0.006}
	sum := 0.0
	for _, v := range base {
		sum += v
	}
	for i := range base {
		base[i] /= sum
	}

	bisect := func(p []float64, want float64) float64 {
		tilted := make([]float64, corpus.NumOutcomes)
		lo, hi := -maxTilt, maxTilt
		for range 200 {
			mid := (lo + hi) / 2
			sim.Tilt(p, runValueOf[:], mid, tilted)
			if expectedRunsOf(tilted) < want {
				lo = mid
			} else {
				hi = mid
			}
		}
		return (lo + hi) / 2
	}

	for _, scale := range []float64{0.75, 0.9, 1.0, 1.1, 1.35} {
		want := expectedRunsOf(base) * scale

		got := append([]float64(nil), base...)
		newtonTo(got, want)

		ref := append([]float64(nil), base...)
		tilted := make([]float64, corpus.NumOutcomes)
		sim.Tilt(ref, runValueOf[:], bisect(ref, want), tilted)

		for k := range corpus.NumOutcomes {
			if math.Abs(got[k]-tilted[k]) > 1e-6 {
				t.Errorf("scale %.2f category %d: Newton %.9f, bisection %.9f", scale, k, got[k], tilted[k])
			}
		}
		if e := expectedRunsOf(got); math.Abs(e-want) > 1e-4 {
			t.Errorf("scale %.2f: expected runs %.6f, wanted %.6f", scale, e, want)
		}
		total := 0.0
		for _, v := range got {
			total += v
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("scale %.2f: distribution sums to %.12f", scale, total)
		}
	}
}

// TestApplyQualityIsBounded guards the case where the rate table asks for
// something the model's distribution cannot reach.
func TestApplyQualityIsBounded(t *testing.T) {
	base := []float64{corpus.Dot: 0.99, corpus.Six: 0.01}
	for _, want := range []float64{0, 100, -5} {
		got := append([]float64(nil), base...)
		newtonTo(got, want)
		total := 0.0
		for k, v := range got {
			if v < 0 || v > 1 || math.IsNaN(v) {
				t.Fatalf("want %.1f category %d = %v", want, k, v)
			}
			total += v
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("want %.1f: sums to %.12f", want, total)
		}
	}
}
