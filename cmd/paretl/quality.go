package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Quality is the data quality report emitted alongside the corpus.
//
// It exists because every model downstream inherits these defects silently.
// The report is committed and diffed in CI: a drop in attribute coverage or a
// new integrity failure fails the build rather than quietly degrading the game.
type Quality struct {
	GeneratedAt string `json:"generated_at"`
	Source      struct {
		Dir          string         `json:"dir"`
		Files        int            `json:"files"`
		Parsed       int            `json:"parsed"`
		DataVersions map[string]int `json:"data_versions"`
		License      string         `json:"license"`
	} `json:"source"`

	Matches struct {
		Included int            `json:"included"`
		Skipped  []string       `json:"skipped"`
		BySeason map[string]int `json:"by_season"`
		Venues   int            `json:"venues"`
		Cities   int            `json:"cities"`
		Teams    int            `json:"teams"`
	} `json:"matches"`

	Deliveries struct {
		Total   int            `json:"total"`
		Legal   int            `json:"legal"`
		Wides   int            `json:"wides"`
		NoBalls int            `json:"noballs"`
		Byes    int            `json:"byes"`
		LegByes int            `json:"legbyes"`
		Penalty int            `json:"penalty"`
		Wickets int            `json:"wickets"`
		ByPhase map[string]int `json:"by_phase"`
	} `json:"deliveries"`

	Entity struct {
		RegisterRows        int             `json:"register_rows"`
		Players             int             `json:"players"`
		AmbiguousNames      []AmbiguousName `json:"ambiguous_names"`
		MultiAlias          []AliasRecord   `json:"multi_alias_ids"`
		UnregisteredNames   map[string]int  `json:"unregistered_names"`
		MissingFromRegister []string        `json:"missing_from_register"`
		CricinfoIDCoverage  float64         `json:"cricinfo_id_coverage_pct"`
	} `json:"entity"`

	Integrity struct {
		BallIndexMismatches  int      `json:"ball_index_mismatches"`
		BallIndexSamples     []string `json:"ball_index_samples"`
		MiscountedOvers      []string `json:"miscounted_overs"`
		MultiWicketBalls     int      `json:"multi_wicket_deliveries"`
		RunsTotalMismatches  int      `json:"runs_total_mismatches"`
		SuperOverInnings     int      `json:"super_over_innings"`
		AbandonedMatches     int      `json:"abandoned_matches"`
		InningsWithTarget    int      `json:"innings_with_target"`
		NonStandardOverCount int      `json:"innings_over_20_overs"`
	} `json:"integrity"`

	// Eligible describes the player set the game can actually deal, and how
	// much of it has resolved attributes. Attribute coverage is measured
	// against this set rather than the whole corpus: a net bowler with forty
	// deliveries will never be picked, so his missing bowling type is not a
	// defect.
	Eligible struct {
		Players           int `json:"players"`
		Bowlers           int `json:"bowlers"`
		MinBallsBowled    int `json:"min_balls_bowled"`
		MinBallsFaced     int `json:"min_balls_faced"`
		BattingHandKnown  int `json:"batting_hand_known"`
		BowlingClassKnown int `json:"bowling_class_known"`
		Sourced           int `json:"attr_rows_sourced"`
		Manual            int `json:"attr_rows_manual"`
		InferredInTable   int `json:"attr_rows_inferred"`

		// Dealable is the eligible set minus players whose attributes could
		// not be resolved. It is the pool the game actually draws from.
		Dealable        int              `json:"dealable_players"`
		DealableBowlers int              `json:"dealable_bowlers"`
		DealableBatters int              `json:"dealable_batters"`
		Excluded        []ExcludedPlayer `json:"excluded"`
	} `json:"eligible"`

	// Coverage is the percentage of records carrying each attribute. The two
	// entries that matter most are the ones Cricsheet does not supply at all.
	Coverage map[string]float64 `json:"coverage_pct"`

	Warnings []string `json:"warnings"`
}

// AmbiguousName is one display name that resolves to more than one person.
// These are the dangerous ones: keyed by name, two different cricketers' balls
// would be merged into a single set of rates.
type AmbiguousName struct {
	Name string   `json:"name"`
	IDs  []string `json:"ids"`
}

// ExcludedPlayer is an eligible player the game will not deal, because the
// attributes the matchup model needs could not be resolved for them.
//
// These are recorded rather than silently dropped: an exclusion is a decision,
// and a high-volume player appearing here is a signal that something needs
// fixing by hand rather than a fact to accept.
type ExcludedPlayer struct {
	CricsheetID string `json:"cricsheet_id"`
	Name        string `json:"name"`
	BallsBowled int    `json:"balls_bowled"`
	BallsFaced  int    `json:"balls_faced"`
	LastSeason  int    `json:"last_season"`
	Missing     string `json:"missing"`
}

// AliasRecord is one person recorded under several display names.
type AliasRecord struct {
	ID    string   `json:"id"`
	Names []string `json:"names"`
}

func (q *Quality) warn(format string, a ...any) {
	q.Warnings = append(q.Warnings, fmt.Sprintf(format, a...))
}

// WriteJSON writes the machine-readable report.
func (q *Quality) WriteJSON(path string) error {
	b, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal quality report: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// WriteMarkdown writes the human-readable report. This is the one you actually
// read before trusting a model.
func (q *Quality) WriteMarkdown(path string) error {
	var s strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&s, format+"\n", a...) }

	p("# Manhattan data quality report")
	p("")
	p("Generated %s from `%s`.", q.GeneratedAt, q.Source.Dir)
	p("Source licence: %s", q.Source.License)
	p("")
	p("## Corpus")
	p("")
	p("| | |")
	p("|---|---:|")
	p("| Match files | %d |", q.Source.Files)
	p("| Matches included | %d |", q.Matches.Included)
	p("| Matches skipped | %d |", len(q.Matches.Skipped))
	p("| Deliveries | %d |", q.Deliveries.Total)
	p("| Legal deliveries | %d |", q.Deliveries.Legal)
	p("| Wickets | %d |", q.Deliveries.Wickets)
	p("| Players | %d |", q.Entity.Players)
	p("| Venues | %d |", q.Matches.Venues)
	p("| Teams | %d |", q.Matches.Teams)
	p("")

	p("## Attribute coverage")
	p("")
	p("| Attribute | Coverage |")
	p("|---|---:|")
	keys := make([]string, 0, len(q.Coverage))
	for k := range q.Coverage {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p("| %s | %.2f%% |", k, q.Coverage[k])
	}
	p("")

	p("## Player attributes")
	p("")
	p("Cricsheet carries neither batting handedness nor bowling type. Both are")
	p("sourced separately by `parattr` into a checked-in table with per-row")
	p("provenance. Coverage below is measured against the players the game can")
	p("actually deal, not the whole corpus.")
	p("")
	p("| | |")
	p("|---|---:|")
	p("| Eligible players (>= %d balls faced, or >= %d bowled) | %d |",
		q.Eligible.MinBallsFaced, q.Eligible.MinBallsBowled, q.Eligible.Players)
	p("| Eligible bowlers | %d |", q.Eligible.Bowlers)
	p("| Batting handedness known | %d |", q.Eligible.BattingHandKnown)
	p("| Bowling class known | %d |", q.Eligible.BowlingClassKnown)
	p("| Attribute rows sourced | %d |", q.Eligible.Sourced)
	p("| Attribute rows set by hand | %d |", q.Eligible.Manual)
	p("| **Dealable players** | **%d** |", q.Eligible.Dealable)
	p("| Dealable as a bowler | %d |", q.Eligible.DealableBowlers)
	p("| Dealable as a batter | %d |", q.Eligible.DealableBatters)
	p("")

	if len(q.Eligible.Excluded) > 0 {
		p("### Excluded from the dealable pool (%d)", len(q.Eligible.Excluded))
		p("")
		p("These players clear the volume threshold but have no resolved")
		p("attributes, so the game will not deal them. A player with no")
		p("Wikipedia article is generally not one a daily game about")
		p("recognisable cricketers should be putting on screen; a high-volume")
		p("name here is worth resolving by hand in `manual.csv` instead.")
		p("")
		p("| Player | Identifier | Bowled | Faced | Last season | Missing |")
		p("|---|---|---:|---:|---:|---|")
		for _, e := range q.Eligible.Excluded {
			p("| %s | `%s` | %d | %d | %d | %s |",
				e.Name, e.CricsheetID, e.BallsBowled, e.BallsFaced, e.LastSeason, e.Missing)
		}
		p("")
	}

	p("## Entity resolution")
	p("")
	p("Cricsheet embeds a per-match `registry.people` map from display name to a")
	p("stable person identifier, so name drift across seasons is resolved")
	p("upstream. The corpus keys every player by that identifier, never by name.")
	p("")
	p("- People register rows: %d", q.Entity.RegisterRows)
	p("- Players in the corpus: %d", q.Entity.Players)
	p("- Cricinfo id coverage: %.2f%%", q.Entity.CricinfoIDCoverage)
	p("- Names in deliveries absent from their match registry: %d", len(q.Entity.UnregisteredNames))
	p("- Corpus players absent from the people register: %d", len(q.Entity.MissingFromRegister))
	p("")
	if len(q.Entity.AmbiguousNames) > 0 {
		p("### Ambiguous display names (%d)", len(q.Entity.AmbiguousNames))
		p("")
		p("One name, more than one cricketer. Keying rates by name would merge them.")
		p("")
		p("| Name | Identifiers |")
		p("|---|---|")
		for _, a := range q.Entity.AmbiguousNames {
			p("| %s | `%s` |", a.Name, strings.Join(a.IDs, "`, `"))
		}
		p("")
	}
	if len(q.Entity.MultiAlias) > 0 {
		p("### Identifiers with multiple display names (%d)", len(q.Entity.MultiAlias))
		p("")
		p("| Identifier | Names |")
		p("|---|---|")
		for _, a := range q.Entity.MultiAlias {
			p("| `%s` | %s |", a.ID, strings.Join(a.Names, ", "))
		}
		p("")
	}

	p("## Integrity")
	p("")
	p("| Check | Count |")
	p("|---|---:|")
	p("| Legal-ball index disagrees with `actual_delivery` | %d |", q.Integrity.BallIndexMismatches)
	p("| Overs miscounted by the umpire | %d |", len(q.Integrity.MiscountedOvers))
	p("| Deliveries with more than one wicket | %d |", q.Integrity.MultiWicketBalls)
	p("| `runs.total` disagrees with its components | %d |", q.Integrity.RunsTotalMismatches)
	p("| Super-over innings (excluded from modelling) | %d |", q.Integrity.SuperOverInnings)
	p("| Abandoned matches (no result) | %d |", q.Integrity.AbandonedMatches)
	p("| Innings carrying a target | %d |", q.Integrity.InningsWithTarget)
	p("")
	if len(q.Integrity.MiscountedOvers) > 0 {
		p("Miscounted overs (an umpire signalled five or seven balls; the")
		p("`actual_delivery` cross-check is legitimately expected to fail here):")
		p("")
		for _, m := range q.Integrity.MiscountedOvers {
			p("- %s", m)
		}
		p("")
	}

	if len(q.Warnings) > 0 {
		p("## Warnings (%d)", len(q.Warnings))
		p("")
		for _, w := range q.Warnings {
			p("- %s", w)
		}
		p("")
	}

	if len(q.Matches.Skipped) > 0 {
		p("## Skipped matches (%d)", len(q.Matches.Skipped))
		p("")
		for _, m := range q.Matches.Skipped {
			p("- %s", m)
		}
		p("")
	}

	if err := os.WriteFile(path, []byte(s.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
