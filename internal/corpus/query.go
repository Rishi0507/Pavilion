package corpus

// Aggregation over the corpus is a linear scan. At ~295k deliveries and eight
// bytes of hot columns per delivery, a full pass costs single-digit
// milliseconds, which is why there is no index here and no query planner.

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
