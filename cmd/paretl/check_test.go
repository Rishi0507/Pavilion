package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func baseReport() *Quality {
	q := &Quality{Coverage: map[string]float64{
		"player.batting_hand":  97.93,
		"player.bowling_class": 95.13,
		"match.venue":          100,
	}}
	q.Deliveries.Total = 295215
	q.Matches.Included = 1237
	q.Entity.Players = 964
	q.Entity.UnregisteredNames = map[string]int{}
	return q
}

// TestGateIgnoresTimestamp is the reason the gate exists in this form. The
// report carries a generation timestamp, so comparing the file byte for byte
// would fail on every run; the gate must compare substance only.
func TestGateIgnoresTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	before := baseReport()
	before.GeneratedAt = "2026-01-01T00:00:00Z"
	if err := WriteBaseline(before, path); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}

	after := baseReport()
	after.GeneratedAt = "2027-06-15T12:34:56Z"
	if err := CheckAgainst(after, path); err != nil {
		t.Errorf("gate failed on a timestamp-only change: %v", err)
	}
}

func TestGateCatchesRegressions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Quality)
		wantErr string
	}{
		{
			name:    "unchanged passes",
			mutate:  func(*Quality) {},
			wantErr: "",
		},
		{
			name:    "coverage improving passes",
			mutate:  func(q *Quality) { q.Coverage["player.bowling_class"] = 99.0 },
			wantErr: "",
		},
		{
			name:    "coverage falling fails",
			mutate:  func(q *Quality) { q.Coverage["player.bowling_class"] = 80.0 },
			wantErr: "coverage regression: player.bowling_class",
		},
		{
			name:    "floating point noise is tolerated",
			mutate:  func(q *Quality) { q.Coverage["player.bowling_class"] = 95.13 - 0.001 },
			wantErr: "",
		},
		{
			name:    "an attribute disappearing fails",
			mutate:  func(q *Quality) { delete(q.Coverage, "player.batting_hand") },
			wantErr: "disappeared from the report",
		},
		{
			name:    "losing deliveries fails",
			mutate:  func(q *Quality) { q.Deliveries.Total = 100 },
			wantErr: "deliveries fell",
		},
		{
			name:    "gaining deliveries passes",
			mutate:  func(q *Quality) { q.Deliveries.Total = 400000 },
			wantErr: "",
		},
		{
			name:    "losing matches fails",
			mutate:  func(q *Quality) { q.Matches.Included = 10 },
			wantErr: "matches fell",
		},
		{
			name:    "losing players fails",
			mutate:  func(q *Quality) { q.Entity.Players = 5 },
			wantErr: "players fell",
		},
		{
			name:    "a new ball-index mismatch fails",
			mutate:  func(q *Quality) { q.Integrity.BallIndexMismatches = 1 },
			wantErr: "ball-index mismatches rose",
		},
		{
			name:    "a new runs.total mismatch fails",
			mutate:  func(q *Quality) { q.Integrity.RunsTotalMismatches = 3 },
			wantErr: "runs.total mismatches rose",
		},
		{
			name: "a new unregistered name fails",
			mutate: func(q *Quality) {
				q.Entity.UnregisteredNames = map[string]int{"Mystery Player": 4}
			},
			wantErr: "unregistered player names rose",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "baseline.json")
			if err := WriteBaseline(baseReport(), path); err != nil {
				t.Fatalf("WriteBaseline: %v", err)
			}
			q := baseReport()
			tc.mutate(q)

			err := CheckAgainst(q, path)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("CheckAgainst: unexpected failure: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckAgainst: want failure containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("CheckAgainst: error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestGateReportsEveryRegression checks that a run with several problems lists
// all of them, so a fix does not have to be found one CI run at a time.
func TestGateReportsEveryRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(baseReport(), path); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	q := baseReport()
	q.Coverage["player.bowling_class"] = 10
	q.Deliveries.Total = 1
	q.Integrity.BallIndexMismatches = 7

	err := CheckAgainst(q, path)
	if err == nil {
		t.Fatal("CheckAgainst: want failure")
	}
	for _, want := range []string{"coverage regression", "deliveries fell", "ball-index mismatches rose"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestGateNeedsABaseline(t *testing.T) {
	err := CheckAgainst(baseReport(), filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || !strings.Contains(err.Error(), "write-baseline") {
		t.Errorf("want a helpful missing-baseline error, got %v", err)
	}
}
