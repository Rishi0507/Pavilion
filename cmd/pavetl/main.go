// Command pavetl converts the raw Cricsheet IPL archive into Manhattan's
// binary corpus, and emits a data quality report alongside it.
//
// The corpus is the single source of truth for every model and for the match
// engine. It is deliberately a build artifact: nothing downstream ever reads
// the raw JSON, and regenerating it is one command.
//
// Data source: Cricsheet (https://cricsheet.org), by Stephen Rushe, published
// under the Open Data Commons Attribution License 1.0.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
)

// sourceLicense credits both upstream sources. They carry different terms, and
// the difference is load-bearing: Cricsheet is attribution-only, while the
// Wikipedia-derived attribute table is share-alike.
const sourceLicense = "Ball-by-ball data: Cricsheet (cricsheet.org), by Stephen Rushe, " +
	"under ODC-BY 1.0 (http://opendatacommons.org/licenses/by/1.0/). " +
	"Player attributes: English Wikipedia, under CC BY-SA 4.0 " +
	"(https://creativecommons.org/licenses/by-sa/4.0/)."

func main() {
	var (
		matchDir = flag.String("matches", filepath.Join("data", "raw", "ipl_json"), "directory of Cricsheet match JSON files")
		register = flag.String("register", filepath.Join("data", "raw", "people.csv"), "Cricsheet people register CSV")
		outDir   = flag.String("out", filepath.Join("data", "out"), "output directory")
		attrPath = flag.String("attributes", filepath.Join("data", "attributes", "players.csv"), "player attribute table")
		baseline = flag.String("baseline", filepath.Join("data", "quality_baseline.json"), "committed quality baseline")
		check    = flag.Bool("check", false, "fail if the report regresses against the baseline")
		writeBl  = flag.Bool("write-baseline", false, "accept the current report as the new baseline")
		verbose  = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	lvl := slog.LevelInfo
	if *verbose {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	opts := options{
		matchDir: *matchDir,
		register: *register,
		outDir:   *outDir,
		attrPath: *attrPath,
		baseline: *baseline,
		check:    *check,
		writeBl:  *writeBl,
	}
	if err := run(log, opts); err != nil {
		log.Error("etl failed", "err", err)
		os.Exit(1)
	}
}

type options struct {
	matchDir, register, outDir, attrPath, baseline string
	check, writeBl                                 bool
}

func run(log *slog.Logger, opt options) error {
	started := time.Now()
	matchDir, register, outDir := opt.matchDir, opt.register, opt.outDir

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	b := newBuilder()
	b.q.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	b.q.Source.License = sourceLicense

	if err := b.loadRegister(register); err != nil {
		return err
	}
	attrs, err := attr.Load(opt.attrPath)
	if err != nil {
		return err
	}
	if len(attrs) > 0 {
		b.attrs = attrs
		log.Info("attribute table loaded", "rows", len(attrs), "path", opt.attrPath)
	} else {
		log.Warn("no attribute table; run pavattr", "path", opt.attrPath)
	}
	log.Info("people register loaded", "rows", b.q.Entity.RegisterRows)

	matches, files, err := parseAll(matchDir)
	if err != nil {
		return err
	}
	log.Info("match files parsed", "matches", len(matches), "took", time.Since(started).Round(time.Millisecond))

	// Two passes: resolve every player identity first, so that PlayerIDs are
	// assigned from the complete set and are stable regardless of match order.
	b.scanRegistries(matches)
	b.resolvePlayers()
	log.Info("players resolved",
		"players", b.q.Entity.Players,
		"ambiguous_names", len(b.q.Entity.AmbiguousNames),
		"multi_alias", len(b.q.Entity.MultiAlias))

	for i, m := range matches {
		if err := b.addMatch(m, files[i]); err != nil {
			return fmt.Errorf("ingest: %w", err)
		}
	}
	b.finish(matchDir, len(files))

	corpusPath := filepath.Join(outDir, "corpus.bin")
	if err := corpus.Save(&b.st, corpusPath); err != nil {
		return err
	}
	info, err := os.Stat(corpusPath)
	if err != nil {
		return fmt.Errorf("stat corpus: %w", err)
	}

	// Read it straight back. A corpus that cannot round-trip is worse than no
	// corpus, and this catches codec drift at the moment it is introduced.
	rt, err := corpus.Load(corpusPath)
	if err != nil {
		return fmt.Errorf("round-trip: %w", err)
	}
	if rt.D.Len() != b.st.D.Len() || rt.Inn.Len() != b.st.Inn.Len() || rt.M.Len() != b.st.M.Len() {
		return fmt.Errorf("round-trip mismatch: deliveries %d/%d innings %d/%d matches %d/%d",
			rt.D.Len(), b.st.D.Len(), rt.Inn.Len(), b.st.Inn.Len(), rt.M.Len(), b.st.M.Len())
	}

	if err := b.q.WriteJSON(filepath.Join(outDir, "quality.json")); err != nil {
		return err
	}
	if err := b.q.WriteMarkdown(filepath.Join(outDir, "quality.md")); err != nil {
		return err
	}

	log.Info("corpus written",
		"path", corpusPath,
		"bytes", info.Size(),
		"deliveries", b.st.D.Len(),
		"innings", b.st.Inn.Len(),
		"matches", b.st.M.Len(),
		"players", len(b.st.Players),
		"took", time.Since(started).Round(time.Millisecond))

	for _, w := range b.q.Warnings {
		log.Warn(w)
	}

	if opt.writeBl {
		if err := WriteBaseline(&b.q, opt.baseline); err != nil {
			return err
		}
		log.Info("baseline updated", "path", opt.baseline)
	}
	if opt.check {
		if err := CheckAgainst(&b.q, opt.baseline); err != nil {
			return err
		}
		log.Info("quality gate passed", "baseline", opt.baseline)
	}
	return nil
}
