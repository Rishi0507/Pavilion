package api

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"manhattan/internal/corpus"
	"manhattan/internal/engine"
	"manhattan/internal/sim"
)

// The three ways to play.
//
// Daily is one problem shared by everyone, and it is the only one that counts
// towards the day's numbers or a streak. That restriction is the whole reason
// the shared figures mean anything: if a player could reroll until the
// situation suited them, "68% defended it" would describe nothing.
//
// Practice draws from a pool of situations validated exactly like a daily
// puzzle, so it is a real game, just not a shared one. Pick your XI hands over
// the squad selection as well, under a budget that cannot afford five good
// bowlers, so choosing the side is itself a decision.

// Mode is which of the three a run belongs to.
type Mode string

const (
	ModeDaily    Mode = "daily"
	ModePractice Mode = "practice"
	ModeDraft    Mode = "draft"
)

// Counts reports whether a run's result belongs in the shared numbers.
func (m Mode) Counts() bool { return m == ModeDaily }

type startRequest struct {
	Mode Mode `json:"mode"`

	// Draft mode only: the squad the player picked.
	BowlerIDs []uint16 `json:"bowler_ids"`
	BatterIDs []uint16 `json:"batter_ids"`

	// Practice mode only: the situation just played, so the next one differs.
	Avoid string `json:"avoid"`
}

// DraftView is everything needed to pick a side.
type DraftView struct {
	Bowlers      []engine.Rated `json:"bowlers"`
	Batters      []engine.Rated `json:"batters"`
	BowlerBudget int            `json:"bowler_budget"`
	BatterBudget int            `json:"batter_budget"`
	PickBowlers  int            `json:"pick_bowlers"`
	PickBatters  int            `json:"pick_batters"`
	Target       int            `json:"target"`
	Venue        string         `json:"venue"`
	Ground       engine.Ground  `json:"ground"`
}

func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request) {
	bowl, bat := s.Engine.DraftPool(0, 0)
	target, venue := s.draftSituation()

	writeJSON(w, http.StatusOK, DraftView{
		Bowlers:      bowl,
		Batters:      bat,
		BowlerBudget: engine.BowlerBudget,
		BatterBudget: engine.BatterBudget,
		PickBowlers:  engine.SquadBowlers,
		PickBatters:  engine.SquadBatters,
		Target:       target,
		Venue:        s.Engine.VenueName(venue),
		Ground:       engine.GroundOf(s.Engine.VenueName(venue)),
	})
}

// draftSituation picks the target and ground a drafted side will face.
//
// It comes from the validated pool when one exists, so the score is one that
// has been shown to make a contest, rather than a number chosen because it
// sounded about right.
func (s *Server) draftSituation() (int, corpus.VenueID) {
	if s.Pool.Len() > 0 {
		if q, ok := s.Pool.Pick("", s.rng()); ok {
			return int(q.Target), corpus.VenueID(q.VenueID)
		}
	}
	return 190, 0
}

func (s *Server) rng() *rand.Rand {
	return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x9E3779B9))
}

// buildForMode assembles the puzzle a new run will use.
func (s *Server) buildForMode(req startRequest) (*sim.Puzzle, sim.DailyKey, Mode, bool, error) {
	switch req.Mode {
	case ModePractice:
		return s.buildPractice(req.Avoid)
	case ModeDraft:
		return s.buildDraft(req)
	default:
		date := s.today()
		p, key, validated, err := s.puzzleFor(date)
		return p, key, ModeDaily, validated, err
	}
}

// buildPractice draws a validated situation from the pool.
//
// The key is fresh for every run, so replaying the same situation gives a
// different match. That is the opposite of the daily rule and deliberately so:
// the daily key is fixed because everyone must face the same luck, and a
// practice key is not because nobody is being compared.
func (s *Server) buildPractice(avoid string) (*sim.Puzzle, sim.DailyKey, Mode, bool, error) {
	if s.Pool.Len() == 0 {
		return nil, sim.DailyKey{}, ModePractice, false,
			fmt.Errorf("no practice situations available yet")
	}
	q, ok := s.Pool.Pick(avoid, s.rng())
	if !ok {
		return nil, sim.DailyKey{}, ModePractice, false,
			fmt.Errorf("no practice situations available yet")
	}

	key, err := sim.DeriveDailyKey(s.Secret,
		fmt.Sprintf("practice/%s/%d", q.Date, time.Now().UnixNano()))
	if err != nil {
		return nil, key, ModePractice, false, err
	}

	p, err := s.Engine.FromQueued(q.Date, q.Target, corpus.VenueID(q.VenueID), q.AttackIDs, q.BattingIDs)
	return p, key, ModePractice, true, err
}

// buildDraft assembles a puzzle around the squad the player picked.
func (s *Server) buildDraft(req startRequest) (*sim.Puzzle, sim.DailyKey, Mode, bool, error) {
	bowl, bat := s.Engine.DraftPool(0, 0)

	if err := checkSquad(req.BowlerIDs, bowl, engine.SquadBowlers, engine.BowlerBudget, "bowler"); err != nil {
		return nil, sim.DailyKey{}, ModeDraft, false, err
	}
	if err := checkSquad(req.BatterIDs, bat, engine.SquadBatters, engine.BatterBudget, "batter"); err != nil {
		return nil, sim.DailyKey{}, ModeDraft, false, err
	}

	target, venue := s.draftSituation()

	// The batting order needs eleven names, and the player picks six, so the
	// rest of the order is filled with the cheapest available. A side is more
	// than its top six and a chase that loses six wickets still has to be
	// finished by somebody.
	batting := append([]uint16(nil), req.BatterIDs...)
	picked := map[uint16]bool{}
	for _, id := range batting {
		picked[id] = true
	}
	for i := len(bat) - 1; i >= 0 && len(batting) < 11; i-- {
		id := uint16(bat[i].ID)
		if !picked[id] {
			batting = append(batting, id)
			picked[id] = true
		}
	}

	key, err := sim.DeriveDailyKey(s.Secret, fmt.Sprintf("draft/%d", time.Now().UnixNano()))
	if err != nil {
		return nil, key, ModeDraft, false, err
	}
	p, err := s.Engine.FromQueued("draft", uint16(target), venue, req.BowlerIDs, batting)
	return p, key, ModeDraft, false, err
}

// checkSquad validates a submitted selection server-side.
//
// The budget is a rule of the game, so it is enforced here rather than trusted
// to the page that displayed it. A client that sends five of the best bowlers
// is refused, however convincing its own arithmetic was.
func checkSquad(ids []uint16, pool []engine.Rated, want, budget int, what string) error {
	if len(ids) != want {
		return fmt.Errorf("pick exactly %d %ss, not %d", want, what, len(ids))
	}
	cost := map[uint16]int{}
	for _, r := range pool {
		cost[uint16(r.ID)] = r.Cost
	}

	total := 0
	seen := map[uint16]bool{}
	for _, id := range ids {
		c, ok := cost[id]
		if !ok {
			return fmt.Errorf("%s %d is not in the pool", what, id)
		}
		if seen[id] {
			return fmt.Errorf("the same %s was picked twice", what)
		}
		seen[id] = true
		total += c
	}
	if total > budget {
		return fmt.Errorf("that %s squad costs %d credits, and the budget is %d", what, total, budget)
	}
	return nil
}

func decodeStart(w http.ResponseWriter, r *http.Request) (startRequest, error) {
	var req startRequest
	if r.Body == nil {
		return req, nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	if err := dec.Decode(&req); err != nil {
		// An empty body is the ordinary way to start a daily run.
		return startRequest{Mode: ModeDaily}, nil
	}
	if req.Mode == "" {
		req.Mode = ModeDaily
	}
	return req, nil
}
