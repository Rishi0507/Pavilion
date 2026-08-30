package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The CI gate.
//
// The quality report carries a generation timestamp, which means the file
// differs on every run and cannot itself be compared byte for byte. So the gate
// compares substantive fields only: attribute coverage must not fall, corpus
// size must not shrink, and integrity failures must not appear. Everything
// incidental, timestamps included, is ignored.

// Baseline is the committed snapshot the gate compares against.
type Baseline struct {
	Coverage   map[string]float64 `json:"coverage_pct"`
	Deliveries int                `json:"deliveries"`
	Matches    int                `json:"matches"`
	Players    int                `json:"players"`
	Integrity  struct {
		BallIndexMismatches int `json:"ball_index_mismatches"`
		RunsTotalMismatches int `json:"runs_total_mismatches"`
		UnregisteredNames   int `json:"unregistered_names"`
	} `json:"integrity"`
}

// coverageTolerance allows for floating-point noise but nothing more. A real
// regression moves coverage by far more than this.
const coverageTolerance = 0.005

// BaselineOf extracts the gated fields from a report.
func BaselineOf(q *Quality) Baseline {
	b := Baseline{
		Coverage:   make(map[string]float64, len(q.Coverage)),
		Deliveries: q.Deliveries.Total,
		Matches:    q.Matches.Included,
		Players:    q.Entity.Players,
	}
	for k, v := range q.Coverage {
		b.Coverage[k] = v
	}
	b.Integrity.BallIndexMismatches = q.Integrity.BallIndexMismatches
	b.Integrity.RunsTotalMismatches = q.Integrity.RunsTotalMismatches
	b.Integrity.UnregisteredNames = len(q.Entity.UnregisteredNames)
	return b
}

// WriteBaseline commits a new baseline. Regenerating it is deliberate: it is
// how an intended change is accepted.
func WriteBaseline(q *Quality, path string) error {
	b, err := json.MarshalIndent(BaselineOf(q), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// CheckAgainst compares a report to a committed baseline and reports every
// regression at once, rather than failing on the first.
func CheckAgainst(q *Quality, path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no baseline at %s; create one with pavetl -write-baseline", path)
	}
	if err != nil {
		return fmt.Errorf("read baseline %s: %w", path, err)
	}
	var want Baseline
	if err := json.Unmarshal(raw, &want); err != nil {
		return fmt.Errorf("parse baseline %s: %w", path, err)
	}
	got := BaselineOf(q)

	var problems []string

	keys := make([]string, 0, len(want.Coverage))
	for k := range want.Coverage {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w := want.Coverage[k]
		g, ok := got.Coverage[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("attribute %q disappeared from the report (baseline had %.2f%%)", k, w))
			continue
		}
		if g < w-coverageTolerance {
			problems = append(problems, fmt.Sprintf("coverage regression: %s fell from %.2f%% to %.2f%%", k, w, g))
		}
	}

	if got.Deliveries < want.Deliveries {
		problems = append(problems, fmt.Sprintf("deliveries fell from %d to %d", want.Deliveries, got.Deliveries))
	}
	if got.Matches < want.Matches {
		problems = append(problems, fmt.Sprintf("matches fell from %d to %d", want.Matches, got.Matches))
	}
	if got.Players < want.Players {
		problems = append(problems, fmt.Sprintf("players fell from %d to %d", want.Players, got.Players))
	}

	if got.Integrity.BallIndexMismatches > want.Integrity.BallIndexMismatches {
		problems = append(problems, fmt.Sprintf("ball-index mismatches rose from %d to %d",
			want.Integrity.BallIndexMismatches, got.Integrity.BallIndexMismatches))
	}
	if got.Integrity.RunsTotalMismatches > want.Integrity.RunsTotalMismatches {
		problems = append(problems, fmt.Sprintf("runs.total mismatches rose from %d to %d",
			want.Integrity.RunsTotalMismatches, got.Integrity.RunsTotalMismatches))
	}
	if got.Integrity.UnregisteredNames > want.Integrity.UnregisteredNames {
		problems = append(problems, fmt.Sprintf("unregistered player names rose from %d to %d",
			want.Integrity.UnregisteredNames, got.Integrity.UnregisteredNames))
	}

	if len(problems) > 0 {
		return fmt.Errorf("data quality regression against %s:\n  - %s",
			path, strings.Join(problems, "\n  - "))
	}
	return nil
}
