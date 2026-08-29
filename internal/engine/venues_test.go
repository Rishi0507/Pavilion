package engine

import "testing"

// Every venue string in the corpus must resolve to a described ground.
//
// The corpus records whatever the scorer typed, and the same ground appears
// under several names across eighteen seasons. An alias that is missed does not
// fail loudly: it silently produces a ground with hashed dimensions and no
// commentary line, which looks like a real answer and is not one. So the check
// is that the name matched the table, not merely that something came back.
func TestEveryKnownVenueResolves(t *testing.T) {
	// The full list is exercised by the corpus test; these are the ones where a
	// rename or a city suffix makes an alias easy to forget.
	aliases := map[string]string{
		"Feroz Shah Kotla":                           "Arun Jaitley Stadium",
		"Arun Jaitley Stadium, Delhi":                "Arun Jaitley Stadium",
		"M.Chinnaswamy Stadium":                      "M Chinnaswamy Stadium",
		"M Chinnaswamy Stadium, Bengaluru":           "M Chinnaswamy Stadium",
		"MA Chidambaram Stadium, Chepauk":            "MA Chidambaram Stadium",
		"MA Chidambaram Stadium, Chepauk, Chennai":   "MA Chidambaram Stadium",
		"Sardar Patel Stadium, Motera":               "Narendra Modi Stadium",
		"Narendra Modi Stadium, Ahmedabad":           "Narendra Modi Stadium",
		"Subrata Roy Sahara Stadium":                 "Maharashtra Cricket Association Stadium",
		"Rajiv Gandhi International Stadium, Uppal":  "Rajiv Gandhi International Stadium",
		"Punjab Cricket Association Stadium, Mohali": "Punjab Cricket Association IS Bindra Stadium",
		"Zayed Cricket Stadium, Abu Dhabi":           "Sheikh Zayed Stadium",
	}

	for alias, want := range aliases {
		g := GroundOf(alias)
		if g.Name != want {
			t.Errorf("GroundOf(%q).Name = %q, want %q", alias, g.Name, want)
		}
		if g.Note == "" {
			t.Errorf("GroundOf(%q) fell through to the hashed default", alias)
		}
	}
}

// An unknown ground must still be describable, and describable the same way
// every time: a background that changed shape between two views of the same
// fixture would look like a bug.
func TestUnknownGroundIsStable(t *testing.T) {
	a := GroundOf("Some Ground Nobody Has Heard Of, Nowhere")
	b := GroundOf("Some Ground Nobody Has Heard Of, Nowhere")
	if a != b {
		t.Errorf("the same unknown ground produced two descriptions:\n%+v\n%+v", a, b)
	}
	if a.City != "Nowhere" || a.Name != "Some Ground Nobody Has Heard Of" {
		t.Errorf("city was not split off the name: %+v", a)
	}
	if a.Straight < 60 || a.Straight > 90 || a.Square < 55 || a.Square > 85 {
		t.Errorf("implausible boundary lengths: %+v", a)
	}
}

// Renamed franchises are one club, not several.
func TestFranchiseRenamesFold(t *testing.T) {
	same := [][2]string{
		{"Kings XI Punjab", "Punjab Kings"},
		{"Delhi Daredevils", "Delhi Capitals"},
		{"Royal Challengers Bangalore", "Royal Challengers Bengaluru"},
	}
	for _, pair := range same {
		if CanonicalTeam(pair[0]) != CanonicalTeam(pair[1]) {
			t.Errorf("%q and %q are the same club but canonicalise apart", pair[0], pair[1])
		}
	}

	// The Chargers were terminated and the Hyderabad slot re-tendered to a
	// different owner, so these are two franchises that share a city. Folding
	// them would credit a decade of Sunrisers cricket to players who never
	// played it.
	if CanonicalTeam("Deccan Chargers") == CanonicalTeam("Sunrisers Hyderabad") {
		t.Error("Deccan Chargers was folded into Sunrisers Hyderabad")
	}
}

func TestShortTeam(t *testing.T) {
	cases := map[string]string{
		"Kings XI Punjab":             "PBKS",
		"Punjab Kings":                "PBKS",
		"Delhi Daredevils":            "DC",
		"Royal Challengers Bangalore": "RCB",
		"Chennai Super Kings":         "CSK",
		"Mumbai Indians":              "MI",
		// Unknown clubs fall back to initials rather than to nothing.
		"Some Invented Club": "SIC",
	}
	for in, want := range cases {
		if got := ShortTeam(in); got != want {
			t.Errorf("ShortTeam(%q) = %q, want %q", in, got, want)
		}
	}
}

// Cricsheet writes names as initials and surname, so the monogram is the first
// letter of each end.
func TestMonogram(t *testing.T) {
	cases := map[string]string{
		"KD Karthik":      "KK",
		"SA Yadav":        "SY",
		"V Kohli":         "VK",
		"CH Gayle":        "CG",
		"AB de Villiers":  "AV",
		"Harbhajan Singh": "HS",
		"Sakib Hussain":   "SH",
		"Yash Thakur":     "YT",
		// One name, and nothing at all.
		"Sandeep": "SA",
		"":        "—",
	}
	for in, want := range cases {
		if got := Monogram(in); got != want {
			t.Errorf("Monogram(%q) = %q, want %q", in, got, want)
		}
	}
}

// The check that actually catches a missed alias: every venue string the corpus
// holds, against the real corpus rather than a list written from memory.
func TestEveryCorpusVenueIsDescribed(t *testing.T) {
	e := testEngine(t)
	for _, v := range e.store.Venues {
		if g := GroundOf(v); g.Note == "" {
			t.Errorf("venue %q has no ground description; add it or alias it", v)
		}
	}
}

// Career labels have to answer the question a supporter would ask, which is
// "who is that", not "where did he finish".
func TestCareerLabels(t *testing.T) {
	e := testEngine(t)

	want := map[string]string{
		"CH Gayle":        "RCB", // finished at Punjab, remembered at Bangalore
		"V Sehwag":        "DC",  // played as Delhi Daredevils, folded onto Capitals
		"MS Dhoni":        "CSK",
		"SL Malinga":      "MI",
		"SK Warne":        "RR",
		"Harbhajan Singh": "MI",
	}

	found := map[string]bool{}
	for _, id := range e.dealable {
		p := e.player(id)
		if team, ok := want[p.Name]; ok {
			found[p.Name] = true
			if p.Team != team {
				t.Errorf("%s is labelled %q, want %q", p.Name, p.Team, team)
			}
			if p.Years == "" {
				t.Errorf("%s has no years", p.Name)
			}
		}
	}
	for name := range want {
		if !found[name] {
			t.Logf("%s is not in the dealable set; label not checked", name)
		}
	}
}
