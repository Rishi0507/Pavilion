package attr

import (
	"regexp"
	"strings"
)

// Normalisation of free-text style strings into the canonical taxonomy.
//
// Source strings are written by many hands and vary widely: "Right-arm off
// break", "Right arm offbreak" and "Right-arm off-spin" are the same thing.
// The rule throughout is that an unrecognised or genuinely ambiguous string
// yields Unknown rather than a guess. A gap is recoverable; a wrong value that
// looks sourced is not.

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
		if strings.Contains(t, " "+sub) {
			return true
		}
	}
	return false
}

// NormaliseHand maps a free-text batting style to a Hand.
func NormaliseHand(raw string) Hand {
	t := tokens(raw)
	if strings.TrimSpace(t) == "" {
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
// Cricket's spin terminology already encodes which arm the ball comes from, so
// the arm does not have to be stated separately and frequently is not:
//
//   - "leg break" is right-arm wrist spin by definition. There is no such thing
//     as a left-arm leg break; a left-arm wrist spinner is called unorthodox,
//     or a chinaman.
//   - "off break" is right-arm finger spin. The left-arm equivalent is called
//     slow left-arm orthodox.
//
// Reading the arm off those terms is applying the vocabulary, not guessing, so
// such rows are sourced rather than sent for review. An explicitly stated arm
// still wins over the term's default, because the source knows more than the
// convention does.
//
// Pace is different: "fast-medium" says nothing about which arm, and no
// convention fills the gap, so an unstated arm leaves the class unknown.
func NormaliseBowl(raw string) BowlClass {
	t := tokens(raw)
	if strings.TrimSpace(t) == "" {
		return BowlUnknown
	}
	if has(t, "does not bowl", "doesnt bowl", "none", "non bowler", "no bowling") {
		return BowlUnknown
	}

	left := has(t, "left", "leftarm")
	right := has(t, "right", "rightarm")
	pace := has(t, "fast", "medium", "pace", "seam", "swing", "quick")

	// The four spin families. "unorthodox" is checked with a leading space so
	// it cannot also register as "orthodox".
	unorthodox := has(t, "unorthodox", "chinaman", "wrist")
	orthodox := has(t, "orthodox")
	offSpin := has(t, "off break", "offbreak", "off spin", "offspin", "offbreaks", "off cutter")
	legSpin := has(t, "leg break", "legbreak", "leg spin", "legspin", "legbreaks", "googly")

	// A source naming two different families describes a bowler who bowls
	// both. There is no single right answer, so it goes to review.
	families := 0
	for _, present := range []bool{unorthodox, orthodox, offSpin, legSpin} {
		if present {
			families++
		}
	}
	if families > 1 {
		return BowlUnknown
	}
	if families == 1 && pace {
		return BowlUnknown
	}

	if pace {
		switch {
		case left && !right:
			return LeftArmPace
		case right && !left:
			return RightArmPace
		}
		// Pace carries no convention about the arm.
		return BowlUnknown
	}

	switch {
	case unorthodox:
		// Wrist spin called unorthodox or chinaman is left-arm. A right-arm
		// wrist spinner is a leg spinner and is named as one, so an explicit
		// "right" here means exactly that.
		if right && !left {
			return LegBreak
		}
		return LeftArmWrist
	case legSpin:
		if left && !right {
			return LeftArmWrist
		}
		return LegBreak
	case offSpin:
		if left && !right {
			return LeftArmOrthodox
		}
		return OffBreak
	case orthodox:
		if right && !left {
			return OffBreak
		}
		return LeftArmOrthodox
	}

	// A stated arm with no stated type, or a bare "slow". Nothing to work with.
	return BowlUnknown
}
