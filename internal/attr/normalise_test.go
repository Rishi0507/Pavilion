package attr

import "testing"

func TestNormaliseHand(t *testing.T) {
	tests := []struct {
		raw  string
		want Hand
	}{
		{"Right-handed", RightHandBat},
		{"Right-hand bat", RightHandBat},
		{"Right handed batsman", RightHandBat},
		{"Left-handed", LeftHandBat},
		{"Left-hand bat", LeftHandBat},
		{"Left handed", LeftHandBat},
		{"", HandUnknown},
		{"   ", HandUnknown},
		{"Unknown", HandUnknown},
		// Ambiguous: mentions both, so it must refuse rather than pick one.
		{"Right-handed (bats left-handed in T20)", HandUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := NormaliseHand(tc.raw); got != tc.want {
				t.Errorf("NormaliseHand(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormaliseBowl(t *testing.T) {
	tests := []struct {
		raw  string
		want BowlClass
	}{
		// Pace, in the spellings the sources actually use.
		{"Right-arm fast", RightArmPace},
		{"Right arm fast-medium", RightArmPace},
		{"Right-arm medium", RightArmPace},
		{"Right-arm medium-fast", RightArmPace},
		{"Right arm seam", RightArmPace},
		{"Left-arm fast", LeftArmPace},
		{"Left-arm fast medium", LeftArmPace},
		{"Left arm medium-fast", LeftArmPace},
		{"Left-arm swing", LeftArmPace},

		// Right-arm finger spin.
		{"Right-arm off break", OffBreak},
		{"Right arm off break", OffBreak},
		{"Right-arm offbreak", OffBreak},
		{"Right-arm off spin", OffBreak},
		{"Right-arm off-spin", OffBreak},
		{"Right arm offspin", OffBreak},

		// Right-arm wrist spin.
		{"Right-arm leg break", LegBreak},
		{"Right-arm legbreak", LegBreak},
		{"Right-arm leg spin", LegBreak},
		{"Right-arm legbreak googly", LegBreak},

		// Left-arm finger spin.
		{"Slow left-arm orthodox", LeftArmOrthodox},
		{"Slow left arm orthodox", LeftArmOrthodox},
		{"Left-arm orthodox", LeftArmOrthodox},
		{"Left arm orthodox spin", LeftArmOrthodox},

		// Left-arm wrist spin.
		{"Slow left-arm unorthodox", LeftArmWrist},
		{"Left-arm unorthodox spin", LeftArmWrist},
		{"Left-arm chinaman", LeftArmWrist},
		{"Left-arm wrist spin", LeftArmWrist},

		// Refusals. Each of these is a case where a plausible guess exists and
		// is deliberately not made.
		{"", BowlUnknown},
		{"Right-arm bowler", BowlUnknown}, // arm known, type not
		{"Does not bowl", BowlUnknown},
		{"None", BowlUnknown},
		{"Slow", BowlUnknown},
		{"Fast bowler", BowlUnknown},                 // pace carries no convention about the arm
		{"Right-arm medium, off break", BowlUnknown}, // two families
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := NormaliseBowl(tc.raw); got != tc.want {
				t.Errorf("NormaliseBowl(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestArmIsReadFromSpinTerminology pins the vocabulary rules.
//
// Cricket's spin terms already name the arm, so reading it off them is applying
// the terminology rather than guessing. "Leg break" is right-arm wrist spin by
// definition; there is no left-arm leg break, because that delivery is called
// unorthodox or a chinaman. "Off break" is right-arm finger spin, whose
// left-arm equivalent is slow left-arm orthodox.
func TestArmIsReadFromSpinTerminology(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want BowlClass
	}{
		// Leg break family: right-arm wrist spin, arm unstated.
		{"bare leg break", "Leg break", LegBreak},
		{"legbreak one word", "Legbreak", LegBreak},
		{"legbreak googly", "Legbreak googly", LegBreak},
		{"leg break googly spaced", "Leg break googly", LegBreak},
		{"leg spin", "Leg spin", LegBreak},

		// Off break family: right-arm finger spin, arm unstated.
		{"bare off break", "Off break", OffBreak},
		{"offbreak one word", "Offbreak", OffBreak},
		{"off spin", "Off spin", OffBreak},
		{"off-break hyphenated", "Off-break", OffBreak},

		// Left-arm finger spin.
		{"slow left-arm orthodox", "Slow left-arm orthodox", LeftArmOrthodox},
		{"bare orthodox", "Orthodox", LeftArmOrthodox},

		// Left-arm wrist spin, in each of its names.
		{"left-arm unorthodox", "Left-arm unorthodox", LeftArmWrist},
		{"slow left-arm wrist-spin", "Slow left-arm wrist-spin", LeftArmWrist},
		{"bare chinaman", "Chinaman", LeftArmWrist},
		{"unorthodox alone", "Unorthodox spin", LeftArmWrist},

		// An explicitly stated arm overrides the term's default, because the
		// source knows more than the convention does.
		{"left-arm leg break is wrist spin", "Left-arm leg break", LeftArmWrist},
		{"left-arm off break is orthodox", "Left-arm off break", LeftArmOrthodox},
		{"right-arm wrist spin is a leg break", "Right-arm wrist spin", LegBreak},

		// The specific rows this rule was written to resolve.
		{"Rashid Khan", "Leg break googly", LegBreak},
		{"Anil Kumble", "Leg break", LegBreak},
		{"Rahul Sharma", "Legbreak googly", LegBreak},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormaliseBowl(tc.raw); got != tc.want {
				t.Errorf("NormaliseBowl(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestTwoFamiliesStayAmbiguous checks that a source naming two different spin
// types is not silently collapsed to one of them.
func TestTwoFamiliesStayAmbiguous(t *testing.T) {
	for _, raw := range []string{
		"Right-arm off break, leg break",
		"Leg break, Off-break",
		"Slow Left arm Orthodox, Left arm Wrist spin",
	} {
		if got := NormaliseBowl(raw); got != BowlUnknown {
			t.Errorf("NormaliseBowl(%q) = %v, want BowlUnknown", raw, got)
		}
	}
}

// TestBowlClassFamilies pins the pace/spin split that the matchup model and the
// inference heuristic both depend on.
func TestBowlClassFamilies(t *testing.T) {
	spin := []BowlClass{OffBreak, LegBreak, LeftArmOrthodox, LeftArmWrist}
	pace := []BowlClass{RightArmPace, LeftArmPace}

	for _, c := range spin {
		if !c.IsSpin() || c.IsPace() {
			t.Errorf("%v: want spin", c)
		}
	}
	for _, c := range pace {
		if !c.IsPace() || c.IsSpin() {
			t.Errorf("%v: want pace", c)
		}
	}
	if BowlUnknown.IsSpin() || BowlUnknown.IsPace() {
		t.Error("BowlUnknown must be neither pace nor spin")
	}
}

func TestCodeRoundTrip(t *testing.T) {
	for _, c := range []BowlClass{RightArmPace, LeftArmPace, OffBreak, LegBreak, LeftArmOrthodox, LeftArmWrist} {
		if got := ParseBowlClass(c.String()); got != c {
			t.Errorf("ParseBowlClass(%q) = %v, want %v", c.String(), got, c)
		}
	}
	for _, h := range []Hand{RightHandBat, LeftHandBat} {
		if got := ParseHand(h.String()); got != h {
			t.Errorf("ParseHand(%q) = %v, want %v", h.String(), got, h)
		}
	}
	if ParseBowlClass("nonsense") != BowlUnknown {
		t.Error("ParseBowlClass: unknown code must yield BowlUnknown")
	}
	if ParseHand("") != HandUnknown {
		t.Error("ParseHand: empty must yield HandUnknown")
	}
}

// TestMovesAway pins the geometry of every matchup. Getting one of these
// backwards would invert the most informative feature the outcome model has.
func TestMovesAway(t *testing.T) {
	tests := []struct {
		name string
		bowl BowlClass
		hand Hand
		want bool
	}{
		// Against a right-hander.
		{"off break into the right-hander", OffBreak, RightHandBat, false},
		{"leg break away from the right-hander", LegBreak, RightHandBat, true},
		{"left-arm orthodox away from the right-hander", LeftArmOrthodox, RightHandBat, true},
		{"left-arm wrist into the right-hander", LeftArmWrist, RightHandBat, false},
		{"right-arm pace into the right-hander", RightArmPace, RightHandBat, false},
		{"left-arm pace across the right-hander", LeftArmPace, RightHandBat, true},

		// Against a left-hander, every one of those inverts.
		{"off break away from the left-hander", OffBreak, LeftHandBat, true},
		{"leg break into the left-hander", LegBreak, LeftHandBat, false},
		{"left-arm orthodox into the left-hander", LeftArmOrthodox, LeftHandBat, false},
		{"left-arm wrist away from the left-hander", LeftArmWrist, LeftHandBat, true},
		{"right-arm pace across the left-hander", RightArmPace, LeftHandBat, true},
		{"left-arm pace into the left-hander", LeftArmPace, LeftHandBat, false},

		// Unknown handedness cannot have a geometry.
		{"unknown hand", OffBreak, HandUnknown, false},
		{"unknown class", BowlUnknown, RightHandBat, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MovesAway(tc.bowl, tc.hand); got != tc.want {
				t.Errorf("MovesAway(%v, %v) = %v, want %v", tc.bowl, tc.hand, got, tc.want)
			}
		})
	}

	// Every class moves away from exactly one of the two handednesses.
	for _, b := range []BowlClass{RightArmPace, LeftArmPace, OffBreak, LegBreak, LeftArmOrthodox, LeftArmWrist} {
		if MovesAway(b, RightHandBat) == MovesAway(b, LeftHandBat) {
			t.Errorf("%v moves the same way against both hands", b)
		}
	}
}
