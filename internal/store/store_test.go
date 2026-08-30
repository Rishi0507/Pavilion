package store

import (
	"context"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func save(t *testing.T, db *DB, r Result) {
	t.Helper()
	if r.Date == "" {
		r.Date = "2026-08-29"
	}
	if err := db.Save(context.Background(), r); err != nil {
		t.Fatalf("save %s: %v", r.RunID, err)
	}
}

// Solving the puzzle means winning both halves, and the board has to say so.
//
// Ranking on the decision score alone put a clever defeat above a win, which is
// not what anybody means by a leaderboard: somebody who held the target and then
// chased it down has done the thing the game asks, and somebody who lost both
// halves thoughtfully has not.
func TestLeaderboardRanksSolvedFirst(t *testing.T) {
	db := open(t)

	save(t, db, Result{RunID: "a", Player: "Cautious", Defended: true, Chased: false,
		DefendScore: 30, ChaseScore: 30}) // a very good losing run
	save(t, db, Result{RunID: "b", Player: "Winner", Defended: true, Chased: true,
		DefendScore: 1, ChaseScore: 1}) // a scruffy winning one
	save(t, db, Result{RunID: "c", Player: "AlsoWon", Defended: true, Chased: true,
		DefendScore: 5, ChaseScore: 5})

	rows, err := db.Leaderboard(context.Background(), "2026-08-29", 10)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// Both solved runs outrank the unsolved one, however good its decisions.
	if !rows[0].Solved || !rows[1].Solved {
		t.Errorf("an unsolved run outranked a solved one: %+v", rows)
	}
	if rows[2].Player != "Cautious" {
		t.Errorf("third place is %q, want the unsolved run", rows[2].Player)
	}
	// Within the solved group, the decision score separates them.
	if rows[0].Player != "AlsoWon" {
		t.Errorf("top solved run is %q, want the higher score", rows[0].Player)
	}
}

// A board of rows reading "anonymous" is not a board, and nobody is required to
// give a name in order to play.
func TestLeaderboardOmitsUnnamedRuns(t *testing.T) {
	db := open(t)

	save(t, db, Result{RunID: "a", Player: "", Defended: true, Chased: true, DefendScore: 99})
	save(t, db, Result{RunID: "b", Player: "   ", Defended: true, Chased: true, DefendScore: 98})
	save(t, db, Result{RunID: "c", Player: "Named", Defended: false, Chased: false})

	rows, err := db.Leaderboard(context.Background(), "2026-08-29", 10)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if len(rows) != 1 || rows[0].Player != "Named" {
		t.Fatalf("unnamed runs reached the board: %+v", rows)
	}
}

// A name is asked for once the puzzle is solved, which is necessarily after the
// run was recorded, so it has to be attachable afterwards.
func TestSetPlayerNamesAFinishedRun(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	save(t, db, Result{RunID: "a", Player: "", Defended: true, Chased: true, DefendScore: 4})

	if err := db.SetPlayer(ctx, "a", "Rishi"); err != nil {
		t.Fatalf("set player: %v", err)
	}
	rows, err := db.Leaderboard(ctx, "2026-08-29", 10)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if len(rows) != 1 || rows[0].Player != "Rishi" || !rows[0].Solved {
		t.Fatalf("naming did not put the run on the board: %+v", rows)
	}

	if err := db.SetPlayer(ctx, "nope", "Ghost"); err == nil {
		t.Error("naming a run that does not exist was accepted")
	}
}
