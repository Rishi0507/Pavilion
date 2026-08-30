// Command pavattr resolves the two player attributes that Cricsheet does not
// carry, batting handedness and bowling type, for the players the game can
// actually deal.
//
// Sources, in precedence order:
//
//  1. data/attributes/manual.csv, hand-authored corrections. Always wins.
//  2. English Wikipedia cricketer infoboxes, joined by Wikidata property P2697
//     (ESPNcricinfo player ID). Content is CC BY-SA 4.0.
//
// ESPNcricinfo itself is deliberately not used. See the README: its robots.txt
// is unreachable, www.espn.com/robots.txt disallows automated agents outright,
// and the governing Disney terms of use prohibit compiling a data set from the
// site by automated means.
//
// Anything neither source resolves is written to a review file with the
// evidence behind a guess, for a human to settle. Guesses never enter the main
// table.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"time"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
)

const userAgent = "Manhattan/0.1 (IPL daily puzzle, non-commercial prototype; rishipopawala@gmail.com)"

func main() {
	var (
		corpusPath = flag.String("corpus", filepath.Join("data", "out", "corpus.bin"), "corpus binary")
		outDir     = flag.String("out", filepath.Join("data", "attributes"), "attribute table directory")
		cacheDir   = flag.String("cache", filepath.Join("data", "raw", "wpcache"), "HTTP response cache")
		delay      = flag.Duration("delay", 2*time.Second, "minimum delay between outbound requests")
		minBowled  = flag.Int("min-balls-bowled", corpus.DefaultEligibility.MinBallsBowled, "eligibility threshold for bowlers")
		minFaced   = flag.Int("min-balls-faced", corpus.DefaultEligibility.MinBallsFaced, "eligibility threshold for batters")
		offline    = flag.Bool("offline", false, "use only the cache; make no requests")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Ctrl-C leaves the cache intact and the run resumable.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cfg := config{
		corpusPath: *corpusPath,
		outDir:     *outDir,
		cacheDir:   *cacheDir,
		delay:      *delay,
		offline:    *offline,
		eligibility: corpus.Eligibility{
			MinBallsBowled: *minBowled,
			MinBallsFaced:  *minFaced,
		},
	}
	if err := run(ctx, log, cfg); err != nil {
		log.Error("attribute sourcing failed", "err", err)
		os.Exit(1)
	}
}

type config struct {
	corpusPath  string
	outDir      string
	cacheDir    string
	delay       time.Duration
	offline     bool
	eligibility corpus.Eligibility
}

func run(ctx context.Context, log *slog.Logger, cfg config) error {
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	st, err := corpus.Load(cfg.corpusPath)
	if err != nil {
		return err
	}
	eligible, bowlers, batters := st.Eligible(cfg.eligibility)
	log.Info("eligible player set",
		"players", len(eligible), "bowlers", len(bowlers), "batters", len(batters),
		"min_balls_bowled", cfg.eligibility.MinBallsBowled,
		"min_balls_faced", cfg.eligibility.MinBallsFaced)

	manualPath := filepath.Join(cfg.outDir, "manual.csv")
	manual, err := attr.Load(manualPath)
	if err != nil {
		return err
	}
	log.Info("manual corrections loaded", "rows", len(manual), "path", manualPath)

	// Collect the Cricinfo ids to resolve. Players already settled by hand need
	// no lookup at all.
	var cricinfoIDs []string
	byCricinfo := map[string]corpus.PlayerID{}
	for _, p := range eligible {
		pl := st.Players[p]
		if _, ok := manual[pl.CricsheetID]; ok {
			continue
		}
		if pl.CricinfoID == "" {
			continue
		}
		cricinfoIDs = append(cricinfoIDs, pl.CricinfoID)
		byCricinfo[pl.CricinfoID] = p
	}
	sort.Strings(cricinfoIDs)

	table := attr.Table{}
	today := time.Now().UTC().Format("2006-01-02")

	if len(cricinfoIDs) > 0 {
		c, err := newClient(cfg.cacheDir, userAgent, cfg.delay, log)
		if err != nil {
			return err
		}
		if cfg.offline {
			c.delay = 0
		}

		titles, err := c.resolveArticles(ctx, cricinfoIDs)
		if err != nil {
			return err
		}

		list := make([]string, 0, len(titles))
		for _, t := range titles {
			list = append(list, t)
		}
		sort.Strings(list)

		texts, err := c.fetchWikitext(ctx, list)
		if err != nil {
			return err
		}
		log.Info("wikipedia fetch complete",
			"articles", len(texts), "requests_made", c.fetched, "served_from_cache", c.cached)

		for cricinfoID, title := range titles {
			text, ok := texts[title]
			if !ok {
				continue
			}
			p := byCricinfo[cricinfoID]
			pl := st.Players[p]

			batRaw := infoboxField(text, "batting")
			bowlRaw := infoboxField(text, "bowling")
			bat := attr.NormaliseHand(batRaw)
			bowl := attr.NormaliseBowl(bowlRaw)
			if bat == attr.HandUnknown && bowl == attr.BowlUnknown {
				continue
			}
			table[pl.CricsheetID] = attr.Player{
				CricsheetID: pl.CricsheetID,
				Name:        pl.Name,
				CricinfoID:  pl.CricinfoID,
				Bat:         bat,
				Bowl:        bowl,
				BatRaw:      batRaw,
				BowlRaw:     bowlRaw,
				Provenance:  attr.Sourced,
				Source:      "wikipedia:" + title,
				Retrieved:   today,
			}
		}
	}

	// Hand corrections win outright.
	for id, p := range manual {
		p.Provenance = attr.Manual
		table[id] = p
	}

	mainPath := filepath.Join(cfg.outDir, "players.csv")
	if err := attr.Save(table, mainPath); err != nil {
		return err
	}

	// Anything still unresolved goes to review, never into the table above.
	var needReview []Signature
	var missingHand []string
	for _, p := range eligible {
		pl := st.Players[p]
		row, have := table[pl.CricsheetID]

		isBowler := false
		for _, b := range bowlers {
			if b == p {
				isBowler = true
				break
			}
		}
		if isBowler && (!have || row.Bowl == attr.BowlUnknown) {
			sig := inferBowlingFamily(st, p)
			sig.BatRaw, sig.BowlRaw, sig.Source = row.BatRaw, row.BowlRaw, row.Source
			needReview = append(needReview, sig)
		}
		if !have || row.Bat == attr.HandUnknown {
			missingHand = append(missingHand, fmt.Sprintf("%s (%s)", pl.Name, pl.CricsheetID))
		}
	}

	reviewPath := filepath.Join(cfg.outDir, "review_inferred.csv")
	if err := writeReview(reviewPath, needReview); err != nil {
		return err
	}

	sum := summarise(st, eligible, bowlers, batters, table)
	if err := sum.write(filepath.Join(cfg.outDir, "coverage.json")); err != nil {
		return err
	}

	log.Info("attribute table written",
		"path", mainPath,
		"rows", len(table),
		"sourced", sum.Sourced,
		"manual", sum.ManualRows,
		"batting_hand_coverage", fmt.Sprintf("%.2f%%", sum.BattingHandPct),
		"bowling_class_coverage", fmt.Sprintf("%.2f%%", sum.BowlingClassPct))
	log.Info("review file written", "path", reviewPath, "rows_needing_a_human", len(needReview))
	if len(missingHand) > 0 {
		log.Warn("batters with no handedness resolved", "count", len(missingHand),
			"sample", missingHand[:min(5, len(missingHand))])
	}
	return nil
}

// Coverage is the per-attribute summary folded into the data quality report.
type Coverage struct {
	GeneratedAt      string         `json:"generated_at"`
	EligiblePlayers  int            `json:"eligible_players"`
	EligibleBowlers  int            `json:"eligible_bowlers"`
	EligibleBatters  int            `json:"eligible_batters"`
	MinBallsBowled   int            `json:"min_balls_bowled"`
	MinBallsFaced    int            `json:"min_balls_faced"`
	Rows             int            `json:"rows"`
	Sourced          int            `json:"sourced"`
	ManualRows       int            `json:"manual"`
	BattingHandKnown int            `json:"batting_hand_known"`
	BowlingClassKnwn int            `json:"bowling_class_known"`
	BattingHandPct   float64        `json:"batting_hand_coverage_pct"`
	BowlingClassPct  float64        `json:"bowling_class_coverage_pct"`
	ByClass          map[string]int `json:"by_bowling_class"`
	ByHand           map[string]int `json:"by_batting_hand"`
}

func summarise(st *corpus.Store, eligible, bowlers, batters []corpus.PlayerID, t attr.Table) Coverage {
	c := Coverage{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		EligiblePlayers: len(eligible),
		EligibleBowlers: len(bowlers),
		EligibleBatters: len(batters),
		MinBallsBowled:  corpus.DefaultEligibility.MinBallsBowled,
		MinBallsFaced:   corpus.DefaultEligibility.MinBallsFaced,
		Rows:            len(t),
		ByClass:         map[string]int{},
		ByHand:          map[string]int{},
	}
	for _, p := range t {
		switch p.Provenance {
		case attr.Sourced:
			c.Sourced++
		case attr.Manual:
			c.ManualRows++
		}
	}
	// Coverage is measured against the players who need each attribute: every
	// eligible player needs a batting hand, only eligible bowlers need a class.
	for _, p := range eligible {
		if h, ok := t.BatOf(st.Players[p].CricsheetID); ok {
			c.BattingHandKnown++
			c.ByHand[h.String()]++
		}
	}
	for _, p := range bowlers {
		if b, ok := t.BowlOf(st.Players[p].CricsheetID); ok {
			c.BowlingClassKnwn++
			c.ByClass[b.String()]++
		}
	}
	if len(eligible) > 0 {
		c.BattingHandPct = 100 * float64(c.BattingHandKnown) / float64(len(eligible))
	}
	if len(bowlers) > 0 {
		c.BowlingClassPct = 100 * float64(c.BowlingClassKnwn) / float64(len(bowlers))
	}
	return c
}

func (c Coverage) write(path string) error {
	b, err := jsonMarshalIndent(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write coverage: %w", err)
	}
	return nil
}
