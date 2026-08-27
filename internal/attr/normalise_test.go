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
		{"Legbreak googly", BowlUnknown}, // arm unstated
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
		{"Off break", BowlUnknown},        // almost certainly right-arm, but unstated
		{"Leg break", BowlUnknown},        // likewise
		{"Right-arm bowler", BowlUnknown}, // arm known, type not
		{"Does not bowl", BowlUnknown},
		{"None", BowlUnknown},
		{"Slow", BowlUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := NormaliseBowl(tc.raw); got != tc.want {
				t.Errorf("NormaliseBowl(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
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
