package corpus

// Aggregation over the corpus is a linear scan. At ~295k deliveries and eight
// bytes of hot columns per delivery, a full pass costs single-digit
// milliseconds, which is why there is no index here and no query planner.

import "sort"

// Volume counts a player's career involvement in the corpus.
//
// It is the basis of the eligibility filter: the game may only deal players
// with enough recorded deliveries for their rates to mean something, and the
// attribute-sourcing pipeline only resolves players the game can deal.
type Volume struct {
	BallsFaced   int // legal deliveries faced
	RunsScored   int // runs off the bat
	Dismissals   int // times out, excluding retired hurt
	BallsBowled  int // legal deliveries bowled
	RunsConceded int // runs charged to the bowler
	Wickets      int // dismissals credited to the bowler
	Innings      int // innings appeared in, batting or bowling
	FirstSeason  uint16
	LastSeason   uint16
}

// IsBowler reports whether the player has bowled at all.
func (v Volume) IsBowler() bool { return v.BallsBowled > 0 }

// Volumes computes career volume for every player, indexed by PlayerID.
//
// Super-over innings are excluded: a one-over shootout has nothing in common
// with the phase structure the game models, and including it would distort
// death-over rates in particular.
func (s *Store) Volumes() []Volume {
	v := make([]Volume, len(s.Players))

	// Track innings appearances without allocating per player, by remembering
	// the last innings each player was seen in.
	lastInn := make([]int32, len(s.Players))
	for i := range lastInn {
		lastInn[i] = -1
	}

	touch := func(p PlayerID, inn InningsID, season uint16) {
		if p == NoPlayer {
			return
		}
		if lastInn[p] != int32(inn) {
			lastInn[p] = int32(inn)
			v[p].Innings++
		}
		if v[p].FirstSeason == 0 || season < v[p].FirstSeason {
			v[p].FirstSeason = season
		}
		if season > v[p].LastSeason {
			v[p].LastSeason = season
		}
	}

	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if s.Inn.SuperOver[inn] {
			continue
		}
		season := s.M.Season[s.Inn.Match[inn]]

		bat, bowl := s.D.Batter[i], s.D.Bowler[i]
		touch(bat, inn, season)
		touch(bowl, inn, season)

		if bat != NoPlayer {
			if s.D.Legal[i] {
				v[bat].BallsFaced++
			}
			v[bat].RunsScored += int(s.D.RunsBat[i])
		}
		if bowl != NoPlayer {
			if s.D.Legal[i] {
				v[bowl].BallsBowled++
			}
			v[bowl].RunsConceded += s.D.BowlerRuns(i)
			if s.D.Wicket[i].CreditedToBowler() {
				v[bowl].Wickets++
			}
		}
		if out := s.D.PlayerOut[i]; out != NoPlayer && s.D.Wicket[i].CostsWicket() {
			v[out].Dismissals++
		}
	}
	return v
}

// Eligibility is the filter deciding which players the game may deal.
//
// A player qualifies as a bowler or as a batter independently: a specialist
// bowler need not clear the batting threshold to be dealt as one of the five
// bowlers, and vice versa.
type Eligibility struct {
	MinBallsBowled int    // to be dealt as a bowler
	MinBallsFaced  int    // to be dealt as a batter
	SinceSeason    uint16 // 0 for no recency filter
}

// DefaultEligibility is the threshold the game ships with.
//
// 120 legal balls is twenty overs: enough that a bowler's phase rates are not
// pure noise, and low enough to admit a genuine death-overs specialist who has
// played a single season. 200 balls faced is a comparable bar for batters.
var DefaultEligibility = Eligibility{MinBallsBowled: 120, MinBallsFaced: 200}

// Qualifies reports whether a volume clears the filter, and in which roles.
func (e Eligibility) Qualifies(v Volume) (bowler, batter bool) {
	if e.SinceSeason != 0 && v.LastSeason < e.SinceSeason {
		return false, false
	}
	return v.BallsBowled >= e.MinBallsBowled, v.BallsFaced >= e.MinBallsFaced
}

// Eligible returns the PlayerIDs the game may deal, in ascending id order.
func (s *Store) Eligible(e Eligibility) (players, bowlers, batters []PlayerID) {
	vols := s.Volumes()
	for i, v := range vols {
		bowl, bat := e.Qualifies(v)
		if !bowl && !bat {
			continue
		}
		id := PlayerID(i)
		players = append(players, id)
		if bowl {
			bowlers = append(bowlers, id)
		}
		if bat {
			batters = append(batters, id)
		}
	}
	return players, bowlers, batters
}

// Dealable narrows the eligible set to players the game can actually put in
// front of someone.
//
// Eligibility is about sample size: enough deliveries for a player's rates to
// mean something. Dealability adds the second requirement, that the attributes
// the matchup model needs are known. A bowler whose type nobody recorded cannot
// be modelled against a left-hander, and a player with no Wikipedia article is
// almost by definition not one a daily game about recognisable cricketers
// should be dealing.
//
// The two predicates are passed in rather than looked up here so that this
// package keeps no dependency on the attribute table.
func (s *Store) Dealable(e Eligibility, batKnown, bowlKnown func(cricsheetID string) bool) (players, bowlers, batters, excluded []PlayerID) {
	eligible, eligibleBowlers, eligibleBatters := s.Eligible(e)

	isBowler := make(map[PlayerID]bool, len(eligibleBowlers))
	for _, p := range eligibleBowlers {
		isBowler[p] = true
	}
	isBatter := make(map[PlayerID]bool, len(eligibleBatters))
	for _, p := range eligibleBatters {
		isBatter[p] = true
	}

	for _, p := range eligible {
		id := s.Players[p].CricsheetID
		canBowl := isBowler[p] && bowlKnown(id)
		canBat := isBatter[p] && batKnown(id)
		switch {
		case canBowl || canBat:
			players = append(players, p)
			if canBowl {
				bowlers = append(bowlers, p)
			}
			if canBat {
				batters = append(batters, p)
			}
		default:
			excluded = append(excluded, p)
		}
	}
	return players, bowlers, batters, excluded
}

// Spell is one player's time at one team.
type Spell struct {
	Team        TeamID
	Innings     int
	FirstSeason uint16
	LastSeason  uint16
}

// Careers returns, for every player, the teams they have appeared for, ordered
// with the most recent first and ties broken by how long they were there.
//
// Cricsheet records the team per delivery rather than per player, because that
// is the only place the fact exists: a player belongs to whichever side he was
// batting or bowling for on the day. Reconstructing a career therefore means a
// scan, which at this size is a few milliseconds and needs no index.
//
// Franchises that renamed themselves are left as the corpus recorded them here.
// Folding "Kings XI Punjab" into "Punjab Kings" is a presentation decision and
// belongs where the name is shown, not where the history is read.
func (s *Store) Careers() [][]Spell {
	type key struct {
		p PlayerID
		t TeamID
	}
	seen := make(map[key]*Spell)
	lastInn := make(map[key]int32)

	note := func(p PlayerID, t TeamID, inn InningsID, season uint16) {
		if p == NoPlayer || t == NoTeam {
			return
		}
		k := key{p, t}
		sp, ok := seen[k]
		if !ok {
			sp = &Spell{Team: t, FirstSeason: season, LastSeason: season}
			seen[k] = sp
			lastInn[k] = -1
		}
		if lastInn[k] != int32(inn) {
			lastInn[k] = int32(inn)
			sp.Innings++
		}
		if season < sp.FirstSeason {
			sp.FirstSeason = season
		}
		if season > sp.LastSeason {
			sp.LastSeason = season
		}
	}

	for i := range s.D.Innings {
		inn := s.D.Innings[i]
		if s.Inn.SuperOver[inn] {
			continue
		}
		season := s.M.Season[s.Inn.Match[inn]]
		note(s.D.Batter[i], s.Inn.BattingTeam[inn], inn, season)
		note(s.D.NonStriker[i], s.Inn.BattingTeam[inn], inn, season)
		note(s.D.Bowler[i], s.Inn.BowlingTeam[inn], inn, season)
	}

	out := make([][]Spell, len(s.Players))
	for k, sp := range seen {
		out[k.p] = append(out[k.p], *sp)
	}
	for _, spells := range out {
		sort.Slice(spells, func(i, j int) bool {
			if spells[i].LastSeason != spells[j].LastSeason {
				return spells[i].LastSeason > spells[j].LastSeason
			}
			return spells[i].Innings > spells[j].Innings
		})
	}
	return out
}
