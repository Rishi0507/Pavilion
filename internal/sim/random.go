// Package sim is the match engine: a pure function from state and decision to
// new state, with no database, no clock, no logger and no network.
//
// The one thing this package must get right is randomness, and it is the most
// consequential architectural decision in the project. Two players who make the
// same decisions must see the same match, and the luck met in the seventeenth
// over must not depend on what was done in the fifth. Otherwise "unlucky" and
// "wrong" become indistinguishable and comparing scores means nothing.
//
// So every draw is derived statelessly from the ball's coordinates, never from
// a sequential stream. There is no generator threaded through the innings whose
// position depends on how many balls have been bowled; there is a keyed
// function from (innings, over, delivery) to a number.
package sim

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
)

// DailyKey is the per-day secret every draw in that day's puzzle derives from.
//
// It must not be derivable client-side before release: if it were, the outcome
// of every ball could be computed ahead of the decision that is supposed to
// determine it.
type DailyKey [32]byte

// DeriveDailyKey produces the key for one date from the master secret.
//
// The date is the label rather than the salt so that the same master secret
// yields an independent key per day, and knowing one day's key reveals nothing
// about any other.
func DeriveDailyKey(masterSecret []byte, dateIST string) (DailyKey, error) {
	var k DailyKey
	if len(masterSecret) == 0 {
		return k, fmt.Errorf("sim: master secret is empty")
	}
	if dateIST == "" {
		return k, fmt.Errorf("sim: date is empty")
	}
	out, err := hkdf.Key(sha256.New, masterSecret, nil, "par-daily/"+dateIST, 32)
	if err != nil {
		return k, fmt.Errorf("sim: derive daily key: %w", err)
	}
	copy(k[:], out)
	return k, nil
}

// Coord identifies one delivery within a run.
//
// Delivery counts every ball bowled in the over including wides and no-balls,
// not just legal ones. Using the legal-ball index would give two different
// deliveries the same coordinate whenever an over contained an extra, and they
// would then share a draw.
type Coord struct {
	Innings  uint8
	Over     uint8
	Delivery uint8
}

// Draw returns the uniform in [0,1) for one delivery.
//
// Exactly one draw is consumed per delivery, and it is mapped through an
// inverse CDF whose shape depends on the decision taken. That is what makes the
// guarantee hold: the number is fixed by the coordinate, and the decision
// changes only what the number means. Drawing twice for a ball, or drawing a
// variable number of times, would desynchronise the stream across decision
// paths and destroy the property.
func Draw(key DailyKey, c Coord) float64 {
	var seed [32]byte
	copy(seed[:], key[:])

	// The coordinate is mixed into the tail of the seed. The first 28 bytes
	// stay the day's key, so two coordinates differ in the seed regardless of
	// how similar the situations are.
	seed[28] ^= c.Innings
	seed[29] ^= c.Over
	seed[30] ^= c.Delivery
	seed[31] ^= c.Innings*37 + c.Over*11 + c.Delivery

	r := rand.NewChaCha8(seed)
	// Take 53 bits, the width of a float64 mantissa, so the mapping onto [0,1)
	// is uniform without rounding artefacts.
	return float64(binary.LittleEndian.Uint64(chaChaBytes(r))>>11) / (1 << 53)
}

func chaChaBytes(r *rand.ChaCha8) []byte {
	var b [8]byte
	r.Read(b[:])
	return b[:]
}

// Sample maps a uniform onto an outcome by inverse CDF.
//
// The probabilities come from the calibrated outcome model, tilted by whatever
// the batting side is trying to do. Because the uniform is fixed by the ball's
// coordinate, changing the bowler changes the shape of this CDF but not the
// number being looked up, which is exactly the property the game needs.
func Sample(p []float64, u float64) int {
	acc := 0.0
	for i := range p {
		acc += p[i]
		if u < acc {
			return i
		}
	}
	// Floating-point error can leave the last sliver unreachable. Fall back to
	// the last outcome with any mass rather than returning an invalid index.
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] > 0 {
			return i
		}
	}
	return 0
}

// Tilt reweights an outcome distribution by an exponential factor on each
// outcome's aggression value, and renormalises.
//
//	p'_k  proportional to  p_k * exp(lambda * value_k)
//
// This is how batting intent enters the game. The outcome model is trained on
// what happened, not on what was attempted, because no ball-by-ball source
// records intent; a batter choosing to attack is expressed here instead, as a
// deliberate shift of mass toward boundaries and, unavoidably, toward getting
// out.
//
// It is an exponential tilt rather than an ad-hoc reshuffle because tilting
// preserves the support and the ordering within the distribution: an outcome
// the model thought impossible stays impossible, and a batter who is good at
// hitting sixes gets more of them when attacking than one who is not.
func Tilt(p []float64, value []float64, lambda float64, dst []float64) {
	if lambda == 0 {
		copy(dst, p)
		return
	}
	total := 0.0
	for k := range p {
		dst[k] = p[k] * math.Exp(lambda*value[k])
		total += dst[k]
	}
	if total <= 0 {
		copy(dst, p)
		return
	}
	for k := range dst {
		dst[k] /= total
	}
}
