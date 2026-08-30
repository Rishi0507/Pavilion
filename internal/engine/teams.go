package engine

import "strings"

// Franchise names.
//
// The corpus records whatever name a side used on the day, so one franchise can
// appear under several: Kings XI Punjab and Punjab Kings are the same club eight
// years apart, as are Delhi Daredevils and Delhi Capitals, and Royal Challengers
// Bangalore and Bengaluru. A career shown as three separate teams when it was
// one is simply wrong, so the historical names fold into the current one here,
// at the point of display, while the corpus keeps what it was told.
//
// Deccan Chargers is deliberately not folded into Sunrisers Hyderabad. The
// Chargers were terminated and the Hyderabad slot re-tendered to a different
// owner, so they are two franchises that happen to share a city, and a career
// at one is not a career at the other.
//
// Names appear as plain text only. No crest, no wordmark and no kit colour: the
// name is a fact about where somebody played, whereas the marks are the club's
// own and not ours to reproduce.
var franchiseNow = map[string]string{
	"Kings XI Punjab":             "Punjab Kings",
	"Delhi Daredevils":            "Delhi Capitals",
	"Royal Challengers Bangalore": "Royal Challengers Bengaluru",
	"Rising Pune Supergiants":     "Rising Pune Supergiant",
}

// franchiseShort is how a side is labelled where there is no room for the full
// name, which is most places a player is listed.
var franchiseShort = map[string]string{
	"Punjab Kings":                "PBKS",
	"Delhi Capitals":              "DC",
	"Royal Challengers Bengaluru": "RCB",
	"Kolkata Knight Riders":       "KKR",
	"Chennai Super Kings":         "CSK",
	"Rajasthan Royals":            "RR",
	"Mumbai Indians":              "MI",
	"Sunrisers Hyderabad":         "SRH",
	"Lucknow Super Giants":        "LSG",
	"Gujarat Titans":              "GT",
	"Deccan Chargers":             "DECCAN",
	"Kochi Tuskers Kerala":        "KOCHI",
	"Pune Warriors":               "PUNE",
	"Rising Pune Supergiant":      "RPS",
	"Gujarat Lions":               "GL",
}

// CanonicalTeam folds a renamed franchise onto its current name.
func CanonicalTeam(name string) string {
	if now, ok := franchiseNow[name]; ok {
		return now
	}
	return name
}

// ShortTeam abbreviates a franchise, falling back to initials for anything the
// table does not know.
func ShortTeam(name string) string {
	name = CanonicalTeam(name)
	if s, ok := franchiseShort[name]; ok {
		return s
	}
	var b strings.Builder
	for _, w := range strings.Fields(name) {
		b.WriteByte(w[0])
	}
	return strings.ToUpper(b.String())
}

// Monogram reduces a name to the two initials shown on a player's tile.
//
// Match photography belongs to picture agencies and there is no licensed source
// for it, so initials set in the board's own face stand in: honest about what
// they are, and a better fit for the scoreboard than a borrowed headshot would
// be. Cricsheet writes names as initials and surname ("KD Karthik"), so the
// first letter of each end is the right pair.
func Monogram(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "—"
	}
	letters := func(s string) string {
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				return strings.ToUpper(string(r))
			}
		}
		return ""
	}
	last := letters(fields[len(fields)-1])
	if len(fields) == 1 {
		return strings.ToUpper(firstTwo(fields[0]))
	}
	if m := letters(fields[0]) + last; m != "" {
		return m
	}
	return strings.ToUpper(firstTwo(fields[len(fields)-1]))
}

func firstTwo(s string) string {
	r := []rune(s)
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r)
}

// Franchise colours.
//
// A tint only, and never a crest or a wordmark. What a club wears is not the
// same kind of property as its badge: the colour places a player at a glance,
// which is the whole job here, and it does it without reproducing anything that
// belongs to anybody. These are deliberately muted away from the broadcast
// versions, both so they sit on a warm dark board without shouting and so that
// no card reads as an official one.
//
// Every value is a hue that a supporter would name correctly with no logo
// present: Chennai yellow, Mumbai blue, Kolkata purple, Bangalore red.
var franchiseColour = map[string]string{
	"Chennai Super Kings":         "#C9A227",
	"Mumbai Indians":              "#3C6DA8",
	"Kolkata Knight Riders":       "#7A5CA8",
	"Royal Challengers Bengaluru": "#C0473C",
	"Sunrisers Hyderabad":         "#D07A34",
	"Delhi Capitals":              "#4272B8",
	"Punjab Kings":                "#C2453F",
	"Rajasthan Royals":            "#C55A8E",
	"Lucknow Super Giants":        "#3E8FA8",
	"Gujarat Titans":              "#5C7C99",
	"Deccan Chargers":             "#4A6E8C",
	"Kochi Tuskers Kerala":        "#8A5FA0",
	"Pune Warriors":               "#4C7FB5",
	"Rising Pune Supergiant":      "#B0555A",
	"Gujarat Lions":               "#C87A45",
}

// TeamColour returns the tint for a side, or an empty string for one with no
// colour on record, so the caller falls back to the board's own palette rather
// than inventing something.
func TeamColour(name string) string {
	return franchiseColour[CanonicalTeam(name)]
}

// shortToFull inverts the abbreviation table, so a player carrying only a short
// code can still be coloured.
var shortToFull = func() map[string]string {
	m := make(map[string]string, len(franchiseShort))
	for full, short := range franchiseShort {
		m[short] = full
	}
	return m
}()

// TeamColourShort returns the tint for an abbreviated side.
func TeamColourShort(short string) string {
	if full, ok := shortToFull[short]; ok {
		return franchiseColour[full]
	}
	return ""
}
