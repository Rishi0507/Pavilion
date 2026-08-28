package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"manhattan/internal/attr"
	"manhattan/internal/corpus"
)

// builder accumulates the corpus and the quality report in a single pass over
// the match files.
type builder struct {
	st corpus.Store
	q  Quality

	teamIdx  map[string]corpus.TeamID
	venueIdx map[string]corpus.VenueID
	cityIdx  map[string]corpus.CityID

	// Entity resolution state, keyed by Cricsheet person identifier.
	idNames   map[string]map[string]int
	nameIDs   map[string]map[string]struct{}
	playerIdx map[string]corpus.PlayerID

	// From the people register.
	cricinfoID map[string]string
	registerNm map[string]string

	// The attribute table, when one has been built. Nil until parattr has run.
	attrs attr.Table
}

func newBuilder() *builder {
	b := &builder{
		teamIdx:    map[string]corpus.TeamID{},
		venueIdx:   map[string]corpus.VenueID{},
		cityIdx:    map[string]corpus.CityID{},
		idNames:    map[string]map[string]int{},
		nameIDs:    map[string]map[string]struct{}{},
		playerIdx:  map[string]corpus.PlayerID{},
		cricinfoID: map[string]string{},
		registerNm: map[string]string{},
	}
	b.q.Source.DataVersions = map[string]int{}
	b.q.Matches.BySeason = map[string]int{}
	b.q.Deliveries.ByPhase = map[string]int{}
	b.q.Entity.UnregisteredNames = map[string]int{}
	b.q.Coverage = map[string]float64{}
	return b
}

// intern maps a string to a small dense id, growing the dictionary as needed.
// It is a free function rather than a method because Go does not permit type
// parameters on methods.
func intern[T ~uint8](s string, idx map[string]T, list *[]string, what string) (T, error) {
	if v, ok := idx[s]; ok {
		return v, nil
	}
	if len(*list) > 254 {
		return 0, fmt.Errorf("more than 254 distinct %s; widen the id type", what)
	}
	v := T(len(*list))
	idx[s] = v
	*list = append(*list, s)
	return v, nil
}

// loadRegister reads Cricsheet's people register, the spine of entity
// resolution. It supplies the canonical unique_name and the Cricinfo id needed
// later to source batting handedness and bowling type, neither of which
// Cricsheet carries.
func (b *builder) loadRegister(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open people register: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return fmt.Errorf("read people register header: %w", err)
	}
	cID, cUniq := slices.Index(head, "identifier"), slices.Index(head, "unique_name")
	cCricinfo := slices.Index(head, "key_cricinfo")
	if cID < 0 || cUniq < 0 {
		return errors.New("people register missing identifier/unique_name columns")
	}

	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read people register: %w", err)
		}
		id := rec[cID]
		if cUniq < len(rec) {
			b.registerNm[id] = rec[cUniq]
		}
		if cCricinfo >= 0 && cCricinfo < len(rec) {
			b.cricinfoID[id] = rec[cCricinfo]
		}
	}
	b.q.Entity.RegisterRows = len(b.registerNm)
	return nil
}

// parseAll reads every match file and returns them in chronological order, so
// that the corpus is time-ordered and a held-out-season split is a plain suffix.
func parseAll(dir string) ([]*csMatch, []string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no match files in %s", dir)
	}

	type rec struct {
		m    *csMatch
		file string
	}
	recs := make([]rec, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", p, err)
		}
		var m csMatch
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", filepath.Base(p), err)
		}
		recs = append(recs, rec{&m, filepath.Base(p)})
	}

	sort.SliceStable(recs, func(a, c int) bool {
		x, y := recs[a].m.Info.Dates, recs[c].m.Info.Dates
		dx, dy := "", ""
		if len(x) > 0 {
			dx = x[0]
		}
		if len(y) > 0 {
			dy = y[0]
		}
		if dx != dy {
			return dx < dy
		}
		return recs[a].file < recs[c].file
	})

	ms := make([]*csMatch, len(recs))
	files := make([]string, len(recs))
	for i, r := range recs {
		ms[i], files[i] = r.m, r.file
	}
	return ms, files, nil
}

// observe records a display-name-to-identifier sighting for entity resolution.
func (b *builder) observe(name, id string) {
	if b.idNames[id] == nil {
		b.idNames[id] = map[string]int{}
	}
	b.idNames[id][name]++
	if b.nameIDs[name] == nil {
		b.nameIDs[name] = map[string]struct{}{}
	}
	b.nameIDs[name][id] = struct{}{}
}

// scanRegistries is the first pass: it sees every name-to-identifier pairing so
// that PlayerIDs can be assigned before any delivery is written.
func (b *builder) scanRegistries(ms []*csMatch) {
	for _, m := range ms {
		for name, id := range m.Info.Registry.People {
			b.observe(name, id)
		}
	}
}

// resolvePlayers assigns dense PlayerIDs once every sighting is in. Ordering is
// by Cricsheet identifier so the assignment is stable across runs, which keeps
// the corpus binary byte-reproducible.
func (b *builder) resolvePlayers() {
	ids := make([]string, 0, len(b.idNames))
	for id := range b.idNames {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > int(corpus.NoPlayer) {
		b.q.warn("%d players exceeds the PlayerID range", len(ids))
	}

	b.st.Players = make([]corpus.Player, len(ids))
	withCricinfo := 0
	for i, id := range ids {
		aliases := make([]string, 0, len(b.idNames[id]))
		for n := range b.idNames[id] {
			aliases = append(aliases, n)
		}
		sort.Strings(aliases)

		// Prefer the register's unique_name; otherwise the most frequently
		// observed display name, tie-broken by length then lexically.
		name := b.registerNm[id]
		if name == "" {
			best := ""
			for _, n := range aliases {
				switch {
				case best == "",
					b.idNames[id][n] > b.idNames[id][best],
					b.idNames[id][n] == b.idNames[id][best] && len(n) > len(best):
					best = n
				}
			}
			name = best
			b.q.Entity.MissingFromRegister = append(b.q.Entity.MissingFromRegister, id)
		}
		if b.cricinfoID[id] != "" {
			withCricinfo++
		}
		b.st.Players[i] = corpus.Player{
			CricsheetID: id,
			Name:        name,
			Aliases:     aliases,
			CricinfoID:  b.cricinfoID[id],
		}
		b.playerIdx[id] = corpus.PlayerID(i)
	}

	for name, set := range b.nameIDs {
		if len(set) < 2 {
			continue
		}
		list := make([]string, 0, len(set))
		for id := range set {
			list = append(list, id)
		}
		sort.Strings(list)
		b.q.Entity.AmbiguousNames = append(b.q.Entity.AmbiguousNames,
			AmbiguousName{Name: name, IDs: list})
	}
	sort.Slice(b.q.Entity.AmbiguousNames, func(i, j int) bool {
		return b.q.Entity.AmbiguousNames[i].Name < b.q.Entity.AmbiguousNames[j].Name
	})

	for _, id := range ids {
		if len(b.idNames[id]) < 2 {
			continue
		}
		names := make([]string, 0, len(b.idNames[id]))
		for n := range b.idNames[id] {
			names = append(names, n)
		}
		sort.Strings(names)
		b.q.Entity.MultiAlias = append(b.q.Entity.MultiAlias, AliasRecord{ID: id, Names: names})
	}

	b.q.Entity.Players = len(ids)
	if len(ids) > 0 {
		b.q.Entity.CricinfoIDCoverage = 100 * float64(withCricinfo) / float64(len(ids))
	}
	sort.Strings(b.q.Entity.MissingFromRegister)
}

// addMatch is the second pass: it writes one match's innings into the corpus.
func (b *builder) addMatch(m *csMatch, file string) error {
	info := &m.Info

	b.q.Source.DataVersions[m.Meta.DataVersion]++

	if info.MatchType != "T20" || info.BallsPerOver != 6 {
		b.q.Matches.Skipped = append(b.q.Matches.Skipped,
			fmt.Sprintf("%s: match_type=%q balls_per_over=%d", file, info.MatchType, info.BallsPerOver))
		return nil
	}
	if len(m.Innings) < 2 {
		// Abandoned before the second innings started. No usable chase.
		b.q.Integrity.AbandonedMatches++
		b.q.Matches.Skipped = append(b.q.Matches.Skipped,
			fmt.Sprintf("%s: %d innings (%s)", file, len(m.Innings), info.Outcome.Result))
		return nil
	}

	date, err := info.dateInt()
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	season, err := info.editionYear()
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if len(info.Teams) != 2 {
		return fmt.Errorf("%s: %d teams, want 2", file, len(info.Teams))
	}

	venue, err := intern(info.Venue, b.venueIdx, &b.st.Venues, "venues")
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	city, err := intern(info.City, b.cityIdx, &b.st.Cities, "cities")
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	teamA, err := intern(info.Teams[0], b.teamIdx, &b.st.Teams, "teams")
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	teamB, err := intern(info.Teams[1], b.teamIdx, &b.st.Teams, "teams")
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}

	toss := corpus.NoTeam
	if v, ok := b.teamIdx[info.Toss.Winner]; ok {
		toss = v
	}
	winner := corpus.NoTeam
	if v, ok := b.teamIdx[info.Outcome.Winner]; ok {
		winner = v
	}

	var mid uint32
	if _, err := fmt.Sscanf(strings.TrimSuffix(file, ".json"), "%d", &mid); err != nil {
		b.q.warn("%s: filename is not a numeric match id", file)
	}

	matchID := corpus.MatchID(b.st.M.Len())
	b.st.M.CricsheetID = append(b.st.M.CricsheetID, mid)
	b.st.M.Date = append(b.st.M.Date, date)
	b.st.M.Season = append(b.st.M.Season, season)
	b.st.M.Venue = append(b.st.M.Venue, venue)
	b.st.M.City = append(b.st.M.City, city)
	b.st.M.TeamA = append(b.st.M.TeamA, teamA)
	b.st.M.TeamB = append(b.st.M.TeamB, teamB)
	b.st.M.TossWinner = append(b.st.M.TossWinner, toss)
	b.st.M.TossField = append(b.st.M.TossField, info.Toss.Decision == "field")
	b.st.M.Winner = append(b.st.M.Winner, winner)
	b.st.M.Playoff = append(b.st.M.Playoff, info.stage() != "")

	b.q.Matches.Included++
	b.q.Matches.BySeason[fmt.Sprint(season)]++

	for ii := range m.Innings {
		if err := b.addInnings(m, matchID, ii, file); err != nil {
			return err
		}
	}
	return nil
}

// addInnings writes one innings, computing running state and cross-checking the
// legal-ball index against Cricsheet's own actual_delivery field.
func (b *builder) addInnings(m *csMatch, matchID corpus.MatchID, ii int, file string) error {
	in := &m.Innings[ii]
	reg := m.Info.Registry.People

	bat, ok := b.teamIdx[in.Team]
	if !ok {
		return fmt.Errorf("%s: innings team %q is not one of the match teams", file, in.Team)
	}
	bowl := b.st.M.TeamA[matchID]
	if bowl == bat {
		bowl = b.st.M.TeamB[matchID]
	}

	var target uint16
	if in.Target != nil {
		target = uint16(in.Target.Runs)
		b.q.Integrity.InningsWithTarget++
	}
	if in.SuperOver {
		b.q.Integrity.SuperOverInnings++
	}

	inningsID := corpus.InningsID(b.st.Inn.Len())
	start := uint32(b.st.D.Len())

	resolve := func(name string) corpus.PlayerID {
		id, ok := reg[name]
		if !ok {
			b.q.Entity.UnregisteredNames[name]++
			return corpus.NoPlayer
		}
		pid, ok := b.playerIdx[id]
		if !ok {
			b.q.Entity.UnregisteredNames[name]++
			return corpus.NoPlayer
		}
		return pid
	}

	var score uint16
	var wickets, legalBalls uint8

	for _, ov := range in.Overs {
		if ov.Over > 254 {
			return fmt.Errorf("%s: over index %d out of range", file, ov.Over)
		}
		over := uint8(ov.Over)
		var legalInOver uint8
		_, miscounted := in.MiscountedOvers[fmt.Sprint(ov.Over)]

		for bi, d := range ov.Deliveries {
			// Cross-check the independently computed legal-ball index against
			// Cricsheet's own. Drift here is the classic wides/no-balls
			// indexing bug, so it is recorded as an integrity failure rather
			// than silently tolerated. Umpire miscounts are exempt: there the
			// over genuinely did not contain six legal balls.
			if csOverIdx, csBall, err := legalBallOf(d.ActualDelivery); err != nil {
				b.q.warn("%s: %v", file, err)
			} else if !miscounted && (csOverIdx != ov.Over || csBall != int(legalInOver)) {
				b.q.Integrity.BallIndexMismatches++
				if len(b.q.Integrity.BallIndexSamples) < 20 {
					b.q.Integrity.BallIndexSamples = append(b.q.Integrity.BallIndexSamples,
						fmt.Sprintf("%s innings %d: actual_delivery %s, computed %d.%d",
							file, ii, d.ActualDelivery, ov.Over, legalInOver+1))
				}
			}

			if d.Runs.Batter+d.Runs.Extras != d.Runs.Total {
				b.q.Integrity.RunsTotalMismatches++
			}
			if len(d.Wickets) > 1 {
				b.q.Integrity.MultiWicketBalls++
			}

			kind := corpus.WicketNone
			out := corpus.NoPlayer
			if len(d.Wickets) > 0 {
				k, err := corpus.ParseWicketKind(d.Wickets[0].Kind)
				if err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				kind = k
				out = resolve(d.Wickets[0].PlayerOut)
			}

			legal := d.IsLegal()
			b.st.D.Innings = append(b.st.D.Innings, inningsID)
			b.st.D.Over = append(b.st.D.Over, over)
			b.st.D.BallInOver = append(b.st.D.BallInOver, uint8(bi))
			b.st.D.LegalBall = append(b.st.D.LegalBall, legalInOver)
			b.st.D.Legal = append(b.st.D.Legal, legal)
			b.st.D.Batter = append(b.st.D.Batter, resolve(d.Batter))
			b.st.D.NonStriker = append(b.st.D.NonStriker, resolve(d.NonStriker))
			b.st.D.Bowler = append(b.st.D.Bowler, resolve(d.Bowler))
			b.st.D.RunsBat = append(b.st.D.RunsBat, uint8(d.Runs.Batter))
			b.st.D.Wides = append(b.st.D.Wides, uint8(d.Extras.Wides))
			b.st.D.NoBalls = append(b.st.D.NoBalls, uint8(d.Extras.NoBalls))
			b.st.D.Byes = append(b.st.D.Byes, uint8(d.Extras.Byes))
			b.st.D.LegByes = append(b.st.D.LegByes, uint8(d.Extras.LegByes))
			b.st.D.Penalty = append(b.st.D.Penalty, uint8(d.Extras.Penalty))
			b.st.D.Wicket = append(b.st.D.Wicket, kind)
			b.st.D.PlayerOut = append(b.st.D.PlayerOut, out)
			b.st.D.ScoreBefore = append(b.st.D.ScoreBefore, score)
			b.st.D.WicketsBefore = append(b.st.D.WicketsBefore, wickets)
			b.st.D.LegalBallsBefore = append(b.st.D.LegalBallsBefore, legalBalls)

			b.q.Deliveries.Total++
			b.q.Deliveries.Wides += d.Extras.Wides
			b.q.Deliveries.NoBalls += d.Extras.NoBalls
			b.q.Deliveries.Byes += d.Extras.Byes
			b.q.Deliveries.LegByes += d.Extras.LegByes
			b.q.Deliveries.Penalty += d.Extras.Penalty
			if legal {
				b.q.Deliveries.Legal++
				legalInOver++
				legalBalls++
			}
			if kind != corpus.WicketNone {
				b.q.Deliveries.Wickets++
			}
			b.q.Deliveries.ByPhase[corpus.PhaseOf(over).String()]++

			score += uint16(d.Runs.Total)
			if kind.CostsWicket() {
				wickets++
			}
		}
	}

	for ov, mc := range in.MiscountedOvers {
		b.q.Integrity.MiscountedOvers = append(b.q.Integrity.MiscountedOvers,
			fmt.Sprintf("%s innings %d over %s: %d legal balls", file, ii, ov, mc.Balls))
	}
	if len(in.Overs) > 20 && !in.SuperOver {
		b.q.Integrity.NonStandardOverCount++
	}

	b.st.Inn.Match = append(b.st.Inn.Match, matchID)
	b.st.Inn.BattingTeam = append(b.st.Inn.BattingTeam, bat)
	b.st.Inn.BowlingTeam = append(b.st.Inn.BowlingTeam, bowl)
	b.st.Inn.Target = append(b.st.Inn.Target, target)
	b.st.Inn.SuperOver = append(b.st.Inn.SuperOver, in.SuperOver)
	b.st.Inn.Miscounted = append(b.st.Inn.Miscounted, len(in.MiscountedOvers) > 0)
	b.st.Inn.Start = append(b.st.Inn.Start, start)
	b.st.Inn.End = append(b.st.Inn.End, uint32(b.st.D.Len()))
	b.st.Inn.Runs = append(b.st.Inn.Runs, score)
	b.st.Inn.Wickets = append(b.st.Inn.Wickets, wickets)
	b.st.Inn.LegalBalls = append(b.st.Inn.LegalBalls, uint16(legalBalls))
	return nil
}

// finish computes the coverage percentages that CI gates on.
func (b *builder) finish(dir string, files int) {
	b.q.Source.Dir = dir
	b.q.Source.Files = files
	b.q.Source.Parsed = files
	b.q.Matches.Venues = len(b.st.Venues)
	b.q.Matches.Cities = len(b.st.Cities)
	b.q.Matches.Teams = len(b.st.Teams)

	n := float64(b.st.M.Len())
	pct := func(k int) float64 {
		if n == 0 {
			return 0
		}
		return 100 * float64(k) / n
	}

	var withCity, withVenue, withToss, withWinner int
	for i := range b.st.M.CricsheetID {
		if b.st.Cities[b.st.M.City[i]] != "" {
			withCity++
		}
		if b.st.Venues[b.st.M.Venue[i]] != "" {
			withVenue++
		}
		if b.st.M.TossWinner[i] != corpus.NoTeam {
			withToss++
		}
		if b.st.M.Winner[i] != corpus.NoTeam {
			withWinner++
		}
	}
	b.q.Coverage["match.city"] = pct(withCity)
	b.q.Coverage["match.venue"] = pct(withVenue)
	b.q.Coverage["match.toss_winner"] = pct(withToss)
	b.q.Coverage["match.result_winner"] = pct(withWinner)

	var namedBat, namedBowl int
	for i := range b.st.D.Batter {
		if b.st.D.Batter[i] != corpus.NoPlayer {
			namedBat++
		}
		if b.st.D.Bowler[i] != corpus.NoPlayer {
			namedBowl++
		}
	}
	if dn := float64(b.st.D.Len()); dn > 0 {
		b.q.Coverage["delivery.batter_resolved"] = 100 * float64(namedBat) / dn
		b.q.Coverage["delivery.bowler_resolved"] = 100 * float64(namedBowl) / dn
	}
	b.q.Coverage["player.cricinfo_id"] = b.q.Entity.CricinfoIDCoverage

	b.attributeCoverage()

	if b.q.Integrity.BallIndexMismatches > 0 {
		b.q.warn("%d deliveries where the computed legal-ball index disagrees with actual_delivery",
			b.q.Integrity.BallIndexMismatches)
	}
	if len(b.q.Entity.UnregisteredNames) > 0 {
		b.q.warn("%d display names appear in deliveries but not in their match registry",
			len(b.q.Entity.UnregisteredNames))
	}
	sort.Strings(b.q.Integrity.MiscountedOvers)
	sort.Strings(b.q.Matches.Skipped)
}

// attributeCoverage measures the two attributes Cricsheet does not carry.
//
// Coverage is deliberately measured against the eligible player set rather than
// every player in the corpus. A 2009 net bowler with forty deliveries will
// never be dealt, so his missing bowling type is not a defect; a missing type
// for a bowler the game can actually pick is.
func (b *builder) attributeCoverage() {
	eligible, bowlers, _ := b.st.Eligible(corpus.DefaultEligibility)
	b.q.Eligible.Players = len(eligible)
	b.q.Eligible.Bowlers = len(bowlers)
	b.q.Eligible.MinBallsBowled = corpus.DefaultEligibility.MinBallsBowled
	b.q.Eligible.MinBallsFaced = corpus.DefaultEligibility.MinBallsFaced

	if b.attrs == nil {
		b.q.Coverage["player.batting_hand"] = 0
		b.q.Coverage["player.bowling_class"] = 0
		b.q.warn("no attribute table found; run parattr. Batting handedness and " +
			"bowling type are absent from Cricsheet, and the matchup model " +
			"cannot be trained without them")
		return
	}

	var hands, classes int
	for _, p := range eligible {
		if _, ok := b.attrs.BatOf(b.st.Players[p].CricsheetID); ok {
			hands++
		}
	}
	for _, p := range bowlers {
		if _, ok := b.attrs.BowlOf(b.st.Players[p].CricsheetID); ok {
			classes++
		}
	}
	b.q.Eligible.BattingHandKnown = hands
	b.q.Eligible.BowlingClassKnown = classes

	if len(eligible) > 0 {
		b.q.Coverage["player.batting_hand"] = 100 * float64(hands) / float64(len(eligible))
	}
	if len(bowlers) > 0 {
		b.q.Coverage["player.bowling_class"] = 100 * float64(classes) / float64(len(bowlers))
	}

	for _, p := range b.attrs.All() {
		switch p.Provenance {
		case attr.Sourced:
			b.q.Eligible.Sourced++
		case attr.Manual:
			b.q.Eligible.Manual++
		case attr.Inferred:
			b.q.Eligible.InferredInTable++
		}
	}
	if b.q.Eligible.InferredInTable > 0 {
		b.q.warn("%d rows in the attribute table are marked inferred; inferred values "+
			"belong in the review file, not the main table", b.q.Eligible.InferredInTable)
	}
	if miss := len(eligible) - hands; miss > 0 {
		b.q.warn("%d of %d eligible players have no batting handedness", miss, len(eligible))
	}
	if miss := len(bowlers) - classes; miss > 0 {
		b.q.warn("%d of %d eligible bowlers have no bowling class", miss, len(bowlers))
	}

	b.dealablePool()
}

// dealablePool records the pool the game actually draws from, and every player
// excluded from it.
func (b *builder) dealablePool() {
	batKnown := func(id string) bool { _, ok := b.attrs.BatOf(id); return ok }
	bowlKnown := func(id string) bool { _, ok := b.attrs.BowlOf(id); return ok }

	players, bowlers, batters, excluded := b.st.Dealable(corpus.DefaultEligibility, batKnown, bowlKnown)
	b.q.Eligible.Dealable = len(players)
	b.q.Eligible.DealableBowlers = len(bowlers)
	b.q.Eligible.DealableBatters = len(batters)

	vols := b.st.Volumes()
	for _, p := range excluded {
		pl := b.st.Players[p]
		v := vols[p]

		var missing []string
		if _, ok := b.attrs.BatOf(pl.CricsheetID); !ok {
			missing = append(missing, "batting hand")
		}
		if v.BallsBowled >= corpus.DefaultEligibility.MinBallsBowled {
			if _, ok := b.attrs.BowlOf(pl.CricsheetID); !ok {
				missing = append(missing, "bowling class")
			}
		}
		b.q.Eligible.Excluded = append(b.q.Eligible.Excluded, ExcludedPlayer{
			CricsheetID: pl.CricsheetID,
			Name:        pl.Name,
			BallsBowled: v.BallsBowled,
			BallsFaced:  v.BallsFaced,
			LastSeason:  int(v.LastSeason),
			Missing:     strings.Join(missing, ", "),
		})
	}
	sort.Slice(b.q.Eligible.Excluded, func(i, j int) bool {
		a, c := b.q.Eligible.Excluded[i], b.q.Eligible.Excluded[j]
		if a.BallsBowled != c.BallsBowled {
			return a.BallsBowled > c.BallsBowled
		}
		return a.CricsheetID < c.CricsheetID
	})

	if n := len(b.q.Eligible.Excluded); n > 0 {
		b.q.warn("%d eligible players are excluded from the dealable pool for want of attributes", n)
	}
}
