package attr

import (
	"regexp"
	"strings"
)

// Normalisation of free-text style strings into the canonical taxonomy.
//
// Source strings are written by many hands and vary widely: "Right-arm off
// break", "Right arm offbreak" and "Right-arm off-spin" are the same thing.
// The rule throughout is that an unrecognised or ambiguous string yields
// Unknown rather than a guess. A gap is recoverable; a wrong value that looks
// sourced is not.

var nonAlpha = regexp.MustCompile(`[^a-z ]+`)

// tokens lowercases and strips punctuation so that hyphenated and spaced
// spellings collapse to the same form.
func tokens(s string) string {
	s = strings.ToLower(s)
	s = nonAlpha.ReplaceAllString(s, " ")
	return " " + strings.Join(strings.Fields(s), " ") + " "
}

func has(t string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(t, " "+sub+" ") || strings.Contains(t, " "+sub) {
			return true
		}
	}
	return false
}

// NormaliseHand maps a free-text batting style to a Hand.
func NormaliseHand(raw string) Hand {
	t := tokens(raw)
	if t == "  " {
		return HandUnknown
	}
	left := has(t, "left")
	right := has(t, "right")
	if left == right {
		// Both or neither: ambiguous, so refuse to decide.
		return HandUnknown
	}
	if left {
		return LeftHandBat
	}
	return RightHandBat
}

// NormaliseBowl maps a free-text bowling style to a BowlClass.
//
// Two independent judgements are needed: which arm, and finger spin, wrist spin
// or pace. If either is undetermined the result is Unknown, because a class is
// only useful to the matchup model when both halves are known.
func NormaliseBowl(raw string) BowlClass {
	t := tokens(raw)
	if strings.TrimSpace(t) == "" {
		return BowlUnknown
	}

	// A batter's entry sometimes reads "does not bowl" or similar.
	if has(t, "does not bowl", "none", "doesnt bowl", "non bowler") {
		return BowlUnknown
	}

	left := has(t, "left", "slow left arm", "leftarm")
	right := has(t, "right", "rightarm")

	// Spin family. Order matters: "left arm unorthodox" must be tested before
	// the generic orthodox and off-break rules.
	unorthodox := has(t, "unorthodox", "chinaman", "wrist spin", "wrist")
	orthodox := has(t, "orthodox")
	offSpin := has(t, "off break", "offbreak", "off spin", "offspin", "off cutter", "offbreaks")
	legSpin := has(t, "leg break", "legbreak", "leg spin", "legspin", "googly", "legbreaks", "leg spinner")
	pace := has(t, "fast", "medium", "pace", "seam", "swing", "quick")

	switch {
	case left && unorthodox && !pace:
		return LeftArmWrist
	case left && (orthodox || offSpin) && !pace:
		return LeftArmOrthodox
	case left && legSpin && !pace:
		// A left-armer bowling leg breaks is a wrist spinner.
		return LeftArmWrist
	case right && legSpin && !pace:
		return LegBreak
	case right && offSpin && !pace:
		return OffBreak
	case right && orthodox && !pace:
		// "Right-arm orthodox" is an unusual phrasing but means off spin.
		return OffBreak
	case left && pace:
		return LeftArmPace
	case right && pace:
		return RightArmPace
	}

	// Spin type known but arm unstated. Off breaks and leg breaks are
	// overwhelmingly right-arm, but "overwhelmingly" is not "certainly", and
	// this table exists precisely so the model is not fed assumptions.
	return BowlUnknown
}
