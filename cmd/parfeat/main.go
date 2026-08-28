// Command parfeat exports the training matrix for the ball outcome model.
//
// Features are computed by internal/features, the same code the server will
// call, so the training data cannot drift from what production sees. Everything
// the features derive from is bounded to the training seasons, so a held-out
// evaluation is genuinely held out.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"manhattan/internal/attr"
	"manhattan/internal/corpus"
	"manhattan/internal/features"
)

func main() {
	var (
		corpusPath = flag.String("corpus", filepath.Join("data", "out", "corpus.bin"), "corpus binary")
		attrPath   = flag.String("attributes", filepath.Join("data", "attributes", "players.csv"), "player attribute table")
		outDir     = flag.String("out", filepath.Join("data", "out"), "output directory")
		holdout    = flag.Int("holdout-seasons", 2, "most recent seasons held out entirely")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *corpusPath, *attrPath, *outDir, *holdout); err != nil {
		log.Error("feature export failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, corpusPath, attrPath, outDir string, holdout int) error {
	st, err := corpus.Load(corpusPath)
	if err != nil {
		return err
	}
	attrs, err := attr.Load(attrPath)
	if err != nil {
		return err
	}
	if len(attrs) == 0 {
		return fmt.Errorf("no attribute table at %s; run parattr first", attrPath)
	}

	// The cutoff is the last season the model may learn from.
	latest := uint16(0)
	for _, s := range st.M.Season {
		if s > latest {
			latest = s
		}
	}
	if latest == 0 {
		return fmt.Errorf("corpus has no seasons")
	}
	cutoff := latest - uint16(holdout)
	log.Info("season split", "latest", latest, "train_through", cutoff, "holdout", holdout)

	started := time.Now()

	// The rate table is refitted for each season on everything strictly before
	// it, so no row is ever featurised with knowledge of its own season. Fits
	// are cached because seasons are processed in order and each is asked for
	// once, but the cache is keyed so a repeat is free either way.
	cache := map[uint16]*features.Context{}
	fits := 0
	newContext := func(maxSeason uint16) *features.Context {
		if c, ok := cache[maxSeason]; ok {
			return c
		}
		c := features.NewContext(st, attrs, maxSeason)
		cache[maxSeason] = c
		fits++
		log.Info("refit rate table", "seasons_through", maxSeason)
		return c
	}

	trainPath := filepath.Join(outDir, "train.csv")
	testPath := filepath.Join(outDir, "test.csv")

	train, closeTrain, err := newWriter(trainPath)
	if err != nil {
		return err
	}
	defer closeTrain()
	test, closeTest, err := newWriter(testPath)
	if err != nil {
		return err
	}
	defer closeTest()

	var nTrain, nTest int
	var labelTrain, labelTest [corpus.NumOutcomes]int
	rec := make([]string, features.Dim+2)

	features.Walk(st, attrs, newContext, func(r features.Row) {
		for i, v := range r.X {
			rec[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
		}
		rec[features.Dim] = strconv.Itoa(int(r.Y))
		rec[features.Dim+1] = strconv.Itoa(int(r.Season))

		if r.Season <= cutoff {
			nTrain++
			labelTrain[r.Y]++
			_ = train.Write(rec)
		} else {
			nTest++
			labelTest[r.Y]++
			_ = test.Write(rec)
		}
	})

	train.Flush()
	test.Flush()
	if err := train.Error(); err != nil {
		return fmt.Errorf("write %s: %w", trainPath, err)
	}
	if err := test.Error(); err != nil {
		return fmt.Errorf("write %s: %w", testPath, err)
	}

	log.Info("training rows written", "path", trainPath, "rows", nTrain)
	log.Info("held-out rows written", "path", testPath, "rows", nTest)
	for k := range corpus.NumOutcomes {
		log.Info("label share",
			"outcome", corpus.Outcome(k).String(),
			"train", fmt.Sprintf("%.3f%%", 100*float64(labelTrain[k])/float64(max(nTrain, 1))),
			"test", fmt.Sprintf("%.3f%%", 100*float64(labelTest[k])/float64(max(nTest, 1))))
	}
	log.Info("export complete", "rate_refits", fits, "took", time.Since(started).Round(time.Millisecond))
	return nil
}

func newWriter(path string) (*csv.Writer, func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	buf := bufio.NewWriterSize(f, 1<<20)
	w := csv.NewWriter(buf)

	header := make([]string, 0, features.Dim+2)
	header = append(header, features.Names...)
	header = append(header, "label", "season")
	if err := w.Write(header); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("write header to %s: %w", path, err)
	}
	return w, func() {
		w.Flush()
		buf.Flush()
		f.Close()
	}, nil
}
