// Package api serves the game over HTTP.
//
// The routes are the ones sketched in the brief. Every state transition is
// validated against the signed token and the decision counter before the engine
// is touched, and the client is never told anything it could use to compute an
// outcome ahead of choosing.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pavilion/internal/corpus"
	"pavilion/internal/engine"
	"pavilion/internal/puzzle"
	"pavilion/internal/session"
	"pavilion/internal/sim"
	"pavilion/internal/store"
)

// Server holds everything a request needs.
type Server struct {
	Engine   *engine.Engine
	Sessions *session.Store
	DB       *store.DB
	Queue    *puzzle.Queue
	Pool     *puzzle.Pool
	Secret   []byte
	Log      *slog.Logger

	// Now is injected so tests are not at the mercy of the clock.
	Now func() time.Time
}

// today returns the puzzle date in IST, which is when the day turns over for
// the audience this is built for.
func (s *Server) today() string {
	loc := time.FixedZone("IST", 5*3600+1800)
	return s.Now().In(loc).Format("2006-01-02")
}

// Routes builds the mux. Go's pattern-based routing is enough here; a framework
// would add a dependency for path parameters and nothing else.
func (s *Server) Routes(web http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/puzzle/today", s.handleToday)
	mux.HandleFunc("POST /api/v1/run", s.handleStartRun)
	mux.HandleFunc("POST /api/v1/run/{id}/defend/over", s.handleDefendOver)
	mux.HandleFunc("POST /api/v1/run/{id}/chase/over", s.handleChaseOver)
	mux.HandleFunc("POST /api/v1/run/{id}/finish", s.handleFinish)
	mux.HandleFunc("GET /api/v1/day/{date}/stats", s.handleDayStats)
	mux.HandleFunc("POST /api/v1/run/{id}/name", s.handleName)
	mux.HandleFunc("GET /api/v1/day/{date}/leaderboard", s.handleLeaderboard)
	mux.HandleFunc("GET /api/v1/draft", s.handleDraft)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.Handle("/", web)
	return s.recoverPanic(s.logRequests(mux))
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || rec.status >= 400 {
			s.Log.Info("request",
				"method", r.Method, "path", r.URL.Path,
				"status", rec.status, "took", time.Since(start).Round(time.Millisecond))
		}
	})
}

// recoverPanic keeps one bad request from taking the process down mid-game for
// everyone else.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic serving request", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// statusFor maps a session error onto the response the client should see.
func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		return http.StatusNotFound, "no such run"
	case errors.Is(err, session.ErrBadToken):
		return http.StatusForbidden, "invalid token"
	case errors.Is(err, session.ErrOutOfOrder):
		return http.StatusConflict, err.Error()
	case errors.Is(err, session.ErrRunFinished):
		return http.StatusConflict, "this run is already finished"
	case errors.Is(err, session.ErrWrongHalf):
		return http.StatusConflict, "that decision belongs to the other half"
	case errors.Is(err, sim.ErrIllegalBowler), errors.Is(err, sim.ErrNoAttacksLeft):
		return http.StatusUnprocessableEntity, err.Error()
	}
	return http.StatusInternalServerError, "something went wrong"
}

// PuzzleView is what a client may know before playing.
//
// It carries the target, the ground and the squads, and nothing else. In
// particular it does not carry the day's key: with the key, and the model, a
// client could compute every ball before choosing a bowler, and the entire game
// would be solvable offline.
type PuzzleView struct {
	Date      string        `json:"date"`
	Target    int           `json:"target"`
	Venue     string        `json:"venue"`
	Ground    engine.Ground `json:"ground"`
	Attack    []PlayerView  `json:"attack"`
	Batting   []PlayerView  `json:"batting"`
	Validated bool          `json:"validated"`

	// Mode tells the page which of the three games this is, and Counts whether
	// the result will join the day's shared numbers.
	Mode        string `json:"mode"`
	Counts      bool   `json:"counts"`
	SituationID string `json:"situation_id"`
}

// PlayerView is one cricketer as the client sees them.
type PlayerView struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Style  string `json:"style"`
	Hand   string `json:"hand"`
	Team   string `json:"team"`
	Colour string `json:"colour"`
	Years  string `json:"years"`
	Mark   string `json:"mark"`
}

func viewOfPlayer(i int, p sim.Player) PlayerView {
	return PlayerView{
		Index: i, Name: p.Name,
		Style: engine.ClassName(p.Class), Hand: engine.HandName(p.Hand),
		Team: p.Team, Colour: engine.TeamColourShort(p.Team),
		Years: p.Years, Mark: engine.Monogram(p.Name),
	}
}

func (s *Server) puzzleFor(date string) (*sim.Puzzle, sim.DailyKey, bool, error) {
	key, err := sim.DeriveDailyKey(s.Secret, date)
	if err != nil {
		return nil, key, false, err
	}
	if s.Queue != nil {
		if entry, ok := s.Queue.For(date); ok {
			p, err := s.Engine.FromQueued(date, entry.Target, corpus.VenueID(entry.VenueID),
				entry.AttackIDs, entry.BattingIDs)
			if err != nil {
				return nil, key, false, err
			}
			return p, key, true, nil
		}
	}
	// No validated puzzle for this date. One is generated so the server still
	// works, but it is flagged, because an unchecked day can easily be one
	// where everybody wins.
	//
	// The target is derived from the day's key rather than fixed, so that
	// consecutive unvalidated days at least differ from one another. It is
	// still a guess: only the Monte Carlo can say whether a score is a contest
	// against this particular attack.
	target := uint16(170 + int(key[11])%50)
	p, err := s.Engine.BuildPuzzle(date, key, target)
	return p, key, false, err
}

func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	date := s.today()
	p, _, validated, err := s.puzzleFor(date)
	if err != nil {
		s.Log.Error("build puzzle", "err", err)
		writeError(w, http.StatusInternalServerError, "no puzzle available")
		return
	}
	writeJSON(w, http.StatusOK, s.viewOf(date, p, validated))
}

func (s *Server) viewOf(date string, p *sim.Puzzle, validated bool) PuzzleView {
	venue := s.Engine.VenueName(p.Venue)
	v := PuzzleView{
		Date:      date,
		Target:    int(p.Target),
		Venue:     venue,
		Ground:    engine.GroundOf(venue),
		Validated: validated,
	}
	for i, b := range p.Attack {
		v.Attack = append(v.Attack, viewOfPlayer(i, b))
	}
	for i, b := range p.Batting {
		v.Batting = append(v.Batting, viewOfPlayer(i, b))
	}
	return v
}

// StartResponse opens a run.
type StartResponse struct {
	RunID     string     `json:"run_id"`
	Token     string     `json:"token"`
	Decisions int        `json:"decisions"`
	Puzzle    PuzzleView `json:"puzzle"`
	State     StateView  `json:"state"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	req, err := decodeStart(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed request")
		return
	}

	p, key, mode, validated, err := s.buildForMode(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Only a daily run is dated, because only a daily run is compared with
	// anyone else's.
	date := s.today()
	run, token, err := s.Sessions.Start(date, key, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a run")
		return
	}
	run.Mode = string(mode)
	run.Counts = mode.Counts()

	view := s.viewOf(date, p, validated)
	view.Mode = string(mode)
	view.Counts = run.Counts
	view.SituationID = p.Date

	writeJSON(w, http.StatusOK, StartResponse{
		RunID:     run.ID,
		Token:     token,
		Decisions: run.Decisions,
		Puzzle:    view,
		State:     s.stateOf(run),
	})
}

// StateView is the game as the client draws it.
type StateView struct {
	Half       string `json:"half"`
	Over       int    `json:"over"`
	Score      int    `json:"score"`
	Wickets    int    `json:"wickets"`
	Target     int    `json:"target"`
	RunsNeeded int    `json:"runs_needed"`
	BallsLeft  int    `json:"balls_left"`
	// Both batters are reported, not only the one on strike. A partnership is
	// two people, and which of them is at the other end matters to the next
	// decision.
	Striker          string   `json:"striker"`
	StrikerTeam      string   `json:"striker_team"`
	StrikerColour    string   `json:"striker_colour"`
	StrikerMark      string   `json:"striker_mark"`
	StrikerBalls     int      `json:"striker_balls"`
	StrikerRuns      int      `json:"striker_runs"`
	NonStriker       string   `json:"non_striker"`
	NonStrikerTeam   string   `json:"non_striker_team"`
	NonStrikerColour string   `json:"non_striker_colour"`
	NonStrikerMark   string   `json:"non_striker_mark"`
	NonStrikerBalls  int      `json:"non_striker_balls"`
	NonStrikerRuns   int      `json:"non_striker_runs"`
	LegalBowlers     []int    `json:"legal_bowlers"`
	OversBowled      []int    `json:"overs_bowled"`
	AttacksLeft      int      `json:"attacks_left"`
	WinProbability   float64  `json:"win_probability"`
	Done             bool     `json:"done"`
	Decisions        int      `json:"decisions"`
	DefendGrid       []string `json:"defend_grid"`
	ChaseGrid        []string `json:"chase_grid"`

	// The full batting order, so the page can show a scorecard rather than only
	// the two players at the crease. A side is eleven people, and who is still
	// to come is part of the decision: chasing with Dhoni padded up is a
	// different proposition from chasing with the tail.
	Batting []BatterView `json:"batting"`
}

// BatterView is one place in the batting order.
type BatterView struct {
	Name   string `json:"name"`
	Team   string `json:"team"`
	Colour string `json:"colour"`
	Mark   string `json:"mark"`
	Runs   int    `json:"runs"`
	Balls  int    `json:"balls"`

	// Status is "in", "out" or "yet", which is the whole of what a scorecard
	// says about somebody who is not currently batting.
	Status string `json:"status"`
}

// battingCard reads the order out of the simulator's state.
//
// A batter has come in once the innings has reached their position, and the two
// at the crease are known directly, so anyone who has come in and is not at the
// crease is out. That is the whole rule, and it keeps the card in step with the
// simulation rather than tracking dismissals a second time beside it.
func battingCard(st *sim.State) []BatterView {
	out := make([]BatterView, 0, len(st.Puzzle.Batting))
	for i, b := range st.Puzzle.Batting {
		status := "yet"
		switch {
		case i == st.Striker || i == st.NonStriker:
			status = "in"
			if st.Done {
				status = "out"
			}
		case i < st.NextBatter:
			status = "out"
		}
		out = append(out, BatterView{
			Name:   b.Name,
			Team:   b.Team,
			Colour: engine.TeamColourShort(b.Team),
			Mark:   engine.Monogram(b.Name),
			Runs:   int(st.RunsScored[i]),
			Balls:  int(st.BallsFaced[i]),
			Status: status,
		})
	}
	return out
}

func (s *Server) stateOf(run *session.Run) StateView {
	st := run.State
	// Slices are built explicitly rather than left nil, because a JSON API that
	// returns null where the client expects an array is a trap: every consumer
	// has to remember the special case, and the one that forgets crashes at
	// exactly the moment the innings ends.
	legal := st.LegalBowlers()
	if legal == nil {
		legal = []int{}
	}
	v := StateView{
		Half:         run.Half.String(),
		Over:         int(st.Over),
		Score:        int(st.Score),
		Wickets:      int(st.Wickets),
		Target:       int(st.Puzzle.Target),
		RunsNeeded:   st.RunsNeeded(),
		BallsLeft:    st.BallsLeft(),
		LegalBowlers: legal,
		AttacksLeft:  st.AttacksLeft(),
		Done:         st.Done,
		Decisions:    run.Decisions,
		DefendGrid:   gradesOf(run.DefendOvers),
		ChaseGrid:    gradesOf(run.ChaseOvers),
	}
	if !st.Done {
		on := st.Puzzle.Batting[st.Striker]
		off := st.Puzzle.Batting[st.NonStriker]
		v.Striker, v.StrikerTeam = on.Name, on.Team
		v.StrikerColour = engine.TeamColourShort(on.Team)
		v.StrikerMark = engine.Monogram(on.Name)
		v.StrikerBalls = int(st.BallsFaced[st.Striker])
		v.StrikerRuns = int(st.RunsScored[st.Striker])
		v.NonStriker, v.NonStrikerTeam = off.Name, off.Team
		v.NonStrikerColour = engine.TeamColourShort(off.Team)
		v.NonStrikerMark = engine.Monogram(off.Name)
		v.NonStrikerBalls = int(st.BallsFaced[st.NonStriker])
		v.NonStrikerRuns = int(st.RunsScored[st.NonStriker])
	}
	v.Batting = battingCard(st)

	v.OversBowled = make([]int, 0, len(st.OversBowled))
	for _, n := range st.OversBowled {
		v.OversBowled = append(v.OversBowled, int(n))
	}
	// The defending half reports the defence's chance; the chase reports the
	// chaser's. Both are the same model read from the relevant chair.
	if run.Half == session.Chasing {
		v.WinProbability = s.Engine.WinProbability(st)
	} else {
		v.WinProbability = s.Engine.DefenceProbability(st)
	}
	return v
}

func gradesOf(overs []session.Graded) []string {
	out := make([]string, 0, len(overs))
	for _, o := range overs {
		out = append(out, engine.GradeOver(o.Delta).Name())
	}
	return out
}

// OverResponse is one resolved over, returned as a delivery array so the client
// can animate it ball by ball. The client is told what happened; it never
// decides what happens.
type OverResponse struct {
	Over       int            `json:"over"`
	Bowler     string         `json:"bowler"`
	Intent     string         `json:"intent"`
	Deliveries []DeliveryView `json:"deliveries"`
	Runs       int            `json:"runs"`
	Wickets    int            `json:"wickets"`
	WPDelta    float64        `json:"wp_delta"`
	Grade      string         `json:"grade"`
	State      StateView      `json:"state"`
}

// DeliveryView is one ball.
type DeliveryView struct {
	Outcome string `json:"outcome"`
	Runs    int    `json:"runs"`
	Legal   bool   `json:"legal"`
	Wicket  bool   `json:"wicket"`
	Batter  string `json:"batter"`
}

type overRequest struct {
	BowlerID  *int   `json:"bowler_id"`
	Intent    string `json:"intent"`
	Decisions int    `json:"decisions"`
}

func (s *Server) handleDefendOver(w http.ResponseWriter, r *http.Request) {
	s.playOver(w, r, session.Defending)
}

func (s *Server) handleChaseOver(w http.ResponseWriter, r *http.Request) {
	s.playOver(w, r, session.Chasing)
}

// playOver is the whole state transition, and the only place the engine is
// driven from a request.
func (s *Server) playOver(w http.ResponseWriter, r *http.Request, half session.Half) {
	var req overRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request")
		return
	}

	runID := r.PathValue("id")
	token := bearer(r)

	run, release, err := s.Sessions.Authorise(runID, token, req.Decisions)
	if err != nil {
		code, msg := statusFor(err)
		writeError(w, code, msg)
		return
	}
	defer release()

	if run.Half == session.Finished {
		writeError(w, http.StatusConflict, "this run is already finished")
		return
	}
	if run.Half != half {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("this run is in the %s half", run.Half))
		return
	}

	st := run.State
	before := s.Engine.DefenceProbability(st)
	if half == session.Chasing {
		before = s.Engine.WinProbability(st)
	}

	var bowler int
	var intent sim.Intent
	var value float64

	if half == session.Defending {
		// The player picks the bowler; the AI side decides how to bat.
		if req.BowlerID == nil {
			writeError(w, http.StatusBadRequest, "bowler_id is required when defending")
			return
		}
		bowler = *req.BowlerID
		intent, err = s.Engine.ChooseIntent(st)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not resolve the over")
			return
		}
		// Scored before the over is played, against the alternatives that were
		// available, so the number reflects the choice and not the dice.
		value = s.Engine.DecisionValue(st, true, bowler, intent)
	} else {
		// The player picks the intent; the AI captain picks the bowler.
		intent, err = parseIntent(req.Intent)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		bowler = s.Engine.CaptainPick(st)
		value = s.Engine.DecisionValue(st, false, bowler, intent)
	}

	over, err := sim.PlayOver(st, run.Key, bowler, intent, s.Engine)
	if err != nil {
		code, msg := statusFor(err)
		writeError(w, code, msg)
		return
	}

	after := s.Engine.DefenceProbability(st)
	if half == session.Chasing {
		after = s.Engine.WinProbability(st)
	}
	delta := after - before

	run.Decisions++
	g := session.Graded{Over: over, Delta: delta, Value: value}
	if half == session.Defending {
		run.DefendOvers = append(run.DefendOvers, g)
	} else {
		run.ChaseOvers = append(run.ChaseOvers, g)
	}

	// The half ends when the innings does, and the chase half starts from the
	// same target with the day's key offset so the two do not share luck.
	if st.Done {
		if half == session.Defending {
			run.DefendResult = st.Result()
			run.Half = session.Chasing
			chaseKey := run.Key
			chaseKey[27] ^= 0xC5
			run.Key = chaseKey
			run.State = sim.NewPlayerChase(st.Puzzle)
		} else {
			run.ChaseResult = st.Result()
			run.Half = session.Finished
		}
	}

	writeJSON(w, http.StatusOK, OverResponse{
		Over:       int(over.Number) + 1,
		Bowler:     over.Bowler.Name,
		Intent:     over.Intent.String(),
		Deliveries: deliveriesOf(over),
		Runs:       int(over.Runs),
		Wickets:    int(over.Wickets),
		WPDelta:    delta,
		Grade:      engine.GradeOver(delta).Name(),
		State:      s.stateOf(run),
	})
}

func deliveriesOf(o sim.Over) []DeliveryView {
	out := make([]DeliveryView, 0, len(o.Deliveries))
	for _, d := range o.Deliveries {
		out = append(out, DeliveryView{
			Outcome: d.Outcome.String(),
			Runs:    int(d.Runs),
			Legal:   d.Legal,
			Wicket:  d.Wicket,
			Batter:  d.Batter.Name,
		})
	}
	return out
}

func parseIntent(s string) (sim.Intent, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "block", "b":
		return sim.Block, nil
	case "rotate", "r", "":
		return sim.Rotate, nil
	case "attack", "a":
		return sim.Attack, nil
	}
	return sim.Rotate, fmt.Errorf("intent must be block, rotate or attack")
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return after
	}
	return r.Header.Get("X-Pavilion-Token")
}

// FinishResponse is the share card.
type FinishResponse struct {
	Mode       string   `json:"mode"`
	Counts     bool     `json:"counts"`
	Date       string   `json:"date"`
	Target     int      `json:"target"`
	Defended   bool     `json:"defended"`
	Chased     bool     `json:"chased"`
	DefendGrid []string `json:"defend_grid"`
	ChaseGrid  []string `json:"chase_grid"`
	// A margin is runs when a side fell short and wickets in hand when it got
	// there, because those are the two ways cricket reports a result and
	// neither substitutes for the other. Reporting only the runs meant a chase
	// that succeeded was described as "short by 0 runs".
	DefendMargin     int            `json:"defend_margin"`
	DefendMarginWkts int            `json:"defend_margin_wkts"`
	ChaseMargin      int            `json:"chase_margin"`
	ChaseMarginWkts  int            `json:"chase_margin_wkts"`
	DefendScore      float64        `json:"defend_score"`
	ChaseScore       float64        `json:"chase_score"`
	TotalScore       float64        `json:"total_score"`
	Percentile       float64        `json:"percentile"`
	Streak           int            `json:"streak"`
	Share            string         `json:"share"`
	Day              store.DayStats `json:"day"`
}

type finishRequest struct {
	Decisions int    `json:"decisions"`
	Player    string `json:"player"`
}

func (s *Server) handleFinish(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request")
		return
	}

	run, release, err := s.Sessions.Authorise(r.PathValue("id"), bearer(r), req.Decisions)
	if err != nil {
		code, msg := statusFor(err)
		writeError(w, code, msg)
		return
	}

	if run.Half != session.Finished {
		release()
		writeError(w, http.StatusConflict, "the run is not finished yet")
		return
	}

	defendScore := scoreOf(run.DefendOvers)
	chaseScore := scoreOf(run.ChaseOvers)
	res := store.Result{
		RunID:        run.ID,
		Date:         run.Date,
		Player:       sanitisePlayer(req.Player),
		Defended:     run.DefendResult.Defended,
		Chased:       run.ChaseResult.TargetMet,
		DefendScore:  defendScore,
		ChaseScore:   chaseScore,
		DefendGrid:   emoji(gradesOf(run.DefendOvers)),
		ChaseGrid:    emoji(gradesOf(run.ChaseOvers)),
		DefendMargin: run.DefendResult.MarginRuns,
		ChaseMargin:  run.ChaseResult.MarginRuns,
	}
	// The wickets side of each margin is reported but not recorded: it is how
	// the result reads on the day, and nothing that is kept needs it.
	defendMarginWkts := run.DefendResult.MarginWkts
	chaseMarginWkts := run.ChaseResult.MarginWkts
	date := run.Date
	target := int(run.State.Puzzle.Target)
	defendGrid := gradesOf(run.DefendOvers)
	chaseGrid := gradesOf(run.ChaseOvers)
	counts := run.Counts
	mode := run.Mode
	release()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// Only the daily run joins the shared numbers. Recording practice would
	// let anyone move the day's percentages by replaying situations of their
	// own choosing, which would make the one figure the game is built around
	// mean nothing.
	if counts {
		if err := s.DB.Save(ctx, res); err != nil && !errors.Is(err, store.ErrAlreadyPlayed) {
			s.Log.Error("save result", "err", err)
		}
	}

	day, err := s.DB.Day(ctx, date)
	if err != nil {
		s.Log.Error("day stats", "err", err)
	}
	pct, err := s.DB.Percentile(ctx, date, defendScore+chaseScore)
	if err != nil {
		s.Log.Error("percentile", "err", err)
	}
	streak, err := s.DB.StreakOf(ctx, res.Player, date)
	if err != nil {
		s.Log.Error("streak", "err", err)
	}

	resp := FinishResponse{
		Mode:             mode,
		Counts:           counts,
		Date:             date,
		Target:           target,
		Defended:         res.Defended,
		Chased:           res.Chased,
		DefendGrid:       defendGrid,
		ChaseGrid:        chaseGrid,
		DefendMargin:     res.DefendMargin,
		DefendMarginWkts: defendMarginWkts,
		ChaseMargin:      res.ChaseMargin,
		ChaseMarginWkts:  chaseMarginWkts,
		DefendScore:      defendScore,
		ChaseScore:       chaseScore,
		TotalScore:       defendScore + chaseScore,
		Percentile:       pct,
		Streak:           streak,
		Day:              day,
	}
	resp.Share = ShareText(resp)
	writeJSON(w, http.StatusOK, resp)
}

// scoreOf sums what the player's decisions were worth, in probability points.
//
// It sums Value rather than Delta deliberately. Summing Delta telescopes to the
// final probability minus the starting one, which is just the result, so it
// would rank a player who won on luck above one who played well and lost.
func scoreOf(overs []session.Graded) float64 {
	total := 0.0
	for _, o := range overs {
		total += o.Value
	}
	return 100 * total
}

// sanitisePlayer keeps a display name to something safe and short. Accounts are
// optional by design, so an empty name is normal rather than an error.
func sanitisePlayer(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > 24 {
		name = name[:24]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == ' ':
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func (s *Server) handleDayStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stats, err := s.DB.Day(ctx, r.PathValue("date"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the day")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleName attaches a display name to a run that has already finished.
//
// The name is asked for only once the puzzle is solved, which is necessarily
// after the result has been recorded, so it arrives on its own. The run
// identifier is the authority: it is a random value known only to the session
// that played the run, so holding it is what entitles a caller to name it.
func (s *Server) handleName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Player string `json:"player"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request")
		return
	}

	name := sanitisePlayer(req.Player)
	if name == "" {
		writeError(w, http.StatusUnprocessableEntity, "that name is empty once tidied up")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.DB.SetPlayer(ctx, r.PathValue("id"), name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no such run")
			return
		}
		s.Log.Error("set player", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save that name")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"player": name})
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, err := s.DB.Leaderboard(ctx, r.PathValue("date"), 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the leaderboard")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": r.PathValue("date"), "leaders": rows})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"live_runs": s.Sessions.Count(),
		"date":      s.today(),
	})
}
