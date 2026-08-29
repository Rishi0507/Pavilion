// Package session holds a run in progress and the token that proves it.
//
// The client never simulates. It sends "bowler 3 bowls over 17" and receives
// the resolved over, and the server holds the only copy of the state. The token
// carries no game state at all: it names a run and asserts that the bearer
// started it. Everything that could be forged for advantage lives server-side.
//
// The decision counter is what makes replay useless. Every request must carry
// the number of decisions the client believes have been taken, and the server
// rejects anything that does not match exactly. A replayed request has a stale
// counter; a request sent twice in parallel has the same counter twice, and
// only one of them can win.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"manhattan/internal/sim"
)

// Half is which innings of the day a run is in.
type Half uint8

const (
	Defending Half = iota
	Chasing
	Finished
)

func (h Half) String() string {
	switch h {
	case Defending:
		return "defend"
	case Chasing:
		return "chase"
	}
	return "finished"
}

// Errors the API surfaces directly.
var (
	ErrNotFound    = errors.New("session: no such run")
	ErrBadToken    = errors.New("session: token is not valid for this run")
	ErrOutOfOrder  = errors.New("session: decision counter does not match")
	ErrRunFinished = errors.New("session: this run is already finished")
	ErrWrongHalf   = errors.New("session: that decision belongs to the other half")
)

// Run is one player's attempt at one day.
type Run struct {
	ID   string
	Date string
	Key  sim.DailyKey

	Half  Half
	State *sim.State

	// Decisions counts overs resolved across both halves. It is the anti-replay
	// counter and it only ever increases.
	Decisions int

	// Mode is which of the three games this run belongs to, and Counts whether
	// its result joins the day's shared numbers. Practice and drafted sides are
	// played for their own sake.
	Mode   string
	Counts bool

	DefendOvers  []Graded
	ChaseOvers   []Graded
	DefendResult sim.Result
	ChaseResult  sim.Result

	Started time.Time
}

// Graded is one resolved over, with both the probability it moved and the value
// of the decision that produced it.
//
// The two are different things and both are needed. Delta is what happened and
// colours the share grid, because a player wants to see the over that cost them
// the game even when the choice was defensible. Value is what the decision was
// worth before the dice, and it is what the score is built from, because a sum
// of deltas telescopes into nothing but the result.
type Graded struct {
	Over  sim.Over
	Delta float64
	Value float64
}

// Store keeps runs in memory for the life of the process.
//
// A run is a few kilobytes and lives for minutes, so there is no reason to put
// it in a database; losing them on restart costs a player their current game
// and nothing else. Completed results go to durable storage separately.
type Store struct {
	mu     sync.Mutex
	runs   map[string]*Run
	secret []byte
	ttl    time.Duration
}

// NewStore creates a run store. The secret signs tokens and must not be the
// same one that derives daily keys.
func NewStore(secret []byte, ttl time.Duration) (*Store, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("session: signing secret must be at least 16 bytes")
	}
	return &Store{
		runs:   map[string]*Run{},
		secret: secret,
		ttl:    ttl,
	}, nil
}

// newID returns an unguessable run identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Token signs a run identifier and its start date.
//
// It deliberately carries no score, no over number and no state: a token that
// described the game would be a token worth forging. The only claim is that the
// bearer opened this run.
func (s *Store) Token(runID, date string) string {
	mac := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(mac, "par/v1|%s|%s", runID, date)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verify checks a token in constant time.
func (s *Store) verify(runID, date, token string) bool {
	want := s.Token(runID, date)
	return hmac.Equal([]byte(want), []byte(token))
}

// Start opens a run for a puzzle.
func (s *Store) Start(date string, key sim.DailyKey, p *sim.Puzzle) (*Run, string, error) {
	id, err := newID()
	if err != nil {
		return nil, "", err
	}
	run := &Run{
		ID:      id,
		Date:    date,
		Key:     key,
		Half:    Defending,
		State:   sim.NewChase(p),
		Started: time.Now(),
	}

	s.mu.Lock()
	s.runs[id] = run
	s.sweepLocked()
	s.mu.Unlock()

	return run, s.Token(id, date), nil
}

// sweepLocked drops runs nobody came back to. The caller holds the lock.
func (s *Store) sweepLocked() {
	if s.ttl <= 0 {
		return
	}
	cutoff := time.Now().Add(-s.ttl)
	for id, r := range s.runs {
		if r.Started.Before(cutoff) {
			delete(s.runs, id)
		}
	}
}

// Authorise resolves a run and checks both the token and the decision counter.
//
// The counter check is the anti-replay: the client states how many decisions it
// believes have happened, and anything other than the exact current value is
// refused. That covers a replayed request, a duplicate submit, and two tabs
// racing each other.
func (s *Store) Authorise(runID, token string, decisions int) (*Run, func(), error) {
	s.mu.Lock()
	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, ErrNotFound
	}
	if !s.verify(runID, run.Date, token) {
		s.mu.Unlock()
		return nil, nil, ErrBadToken
	}
	if decisions != run.Decisions {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: server has %d, client sent %d",
			ErrOutOfOrder, run.Decisions, decisions)
	}
	// The lock is held until the caller releases it, so a decision is applied
	// atomically and two racing requests cannot both see the same counter.
	return run, s.mu.Unlock, nil
}

// Get returns a run without requiring a decision counter, for read-only views.
func (s *Store) Get(runID, token string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return nil, ErrNotFound
	}
	if !s.verify(runID, run.Date, token) {
		return nil, ErrBadToken
	}
	return run, nil
}

// Count reports how many runs are live, for the health endpoint.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

// ParseDecisions reads the counter a client sent.
func ParseDecisions(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("session: missing decision counter")
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("session: bad decision counter %q", v)
	}
	return n, nil
}
