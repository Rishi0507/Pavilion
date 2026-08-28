package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"manhattan/internal/engine"
	"manhattan/internal/session"
	"manhattan/internal/sim"
	"manhattan/internal/store"
)

// harness builds a server against the real engine, because the parts worth
// testing here are the ones that only exist when the whole thing is wired up:
// token checking, the decision counter, and the handover between halves.
func harness(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()

	root := filepath.Join("..", "..")
	p := engine.DefaultPaths()
	p.Corpus = filepath.Join(root, p.Corpus)
	p.Attributes = filepath.Join(root, p.Attributes)
	p.Model = filepath.Join(root, p.Model)
	p.ModelMeta = filepath.Join(root, p.ModelMeta)
	p.WinProb = filepath.Join(root, p.WinProb)
	p.WinMeta = filepath.Join(root, p.WinMeta)
	if _, err := os.Stat(p.Model); err != nil {
		t.Skipf("model artifacts not built; run make model")
	}

	eng, err := engine.New(p)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sessions, err := session.NewStore([]byte("test-signing-secret-0123456789"), time.Hour)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	srv := &Server{
		Engine:   eng,
		Sessions: sessions,
		DB:       db,
		Secret:   []byte("test-master-secret"),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) },
	}
	ts := httptest.NewServer(srv.Routes(http.NotFoundHandler()))
	t.Cleanup(ts.Close)
	return ts, srv
}

func do(t *testing.T, ts *httptest.Server, method, path, token string, body any, out any) int {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Par-Token", token)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil && res.StatusCode < 400 {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return res.StatusCode
}

func TestPuzzleTodayHidesNothingItShould(t *testing.T) {
	ts, _ := harness(t)

	var view PuzzleView
	if code := do(t, ts, "GET", "/api/v1/puzzle/today", "", nil, &view); code != 200 {
		t.Fatalf("status %d", code)
	}
	if view.Target <= 0 || view.Venue == "" {
		t.Errorf("puzzle view is incomplete: %+v", view)
	}
	if len(view.Attack) != 5 {
		t.Errorf("attack has %d bowlers, want 5", len(view.Attack))
	}

	// The response must not carry anything that would let a client compute the
	// match itself. The daily key is the one secret that matters.
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"key", "seed", "secret"} {
		if bytes.Contains(bytes.ToLower(raw), []byte(forbidden)) {
			t.Errorf("puzzle view mentions %q: %s", forbidden, raw)
		}
	}
}

// TestFullRun plays a whole day through the API, both halves, and checks that
// the server drives the transitions rather than trusting the client to.
func TestFullRun(t *testing.T) {
	ts, _ := harness(t)

	var start StartResponse
	if code := do(t, ts, "POST", "/api/v1/run", "", map[string]any{}, &start); code != 200 {
		t.Fatalf("start: status %d", code)
	}
	if start.RunID == "" || start.Token == "" {
		t.Fatal("start did not return a run and a token")
	}

	state := start.State
	decisions := start.Decisions
	overs := 0

	for state.Half != "finished" && overs < 60 {
		var r OverResponse
		var code int

		if state.Half == "defend" {
			if len(state.LegalBowlers) == 0 {
				t.Fatalf("no legal bowler at over %d but the innings continues", state.Over)
			}
			code = do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over", start.Token,
				map[string]any{"bowler_id": state.LegalBowlers[0], "decisions": decisions}, &r)
		} else {
			intent := "rotate"
			if state.AttacksLeft > 0 && state.Over >= 15 {
				intent = "attack"
			}
			code = do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/chase/over", start.Token,
				map[string]any{"intent": intent, "decisions": decisions}, &r)
		}
		if code != 200 {
			t.Fatalf("over %d (%s): status %d", state.Over, state.Half, code)
		}
		// A full over is at least six deliveries, but an over ends the moment
		// the chase is won or the last wicket falls, so a short final over is
		// correct rather than a bug.
		lastOfInnings := r.State.Half != state.Half || r.State.Done
		if len(r.Deliveries) < 6 && !lastOfInnings {
			t.Errorf("over %d returned %d deliveries mid-innings", r.Over, len(r.Deliveries))
		}
		legal := 0
		for _, d := range r.Deliveries {
			if d.Legal {
				legal++
			}
		}
		if legal > 6 {
			t.Errorf("over %d contained %d legal balls", r.Over, legal)
		}
		if r.Grade == "" {
			t.Errorf("over %d has no grade", r.Over)
		}

		state = r.State
		decisions = state.Decisions
		overs++
	}

	if state.Half != "finished" {
		t.Fatalf("run did not finish after %d overs", overs)
	}
	if len(state.DefendGrid) == 0 || len(state.ChaseGrid) == 0 {
		t.Errorf("grids are incomplete: %d defend, %d chase", len(state.DefendGrid), len(state.ChaseGrid))
	}

	var fin FinishResponse
	if code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/finish", start.Token,
		map[string]any{"decisions": decisions, "player": "tester"}, &fin); code != 200 {
		t.Fatalf("finish: status %d", code)
	}
	if fin.Share == "" {
		t.Error("finish returned no share text")
	}
	if fin.Day.Runs != 1 {
		t.Errorf("day records %d runs, want 1", fin.Day.Runs)
	}

	var day store.DayStats
	if code := do(t, ts, "GET", "/api/v1/day/"+fin.Date+"/stats", "", nil, &day); code != 200 {
		t.Fatalf("day stats: status %d", code)
	}
	if day.Runs != 1 {
		t.Errorf("day stats report %d runs, want 1", day.Runs)
	}
}

// TestServerIsAuthoritative covers the rules that stop a client from writing
// its own result: a forged token, a replayed decision, and a move belonging to
// the other half.
func TestServerIsAuthoritative(t *testing.T) {
	ts, _ := harness(t)

	var start StartResponse
	do(t, ts, "POST", "/api/v1/run", "", map[string]any{}, &start)
	body := map[string]any{"bowler_id": 0, "decisions": 0}

	t.Run("a forged token is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over",
			"not-the-real-token", body, nil)
		if code != http.StatusForbidden {
			t.Errorf("status %d, want 403", code)
		}
	})

	t.Run("an unknown run is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/nonexistent/defend/over", start.Token, body, nil)
		if code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})

	t.Run("a decision from the wrong half is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/chase/over", start.Token,
			map[string]any{"intent": "attack", "decisions": 0}, nil)
		if code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
	})

	t.Run("an illegal bowler is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over", start.Token,
			map[string]any{"bowler_id": 99, "decisions": 0}, nil)
		if code != http.StatusUnprocessableEntity {
			t.Errorf("status %d, want 422", code)
		}
	})

	// Play one legal over, then try to replay it.
	var r OverResponse
	if code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over", start.Token, body, &r); code != 200 {
		t.Fatalf("legal over: status %d", code)
	}

	t.Run("a replayed decision is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over", start.Token, body, nil)
		if code != http.StatusConflict {
			t.Errorf("replaying decision 0 gave status %d, want 409", code)
		}
	})

	t.Run("a decision from the future is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/defend/over", start.Token,
			map[string]any{"bowler_id": 1, "decisions": 99}, nil)
		if code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
	})

	t.Run("finishing an unfinished run is refused", func(t *testing.T) {
		code := do(t, ts, "POST", "/api/v1/run/"+start.RunID+"/finish", start.Token,
			map[string]any{"decisions": r.State.Decisions}, nil)
		if code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
	})
}

func TestSanitisePlayer(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Rishi", "Rishi"},
		{"  spaced  ", "spaced"},
		{"<script>alert(1)</script>", "scriptalert1script"},
		{"emoji 🏏 name", "emoji  name"},
		{"", ""},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, tc := range tests {
		if got := sanitisePlayer(tc.in); got != tc.want {
			t.Errorf("sanitisePlayer(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShareText(t *testing.T) {
	r := FinishResponse{
		Date: "2026-08-29", Target: 202,
		Defended: true, DefendMargin: 12,
		Chased:      false,
		ChaseMargin: 4,
		DefendGrid:  []string{"good", "bad", "level"},
		ChaseGrid:   []string{"level", "good"},
		Streak:      3,
	}
	r.Day.Runs = 40
	r.Day.DefendRate = 0.68
	r.Day.ChaseRate = 0.31

	out := ShareText(r)
	for _, want := range []string{"Par 2026-08-29", "target 202", "won by 12", "lost by 4",
		"68% defended", "31% chased", "streak 3"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("share text is missing %q:\n%s", want, out)
		}
	}
	// The grid must survive as plain text with no markup.
	if bytes.ContainsAny([]byte(out), "<>") {
		t.Errorf("share text contains markup:\n%s", out)
	}
}

func TestParseIntent(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want sim.Intent
		ok   bool
	}{
		{"block", sim.Block, true},
		{"b", sim.Block, true},
		{"ATTACK", sim.Attack, true},
		{"", sim.Rotate, true},
		{"sweep", sim.Rotate, false},
	} {
		got, err := parseIntent(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("parseIntent(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
		}
		if tc.ok && got != tc.want {
			t.Errorf("parseIntent(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
