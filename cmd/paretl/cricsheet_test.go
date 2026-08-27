package main

import (
	"encoding/json"
	"testing"
)

func TestLegalBallOf(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		over, ball int
		wantErr    bool
	}{
		{"first ball", "0.1", 0, 0, false},
		{"last ball of over", "0.6", 0, 5, false},
		{"death over", "19.4", 19, 3, false},
		{"two digit over", "17.1", 17, 0, false},
		{"missing separator", "104", 0, 0, true},
		{"non-numeric over", "a.1", 0, 0, true},
		{"non-numeric ball", "1.x", 0, 0, true},
		{"empty", "", 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			over, ball, err := legalBallOf(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("legalBallOf(%q) = (%d, %d, nil), want error", tc.in, over, ball)
				}
				return
			}
			if err != nil {
				t.Fatalf("legalBallOf(%q): %v", tc.in, err)
			}
			if over != tc.over || ball != tc.ball {
				t.Errorf("legalBallOf(%q) = (%d, %d), want (%d, %d)", tc.in, over, ball, tc.over, tc.ball)
			}
		})
	}
}

// TestEditionYear pins the season-normalisation rule. Cricsheet labels IPL 2008
// as "2007/08" and IPL 2020 as "2020/21", so neither half of the label is
// reliably the edition. Taking the year from the match date is.
func TestEditionYear(t *testing.T) {
	tests := []struct {
		name   string
		season string
		dates  []string
		want   uint16
	}{
		{"split label, later year correct", `"2007/08"`, []string{"2008-04-18"}, 2008},
		{"split label, later year wrong", `"2020/21"`, []string{"2020-09-19"}, 2020},
		{"split label 2009/10", `"2009/10"`, []string{"2010-03-12"}, 2010},
		{"plain string label", `"2013"`, []string{"2013-04-03"}, 2013},
		{"integer label", `2026`, []string{"2026-03-20"}, 2026},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := csInfo{Season: json.RawMessage(tc.season), Dates: tc.dates}
			got, err := i.editionYear()
			if err != nil {
				t.Fatalf("editionYear: %v", err)
			}
			if got != tc.want {
				t.Errorf("editionYear() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("no dates is an error", func(t *testing.T) {
		i := csInfo{Season: json.RawMessage(`"2013"`)}
		if _, err := i.editionYear(); err == nil {
			t.Fatal("editionYear() with no dates: want error")
		}
	})
}

func TestDateInt(t *testing.T) {
	tests := []struct {
		name    string
		dates   []string
		want    int32
		wantErr bool
	}{
		{"ordinary", []string{"2026-05-31"}, 20260531, false},
		{"single digit month and day are zero padded", []string{"2008-04-01"}, 20080401, false},
		{"multi-day takes the first", []string{"2019-05-07", "2019-05-08"}, 20190507, false},
		{"malformed", []string{"2019/05/07"}, 0, true},
		{"unpadded", []string{"2019-5-7"}, 0, true},
		{"empty", nil, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := csInfo{Dates: tc.dates}
			got, err := i.dateInt()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("dateInt() = %d, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("dateInt(): %v", err)
			}
			if got != tc.want {
				t.Errorf("dateInt() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestStage covers the polymorphism in info.event.stage, which is absent for
// league fixtures and a string for knockouts.
func TestStage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"absent", "", ""},
		{"string", `"Final"`, "Final"},
		{"qualifier", `"Qualifier 1"`, "Qualifier 1"},
		{"numeric", `3`, "3"},
		{"null", `null`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var i csInfo
			if tc.raw != "" {
				i.Event.Stage = json.RawMessage(tc.raw)
			}
			if got := i.stage(); got != tc.want {
				t.Errorf("stage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeliveryIsLegal(t *testing.T) {
	tests := []struct {
		name  string
		wides int
		nobs  int
		want  bool
	}{
		{"legal", 0, 0, true},
		{"wide", 1, 0, false},
		{"no-ball", 0, 1, false},
		{"wide worth more than one", 5, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d csDelivery
			d.Extras.Wides = tc.wides
			d.Extras.NoBalls = tc.nobs
			if got := d.IsLegal(); got != tc.want {
				t.Errorf("IsLegal() = %v, want %v", got, tc.want)
			}
		})
	}
}
