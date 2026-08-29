package engine

import (
	"hash/fnv"
	"strings"

	"manhattan/internal/corpus"
)

// Ground descriptions.
//
// The corpus records whatever venue string the scorer typed, so one ground
// appears under several names across the seasons: Feroz Shah Kotla and Arun
// Jaitley Stadium are the same ground either side of a renaming, and Chepauk
// turns up with and without the city appended. Aliases fold together here.
//
// The measurements are the ones that matter to a T20: the straight boundary and
// the square boundary, in metres. They differ enough between grounds to be worth
// showing. Chinnaswamy is a small, square ground where the ball keeps going, and
// the Chepauk square boundaries are long enough to change where the sixes come
// from. They are approximate, as published boundary lengths always are, because
// the rope moves between matches.
//
// They are not fed into the model. The model learns venue effects from the
// deliveries themselves, which is a better measure than a number off a website.
// These exist so the ground can be drawn, and so that what is drawn is the shape
// of the actual place rather than a generic oval.

// Ground is one venue, as the client draws and labels it.
type Ground struct {
	Name     string `json:"name"`     // the current name of the ground
	City     string `json:"city"`     // where it is
	Straight int    `json:"straight"` // straight boundary, metres
	Square   int    `json:"square"`   // square boundary, metres
	Capacity int    `json:"capacity"`
	Tiers    int    `json:"tiers"`  // how many decks the stands are drawn with
	Roof     string `json:"roof"`   // none, partial or full
	Lights   string `json:"lights"` // pylons or roof
	Note     string `json:"note"`   // one line a commentator might say

	// Home is the club that plays here, and Colour their tint. A ground is not
	// a neutral box to the people in it: Chepauk is yellow and the Wankhede is
	// blue, and colouring the drawing accordingly is most of what makes it feel
	// like a particular place rather than a diagram of an oval. Grounds with no
	// resident club, and the neutral venues abroad, carry neither.
	Home   string `json:"home"`
	Colour string `json:"colour"`
}

type groundSpec struct {
	Ground
	aliases []string
}

var groundSpecs = []groundSpec{
	{Ground{"M Chinnaswamy Stadium", "Bengaluru", 68, 62, 33000, 3, "partial", "roof",
		"Small square boundaries and thin air: the highest-scoring ground in the competition.", "", ""},
		[]string{"M.Chinnaswamy Stadium", "M Chinnaswamy Stadium, Bengaluru"}},
	{Ground{"Wankhede Stadium", "Mumbai", 72, 66, 33000, 3, "partial", "roof",
		"The sea breeze arrives in the evening and the ball starts to swing.", "", ""},
		[]string{"Wankhede Stadium, Mumbai"}},
	{Ground{"Eden Gardens", "Kolkata", 66, 68, 68000, 3, "partial", "pylons",
		"The largest crowd in the competition, and a square turn that arrives late.", "", ""},
		[]string{"Eden Gardens, Kolkata"}},
	{Ground{"MA Chidambaram Stadium", "Chennai", 75, 70, 38000, 3, "partial", "pylons",
		"Long square boundaries and a pitch that grips: spinners bowl in the powerplay here.", "", ""},
		[]string{"MA Chidambaram Stadium, Chepauk", "MA Chidambaram Stadium, Chepauk, Chennai"}},
	{Ground{"Arun Jaitley Stadium", "Delhi", 68, 64, 41000, 2, "partial", "pylons",
		"A slow, low outfield by April, and short straight boundaries at either end.", "", ""},
		[]string{"Feroz Shah Kotla", "Arun Jaitley Stadium, Delhi"}},
	{Ground{"Narendra Modi Stadium", "Ahmedabad", 76, 68, 132000, 3, "full", "roof",
		"The largest cricket ground in the world, and the boundaries to match.", "", ""},
		[]string{"Sardar Patel Stadium, Motera", "Narendra Modi Stadium, Ahmedabad"}},
	{Ground{"Rajiv Gandhi International Stadium", "Hyderabad", 72, 64, 39000, 3, "partial", "pylons",
		"A quick outfield, and a pitch that holds up better than most in the second innings.", "", ""},
		[]string{"Rajiv Gandhi International Stadium, Uppal",
			"Rajiv Gandhi International Stadium, Uppal, Hyderabad"}},
	{Ground{"Punjab Cricket Association IS Bindra Stadium", "Mohali", 70, 65, 26000, 2, "partial", "pylons",
		"Bounce and carry: the closest thing in India to a fast, true surface.", "", ""},
		[]string{"Punjab Cricket Association Stadium, Mohali",
			"Punjab Cricket Association IS Bindra Stadium, Mohali",
			"Punjab Cricket Association IS Bindra Stadium, Mohali, Chandigarh"}},
	{Ground{"Sawai Mansingh Stadium", "Jaipur", 70, 64, 30000, 2, "none", "pylons",
		"A used, gripping surface where chasing has always been the harder half.", "", ""},
		[]string{"Sawai Mansingh Stadium, Jaipur"}},
	{Ground{"Dr DY Patil Sports Academy", "Navi Mumbai", 72, 65, 55000, 3, "partial", "roof",
		"A large bowl on the edge of the city, and a fast outfield.", "", ""},
		[]string{"Dr DY Patil Sports Academy, Mumbai"}},
	{Ground{"Brabourne Stadium", "Mumbai", 68, 60, 20000, 2, "partial", "pylons",
		"Short all round: the smallest of the big grounds on the circuit.", "", ""},
		[]string{"Brabourne Stadium, Mumbai"}},
	{Ground{"Maharashtra Cricket Association Stadium", "Pune", 74, 66, 37000, 3, "partial", "roof",
		"Long boundaries and an even contest, which is rarer than it sounds.", "", ""},
		[]string{"Subrata Roy Sahara Stadium", "Maharashtra Cricket Association Stadium, Pune"}},
	{Ground{"Ekana Cricket Stadium", "Lucknow", 73, 67, 50000, 3, "partial", "roof",
		"A slow, two-paced pitch where the par score is lower than anywhere else.", "", ""},
		[]string{"Bharat Ratna Shri Atal Bihari Vajpayee Ekana Cricket Stadium",
			"Bharat Ratna Shri Atal Bihari Vajpayee Ekana Cricket Stadium, Lucknow"}},
	{Ground{"Himachal Pradesh Cricket Association Stadium", "Dharamsala", 70, 63, 23000, 1, "none", "pylons",
		"The Dhauladhar range behind the sightscreen, and a ball that carries in the cold.", "", ""},
		[]string{"Himachal Pradesh Cricket Association Stadium, Dharamsala"}},
	{Ground{"Holkar Cricket Stadium", "Indore", 66, 60, 30000, 2, "none", "pylons",
		"Short, flat and merciless on bowlers.", "", ""},
		nil},
	{Ground{"ACA-VDCA Cricket Stadium", "Visakhapatnam", 72, 66, 27000, 2, "partial", "pylons",
		"A coastal ground where the ball swings under lights.", "", ""},
		[]string{"Dr. Y.S. Rajasekhara Reddy ACA-VDCA Cricket Stadium",
			"Dr. Y.S. Rajasekhara Reddy ACA-VDCA Cricket Stadium, Visakhapatnam"}},
	{Ground{"Barsapara Cricket Stadium", "Guwahati", 70, 64, 40000, 2, "partial", "roof",
		"A newer ground with a true surface and generous straight boundaries.", "", ""},
		[]string{"Barsapara Cricket Stadium, Guwahati"}},
	{Ground{"Maharaja Yadavindra Singh Stadium", "New Chandigarh", 72, 66, 33000, 2, "partial", "roof",
		"The replacement for Mohali, still finding its character.", "", ""},
		[]string{"Maharaja Yadavindra Singh International Cricket Stadium, Mullanpur",
			"Maharaja Yadavindra Singh International Cricket Stadium, New Chandigarh"}},
	{Ground{"Vidarbha Cricket Association Stadium", "Nagpur", 74, 68, 45000, 2, "none", "pylons",
		"Large square boundaries, and a pitch that has taken turn since it opened.", "", ""},
		[]string{"Vidarbha Cricket Association Stadium, Jamtha"}},
	{Ground{"JSCA International Stadium Complex", "Ranchi", 70, 64, 39000, 2, "partial", "roof",
		"A grass bank down one side, and a quick outfield.", "", ""},
		nil},
	{Ground{"Saurashtra Cricket Association Stadium", "Rajkot", 68, 62, 28000, 2, "none", "pylons",
		"Short boundaries and the flattest pitch in the west.", "", ""},
		nil},
	{Ground{"Barabati Stadium", "Cuttack", 70, 63, 45000, 2, "none", "pylons",
		"An old ground, humid enough that the ball comes on slowly.", "", ""},
		nil},
	{Ground{"Green Park", "Kanpur", 72, 65, 32000, 2, "none", "pylons",
		"A ground that has staged Test cricket since 1952, and shows it.", "", ""},
		nil},
	{Ground{"Shaheed Veer Narayan Singh Stadium", "Raipur", 74, 68, 65000, 2, "partial", "pylons",
		"Large, new and rarely used, with boundaries that suit the bowlers.", "", ""},
		[]string{"Shaheed Veer Narayan Singh International Stadium",
			"Shaheed Veer Narayan Singh International Stadium, Raipur"}},
	{Ground{"Nehru Stadium", "Kochi", 68, 62, 55000, 2, "none", "pylons",
		"A football ground that took the cricket for a single season.", "", ""},
		nil},

	// The seasons played abroad: 2009 in South Africa, 2014 and 2020 in the UAE.
	{Ground{"Dubai International Cricket Stadium", "Dubai", 71, 65, 25000, 2, "partial", "roof",
		"The ring of light, and a pitch that slows through the innings.", "", ""},
		nil},
	{Ground{"Sharjah Cricket Stadium", "Sharjah", 65, 60, 17000, 1, "none", "pylons",
		"The smallest ground the competition visits: everything clears the rope.", "", ""},
		nil},
	{Ground{"Sheikh Zayed Stadium", "Abu Dhabi", 73, 67, 20000, 2, "partial", "roof",
		"Long boundaries and grip: the hardest of the three to score at.", "", ""},
		[]string{"Zayed Cricket Stadium, Abu Dhabi"}},
	{Ground{"Newlands", "Cape Town", 70, 64, 25000, 2, "partial", "pylons",
		"Table Mountain over the sightscreen, and a ball that swings all evening.", "", ""},
		nil},
	{Ground{"New Wanderers Stadium", "Johannesburg", 68, 62, 34000, 3, "none", "pylons",
		"The Bullring, at altitude, where the ball flies.", "", ""},
		nil},
	{Ground{"Kingsmead", "Durban", 70, 63, 25000, 2, "partial", "pylons",
		"Humid and seaming, with the coastal breeze off the Indian Ocean.", "", ""},
		nil},
	{Ground{"SuperSport Park", "Centurion", 70, 64, 22000, 2, "none", "pylons",
		"A grass bank at one end, and pace in the pitch.", "", ""},
		nil},
	{Ground{"St George's Park", "Gqeberha", 68, 62, 19000, 2, "none", "pylons",
		"The brass band, and the wind that never stops.", "", ""},
		nil},
	{Ground{"Buffalo Park", "East London", 68, 62, 15000, 1, "none", "pylons",
		"A small coastal ground that has staged few internationals.", "", ""},
		nil},
	{Ground{"De Beers Diamond Oval", "Kimberley", 66, 60, 11000, 1, "none", "pylons",
		"Short square boundaries on the edge of the Northern Cape.", "", ""},
		nil},
	{Ground{"OUTsurance Oval", "Bloemfontein", 68, 62, 20000, 1, "none", "pylons",
		"High and dry, and the ball carries further than the batter expects.", "", ""},
		nil},
}

var groundIndex = func() map[string]*Ground {
	m := make(map[string]*Ground, len(groundSpecs)*2)
	for i := range groundSpecs {
		g := &groundSpecs[i].Ground
		m[strings.ToLower(g.Name)] = g
		for _, a := range groundSpecs[i].aliases {
			m[strings.ToLower(a)] = g
		}
	}
	return m
}()

// GroundOf describes a venue by the name the corpus recorded.
//
// An unknown ground still gets a description rather than nothing. The figures
// are then derived from the name itself, so they are arbitrary but stable: the
// same ground is drawn the same way every time, which for a background matters
// more than being right about a venue used once in 2009 would.
// groundHome names the resident club of each ground that has one.
var groundHome = map[string]string{
	"M Chinnaswamy Stadium":                        "Royal Challengers Bengaluru",
	"Wankhede Stadium":                             "Mumbai Indians",
	"Eden Gardens":                                 "Kolkata Knight Riders",
	"MA Chidambaram Stadium":                       "Chennai Super Kings",
	"Arun Jaitley Stadium":                         "Delhi Capitals",
	"Narendra Modi Stadium":                        "Gujarat Titans",
	"Rajiv Gandhi International Stadium":           "Sunrisers Hyderabad",
	"Punjab Cricket Association IS Bindra Stadium": "Punjab Kings",
	"Maharaja Yadavindra Singh Stadium":            "Punjab Kings",
	"Sawai Mansingh Stadium":                       "Rajasthan Royals",
	"Ekana Cricket Stadium":                        "Lucknow Super Giants",
	"Maharashtra Cricket Association Stadium":      "Rising Pune Supergiant",
	"Barsapara Cricket Stadium":                    "Rajasthan Royals",
	"Himachal Pradesh Cricket Association Stadium": "Punjab Kings",
	"Holkar Cricket Stadium":                       "Kochi Tuskers Kerala",
	"Saurashtra Cricket Association Stadium":       "Gujarat Lions",
	"Brabourne Stadium":                            "Mumbai Indians",
	"Dr DY Patil Sports Academy":                   "Mumbai Indians",
	"Nehru Stadium":                                "Kochi Tuskers Kerala",
}

func GroundOf(venue string) Ground {
	if g, ok := groundIndex[strings.ToLower(strings.TrimSpace(venue))]; ok {
		out := *g
		if home, ok := groundHome[out.Name]; ok {
			out.Home = ShortTeam(home)
			out.Colour = TeamColour(home)
		}
		return out
	}
	h := fnv.New32a()
	h.Write([]byte(venue))
	n := h.Sum32()

	name, city := venue, ""
	if i := strings.LastIndex(venue, ", "); i > 0 {
		name, city = venue[:i], venue[i+2:]
	}
	return Ground{
		Name:     name,
		City:     city,
		Straight: 66 + int(n%11),
		Square:   60 + int((n>>8)%9),
		Capacity: 15000 + int((n>>16)%30000),
		Tiers:    1 + int((n>>4)%3),
		Roof:     [...]string{"none", "partial", "none"}[(n>>12)%3],
		Lights:   [...]string{"pylons", "roof"}[(n>>20)%2],
	}
}

// RecentVenues lists the grounds in current use, most recent first.
//
// "In current use" means a ground that has staged a match in the seasons the
// game deals from. It keeps the South African grounds of 2009 and the UAE
// grounds of 2014 out of the ordinary rotation without hard-coding a list that
// would go stale the moment a new stadium opened.
func (e *Engine) RecentVenues() []corpus.VenueID {
	if e.recentVenues != nil {
		return e.recentVenues
	}
	last := make([]uint16, len(e.store.Venues))
	for i := range e.store.M.Venue {
		v := e.store.M.Venue[i]
		if season := e.store.M.Season[i]; season > last[v] {
			last[v] = season
		}
	}
	for v, season := range last {
		if season >= recentSince {
			e.recentVenues = append(e.recentVenues, corpus.VenueID(v))
		}
	}
	return e.recentVenues
}
