package engine

import (
	"math"

	"manhattan/internal/corpus"
	"manhattan/internal/features"
	"manhattan/internal/rates"
	"manhattan/internal/sim"
)

// Restoring bowler quality that the outcome model smooths away.
//
// The outcome model is trained to predict one delivery, and on that task the
// situation dominates: the over, the required rate and how long the striker has
// been in explain far more than who is bowling. It is right about that, and it
// is calibrated. But it compresses the differences between bowlers to about a
// run an over in every phase, where the shrunk rate table measures 1.0 in the
// powerplay, 1.7 through the middle and 1.8 at the death.
//
// That compression would be a detail in a forecasting tool and is fatal in this
// game, because the whole decision is which bowler bowls next. Worse, a
// constant spread across phases means the best bowler is best everywhere, so
// there is nothing to weigh: no reason to save anyone for the death.
//
// So the engine restores it. The model supplies the shape of the distribution
// and how the situation moves it; the rate table, which is measured and shrunk
// and already carries its own uncertainty, supplies how good this bowler is in
// this phase against this hand. The two are combined by tilting the model's
// distribution until its expected runs match what the rates say, which changes
// the level without disturbing the situational response or the calibration of
// the relative outcome shape.

// runValueOf is the runs each outcome adds to the score.
var runValueOf = [corpus.NumOutcomes]float64{
	corpus.Dot: 0, corpus.One: 1, corpus.Two: 2, corpus.Three: 3,
	corpus.Four: 4, corpus.Six: 6, corpus.Wicket: 0, corpus.Wide: 1, corpus.NoBall: 1,
}

func expectedRunsOf(p []float64) float64 {
	total := 0.0
	for k := range corpus.NumOutcomes {
		total += p[k] * runValueOf[k]
	}
	return total
}

// bowlerScale returns how much this bowler's economy differs from the
// population's, in this phase and against this hand. A value below one means a
// bowler who goes for fewer runs than the average.
func (e *Engine) bowlerScale(s features.State) float64 {
	t := e.ctx.Rates()
	if t == nil || int(s.Bowler) >= len(t.Players) {
		return 1
	}
	phase := corpus.PhaseOf(s.Over)
	opp := rates.VsRightHand
	if s.BatterHand == 2 { // attr.LeftHandBat
		opp = rates.VsLeftHand
	}
	cell := rates.CellIndex(rates.Bowling, phase, opp)

	cr := t.Players[s.Bowler].Cells[cell]
	st := t.Cells[cell]
	if cr.Deliveries == 0 {
		return 1
	}

	popRuns := 0.0
	for k := range corpus.NumOutcomes {
		popRuns += st.Prior[k] * st.MeanRuns[k]
	}
	bowlRuns := cr.ExpectedRuns(st)
	if popRuns <= 0 {
		return 1
	}
	return bowlRuns / popRuns
}

// maxTilt bounds the correction so a thin sample cannot produce an absurd over.
const maxTilt = 0.8

// applyQuality tilts a predicted distribution so its expected runs match what
// the rate table says this bowler concedes, relative to the population, while
// leaving the situational level the model chose intact.
func (e *Engine) applyQuality(s features.State, p []float64) {
	scale := e.bowlerScale(s)
	if scale <= 0 || math.Abs(scale-1) < 1e-6 {
		return
	}

	want := expectedRunsOf(p) * scale
	if want <= 0 {
		return
	}
	newtonTo(p, want)
}

// newtonTo tilts a distribution in place until its expected runs reach want.
func newtonTo(p []float64, want float64) {
	if want <= 0 {
		return
	}

	// Expected runs rise monotonically with the tilt, and the derivative of the
	// tilted mean is exactly the tilted variance, so Newton's method converges
	// in a handful of steps where a bisection needed two dozen. This is on the
	// hot path: puzzle validation simulates millions of deliveries.
	tilted := make([]float64, corpus.NumOutcomes)
	lambda := 0.0
	for range 6 {
		sim.Tilt(p, runValueOf[:], lambda, tilted)
		mean := expectedRunsOf(tilted)
		diff := mean - want
		if math.Abs(diff) < 1e-6 {
			break
		}
		variance := 0.0
		for k := range corpus.NumOutcomes {
			d := runValueOf[k] - mean
			variance += tilted[k] * d * d
		}
		if variance < 1e-9 {
			break
		}
		lambda -= diff / variance
		lambda = min(max(lambda, -maxTilt), maxTilt)
	}
	sim.Tilt(p, runValueOf[:], lambda, tilted)
	copy(p, tilted)
}
