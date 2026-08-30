package engine

import (
	"os"
	"path/filepath"
	"testing"

	"pavilion/internal/corpus"
	"pavilion/internal/sim"
)

func testEngine(tb testing.TB) *Engine {
	tb.Helper()
	p := DefaultPaths()
	root := filepath.Join("..", "..")
	p.Corpus = filepath.Join(root, p.Corpus)
	p.Attributes = filepath.Join(root, p.Attributes)
	p.Model = filepath.Join(root, p.Model)
	p.ModelMeta = filepath.Join(root, p.ModelMeta)
	p.WinProb = filepath.Join(root, p.WinProb)
	p.WinMeta = filepath.Join(root, p.WinMeta)
	if _, err := os.Stat(p.Model); err != nil {
		tb.Skipf("model artifacts not built; run make model")
	}
	e, err := New(p)
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return e
}

// BenchmarkInnings measures a whole simulated innings, which is the unit the
// puzzle validator repeats ten thousand times per candidate.
func BenchmarkInnings(b *testing.B) {
	e := testEngine(b)
	key, _ := sim.DeriveDailyKey([]byte("bench"), "2026-08-28")
	p, err := e.BuildPuzzle("2026-08-28", key, 190)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		s := sim.NewChase(p)
		for !s.Done {
			legal := s.LegalBowlers()
			intent, _ := e.ChooseIntent(s)
			if _, err := sim.PlayOver(s, key, legal[0], intent, e); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkProbabilities(b *testing.B) {
	e := testEngine(b)
	key, _ := sim.DeriveDailyKey([]byte("bench"), "2026-08-28")
	p, _ := e.BuildPuzzle("2026-08-28", key, 190)
	s := sim.NewChase(p)
	st := e.SituationFor(s, 0)
	dst := make([]float64, corpus.NumOutcomes)
	b.ResetTimer()
	for b.Loop() {
		_ = e.Probabilities(st, dst)
	}
}
