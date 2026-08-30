package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Types mirroring the Cricsheet JSON format v1.2.0.
// https://cricsheet.org/format/json/
//
// Several fields are polymorphic across seasons (season is sometimes an int and
// sometimes a "2007/08" string), so they are held as json.RawMessage and
// normalised explicitly rather than trusted to unmarshal.

type csMatch struct {
	Meta struct {
		DataVersion string `json:"data_version"`
		Created     string `json:"created"`
		Revision    int    `json:"revision"`
	} `json:"meta"`
	Info    csInfo      `json:"info"`
	Innings []csInnings `json:"innings"`
}

type csInfo struct {
	BallsPerOver int             `json:"balls_per_over"`
	City         string          `json:"city"`
	Venue        string          `json:"venue"`
	Dates        []string        `json:"dates"`
	MatchType    string          `json:"match_type"`
	TeamType     string          `json:"team_type"`
	Gender       string          `json:"gender"`
	Overs        int             `json:"overs"`
	Season       json.RawMessage `json:"season"`
	Teams        []string        `json:"teams"`
	Toss         struct {
		Decision string `json:"decision"`
		Winner   string `json:"winner"`
	} `json:"toss"`
	Outcome struct {
		Winner     string `json:"winner"`
		Result     string `json:"result"`
		Eliminator string `json:"eliminator"`
	} `json:"outcome"`
	Event struct {
		Name  string          `json:"name"`
		Stage json.RawMessage `json:"stage"`
	} `json:"event"`
	Players  map[string][]string `json:"players"`
	Registry struct {
		People map[string]string `json:"people"`
	} `json:"registry"`
}

type csInnings struct {
	Team   string   `json:"team"`
	Overs  []csOver `json:"overs"`
	Target *struct {
		Runs  int     `json:"runs"`
		Overs float64 `json:"overs"`
	} `json:"target"`
	SuperOver       bool `json:"super_over"`
	MiscountedOvers map[string]struct {
		Balls int `json:"balls"`
	} `json:"miscounted_overs"`
}

type csOver struct {
	Over       int          `json:"over"`
	Deliveries []csDelivery `json:"deliveries"`
}

type csDelivery struct {
	ActualDelivery string `json:"actual_delivery"`
	Batter         string `json:"batter"`
	Bowler         string `json:"bowler"`
	NonStriker     string `json:"non_striker"`
	Runs           struct {
		Batter int `json:"batter"`
		Extras int `json:"extras"`
		Total  int `json:"total"`
	} `json:"runs"`
	Extras struct {
		Wides   int `json:"wides"`
		NoBalls int `json:"noballs"`
		Byes    int `json:"byes"`
		LegByes int `json:"legbyes"`
		Penalty int `json:"penalty"`
	} `json:"extras"`
	Wickets []struct {
		Kind      string `json:"kind"`
		PlayerOut string `json:"player_out"`
	} `json:"wickets"`
}

// IsLegal reports whether the delivery counts towards the six balls of an over.
func (d *csDelivery) IsLegal() bool { return d.Extras.Wides == 0 && d.Extras.NoBalls == 0 }

// stage returns the knockout stage name, or "" for a league fixture.
func (i *csInfo) stage() string {
	if len(i.Event.Stage) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(i.Event.Stage, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(i.Event.Stage, &n); err == nil {
		return n.String()
	}
	return ""
}

// editionYear returns the IPL edition this match belongs to.
//
// The season label cannot be used directly: Cricsheet labels IPL 2008 as
// "2007/08" and IPL 2020 as "2020/21", so the naive "take the later year" rule
// is right for the former and wrong for the latter. The year of the match date
// is correct for every edition.
func (i *csInfo) editionYear() (uint16, error) {
	if len(i.Dates) == 0 {
		return 0, fmt.Errorf("match has no dates")
	}
	y, err := strconv.Atoi(i.Dates[0][:4])
	if err != nil {
		return 0, fmt.Errorf("unparseable date %q: %w", i.Dates[0], err)
	}
	return uint16(y), nil
}

// dateInt returns the first day of the match as YYYYMMDD.
func (i *csInfo) dateInt() (int32, error) {
	if len(i.Dates) == 0 {
		return 0, fmt.Errorf("match has no dates")
	}
	p := strings.Split(i.Dates[0], "-")
	if len(p) != 3 {
		return 0, fmt.Errorf("unparseable date %q", i.Dates[0])
	}
	var n int32
	for _, s := range p {
		v, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("unparseable date %q: %w", i.Dates[0], err)
		}
		n = n*100 + int32(v)
	}
	if len(p[1]) != 2 || len(p[2]) != 2 {
		return 0, fmt.Errorf("unpadded date %q", i.Dates[0])
	}
	return n, nil
}

// legalBallOf extracts the legal-ball ordinal from an actual_delivery string
// such as "17.4", returning a zero-based index.
//
// Cricsheet numbers a delivery by the legal ball it is attempting, so a wide at
// "0.5" is followed by another delivery also labelled "0.5". This is the field
// that makes over indexing drift if ignored; the ETL uses it purely to
// cross-check its own independently computed count.
func legalBallOf(actual string) (over int, ball int, err error) {
	i := strings.IndexByte(actual, '.')
	if i < 0 {
		return 0, 0, fmt.Errorf("malformed actual_delivery %q", actual)
	}
	if over, err = strconv.Atoi(actual[:i]); err != nil {
		return 0, 0, fmt.Errorf("malformed actual_delivery %q: %w", actual, err)
	}
	if ball, err = strconv.Atoi(actual[i+1:]); err != nil {
		return 0, 0, fmt.Errorf("malformed actual_delivery %q: %w", actual, err)
	}
	return over, ball - 1, nil
}
