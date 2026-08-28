package main

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"strconv"

	"manhattan/internal/corpus"
)

// Win probability training data.
//
// The label is simply whether the chasing side went on to win, so every ball of
// every completed second innings is one example. The features are the four
// numbers that actually determine a chase, plus the target itself, because
// needing 40 off 30 with a target of 240 is not the same problem as needing 40
// off 30 chasing 140: the batting side that got there is different.
var wpFeatures = []string{
	"runs_required",
	"balls_remaining",
	"wickets_in_hand",
	"required_rate",
	"target",
	"venue_run_rate",
}

// exportWinProb writes the win-probability training matrices.
func exportWinProb(log *slog.Logger, st *corpus.Store, venueRate func(corpus.VenueID) float32,
	cutoff uint16, trainPath, testPath string) error {

	train, closeTrain, err := newNamedWriter(trainPath, wpFeatures)
	if err != nil {
		return err
	}
	defer closeTrain()
	test, closeTest, err := newNamedWriter(testPath, wpFeatures)
	if err != nil {
		return err
	}
	defer closeTest()

	rec := make([]string, len(wpFeatures)+2)
	var nTrain, nTest, wonTrain int

	for inn := range st.Inn.Len() {
		if st.Inn.SuperOver[inn] {
			continue
		}
		target := st.Inn.Target[inn]
		if target == 0 {
			continue // first innings: there is nothing to chase yet
		}
		m := st.Inn.Match[inn]
		winner := st.M.Winner[m]
		if winner == corpus.NoTeam {
			continue // abandoned or tied: no outcome to learn from
		}

		won := 0
		if winner == st.Inn.BattingTeam[inn] {
			won = 1
		}
		season := st.M.Season[m]
		venue := venueRate(st.M.Venue[m])

		for j := st.Inn.Start[inn]; j < st.Inn.End[inn]; j++ {
			i := int(j)
			ballsLeft := 120 - int(st.D.LegalBallsBefore[i])
			if ballsLeft <= 0 {
				continue
			}
			need := int(target) - int(st.D.ScoreBefore[i])
			if need <= 0 {
				continue // already won; nothing left to predict
			}
			wkts := 10 - int(st.D.WicketsBefore[i])

			vals := []float64{
				float64(need),
				float64(ballsLeft),
				float64(wkts),
				6 * float64(need) / float64(ballsLeft),
				float64(target),
				float64(venue),
			}
			for k, v := range vals {
				rec[k] = strconv.FormatFloat(v, 'g', -1, 32)
			}
			rec[len(wpFeatures)] = strconv.Itoa(won)
			rec[len(wpFeatures)+1] = strconv.Itoa(int(season))

			if season <= cutoff {
				nTrain++
				wonTrain += won
				_ = train.Write(rec)
			} else {
				nTest++
				_ = test.Write(rec)
			}
		}
	}

	train.Flush()
	test.Flush()
	if err := train.Error(); err != nil {
		return fmt.Errorf("write %s: %w", trainPath, err)
	}
	if err := test.Error(); err != nil {
		return fmt.Errorf("write %s: %w", testPath, err)
	}
	log.Info("win probability rows written",
		"train", nTrain, "test", nTest,
		"chases_won_in_training", fmt.Sprintf("%.1f%%", 100*float64(wonTrain)/float64(max(nTrain, 1))))
	return nil
}

func newNamedWriter(path string, names []string) (*csv.Writer, func(), error) {
	header := make([]string, 0, len(names)+2)
	header = append(header, names...)
	header = append(header, "label", "season")
	return newWriterWithHeader(path, header)
}
