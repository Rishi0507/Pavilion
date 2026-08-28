// Package store persists finished runs and the daily aggregates the share card
// quotes back.
//
// SQLite rather than Postgres, and a pure-Go driver rather than cgo. The brief
// specified Postgres, which is the right answer at scale, but v1 runs on
// localhost and a database that needs installing before the game will start is
// a real cost against zero benefit here. The schema is ordinary SQL and the
// queries are ordinary queries, so moving to Postgres later is a driver swap
// rather than a rewrite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the persistence layer.
type DB struct{ sql *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS runs (
    id            TEXT PRIMARY KEY,
    date          TEXT NOT NULL,
    player        TEXT NOT NULL DEFAULT '',
    defended      INTEGER NOT NULL,
    chased        INTEGER NOT NULL,
    defend_score  REAL NOT NULL,
    chase_score   REAL NOT NULL,
    total_score   REAL NOT NULL,
    defend_grid   TEXT NOT NULL,
    chase_grid    TEXT NOT NULL,
    defend_margin INTEGER NOT NULL,
    chase_margin  INTEGER NOT NULL,
    finished_at   TEXT NOT NULL
);

-- The daily stats query filters by date and ranks by score, so it gets an index
-- on exactly that.
CREATE INDEX IF NOT EXISTS runs_by_day ON runs (date, total_score);

-- A player may only submit one result per day. Without this a single browser
-- could file a thousand runs and own every leaderboard, and the daily
-- percentages, which are the whole social hook, would be meaningless.
CREATE UNIQUE INDEX IF NOT EXISTS runs_one_per_player_per_day
    ON runs (date, player) WHERE player <> '';
`

// Open connects and applies the schema.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite writes are serialised anyway, and a small pool avoids lock churn.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &DB{sql: db}, nil
}

// Close releases the database.
func (d *DB) Close() error { return d.sql.Close() }

// Result is one finished run, as recorded.
type Result struct {
	RunID        string
	Date         string
	Player       string
	Defended     bool
	Chased       bool
	DefendScore  float64
	ChaseScore   float64
	DefendGrid   string
	ChaseGrid    string
	DefendMargin int
	ChaseMargin  int
}

// ErrAlreadyPlayed is returned when a player files a second result for a day.
var ErrAlreadyPlayed = errors.New("store: this player has already played today")

// Save records a finished run.
func (d *DB) Save(ctx context.Context, r Result) error {
	_, err := d.sql.ExecContext(ctx, `
        INSERT INTO runs (id, date, player, defended, chased,
                          defend_score, chase_score, total_score,
                          defend_grid, chase_grid, defend_margin, chase_margin, finished_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.RunID, r.Date, r.Player, b2i(r.Defended), b2i(r.Chased),
		r.DefendScore, r.ChaseScore, r.DefendScore+r.ChaseScore,
		r.DefendGrid, r.ChaseGrid, r.DefendMargin, r.ChaseMargin,
		time.Now().UTC().Format(time.RFC3339))

	if err != nil && isUnique(err) {
		return ErrAlreadyPlayed
	}
	if err != nil {
		return fmt.Errorf("store: save run: %w", err)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUnique(err error) bool {
	// The pure-Go driver reports constraint violations in the message; there is
	// no typed error to match on.
	return err != nil && (contains(err.Error(), "UNIQUE constraint failed") ||
		contains(err.Error(), "constraint failed"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// DayStats is what the share card quotes: the line that makes the result
// social rather than private.
type DayStats struct {
	Date        string  `json:"date"`
	Runs        int     `json:"runs"`
	DefendRate  float64 `json:"defend_rate"`
	ChaseRate   float64 `json:"chase_rate"`
	BothRate    float64 `json:"both_rate"`
	NeitherRate float64 `json:"neither_rate"`
	MeanScore   float64 `json:"mean_score"`
}

// Day returns the aggregate for one date.
func (d *DB) Day(ctx context.Context, date string) (DayStats, error) {
	var s DayStats
	s.Date = date
	var defended, chased, both, neither sql.NullFloat64
	var mean sql.NullFloat64

	err := d.sql.QueryRowContext(ctx, `
        SELECT COUNT(*),
               AVG(defended),
               AVG(chased),
               AVG(CASE WHEN defended=1 AND chased=1 THEN 1.0 ELSE 0.0 END),
               AVG(CASE WHEN defended=0 AND chased=0 THEN 1.0 ELSE 0.0 END),
               AVG(total_score)
        FROM runs WHERE date = ?`, date).
		Scan(&s.Runs, &defended, &chased, &both, &neither, &mean)
	if err != nil {
		return s, fmt.Errorf("store: day stats: %w", err)
	}
	s.DefendRate = defended.Float64
	s.ChaseRate = chased.Float64
	s.BothRate = both.Float64
	s.NeitherRate = neither.Float64
	s.MeanScore = mean.Float64
	return s, nil
}

// Percentile returns the share of the day's runs this score beat.
//
// It is computed against decision score rather than the result, so a player who
// won on luck after three bad calls ranks below one who lost narrowly having
// played it well. That is the only honest way to compare two people who met
// different luck, which is the entire reason the score exists.
func (d *DB) Percentile(ctx context.Context, date string, score float64) (float64, error) {
	var below, total int
	err := d.sql.QueryRowContext(ctx, `
        SELECT
            (SELECT COUNT(*) FROM runs WHERE date = ? AND total_score < ?),
            (SELECT COUNT(*) FROM runs WHERE date = ?)`,
		date, score, date).Scan(&below, &total)
	if err != nil {
		return 0, fmt.Errorf("store: percentile: %w", err)
	}
	if total == 0 {
		return 0, nil
	}
	return 100 * float64(below) / float64(total), nil
}

// StreakOf counts consecutive days ending at the given date on which this
// player filed a result.
func (d *DB) StreakOf(ctx context.Context, player, upto string) (int, error) {
	if player == "" {
		return 0, nil
	}
	rows, err := d.sql.QueryContext(ctx, `
        SELECT date FROM runs WHERE player = ? AND date <= ? ORDER BY date DESC`,
		player, upto)
	if err != nil {
		return 0, fmt.Errorf("store: streak: %w", err)
	}
	defer rows.Close()

	streak := 0
	want, err := time.Parse("2006-01-02", upto)
	if err != nil {
		return 0, fmt.Errorf("store: streak: bad date %q: %w", upto, err)
	}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return 0, fmt.Errorf("store: streak: %w", err)
		}
		got, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		if !got.Equal(want) {
			break
		}
		streak++
		want = want.AddDate(0, 0, -1)
	}
	return streak, rows.Err()
}

// Leader is one row of a day's leaderboard.
type Leader struct {
	Player   string  `json:"player"`
	Score    float64 `json:"score"`
	Defended bool    `json:"defended"`
	Chased   bool    `json:"chased"`
	Grid     string  `json:"grid"`
}

// Leaderboard returns the day's best runs by decision score.
func (d *DB) Leaderboard(ctx context.Context, date string, limit int) ([]Leader, error) {
	rows, err := d.sql.QueryContext(ctx, `
        SELECT player, total_score, defended, chased, defend_grid
        FROM runs WHERE date = ?
        ORDER BY total_score DESC LIMIT ?`, date, limit)
	if err != nil {
		return nil, fmt.Errorf("store: leaderboard: %w", err)
	}
	defer rows.Close()

	var out []Leader
	for rows.Next() {
		var l Leader
		var def, ch int
		if err := rows.Scan(&l.Player, &l.Score, &def, &ch, &l.Grid); err != nil {
			return nil, fmt.Errorf("store: leaderboard: %w", err)
		}
		l.Defended, l.Chased = def == 1, ch == 1
		if l.Player == "" {
			l.Player = "anonymous"
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
