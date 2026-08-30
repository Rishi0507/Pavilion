package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"pavilion/internal/corpus"
)

// Inference of bowling type from delivery signatures, for players the sources
// could not resolve.
//
// Nothing here ever reaches the main attribute table. Inferred values are
// written to a separate review file with their evidence and a confidence, for a
// human who knows the players to accept or reject. That separation is the whole
// point: a wrong value carrying the same provenance as a sourced one would
// silently poison every matchup rate downstream.
//
// Only the pace-versus-spin axis is inferable. Which arm a bowler uses leaves
// no trace in ball-by-ball data at all, so it is never guessed.

// Signature is the evidence behind one inference.
type Signature struct {
	PlayerID    corpus.PlayerID
	CricsheetID string
	Name        string
	BallsBowled int

	PowerplayShare float64 // share of deliveries bowled in overs 1-6
	MiddleShare    float64 // overs 7-15
	DeathShare     float64 // overs 16-20
	StumpingRate   float64 // stumpings per 1000 deliveries
	NoBallRate     float64 // no-balls per 1000 deliveries

	Guess      string  // "spin" or "pace"
	Confidence float64 // 0 to 1
	Reason     string

	// What the source said, when it said anything. A row where the source gave
	// a bowling type but omitted the arm is a far easier review than a row with
	// no information at all, and the reviewer should be able to see which is
	// which without opening another file.
	BatRaw  string
	BowlRaw string
	Source  string
}

// inferBowlingFamily scores a bowler as pace or spin from how they are used and
// what happens off them.
//
// Two signals do most of the work. Stumpings happen almost exclusively off spin,
// because only a spinner is slow enough for a keeper to stand up; a single
// stumping is therefore strong evidence. Deployment is the other: captains bowl
// quicks in the powerplay and at the death, and spinners through the middle.
// No-balls lean pace, since the overstep is a fast bowler's error.
func inferBowlingFamily(s *corpus.Store, p corpus.PlayerID) Signature {
	sig := Signature{PlayerID: p, CricsheetID: s.Players[p].CricsheetID, Name: s.Players[p].Name}

	var pp, mid, death, stumpings, noballs, legal int
	for i := range s.D.Bowler {
		if s.D.Bowler[i] != p {
			continue
		}
		if s.Inn.SuperOver[s.D.Innings[i]] {
			continue
		}
		if s.D.Legal[i] {
			legal++
			switch corpus.PhaseOf(s.D.Over[i]) {
			case corpus.PhasePowerplay:
				pp++
			case corpus.PhaseMiddle:
				mid++
			case corpus.PhaseDeath:
				death++
			}
		}
		if s.D.Wicket[i] == corpus.WicketStumped {
			stumpings++
		}
		if s.D.NoBalls[i] > 0 {
			noballs++
		}
	}
	if legal == 0 {
		return sig
	}

	sig.BallsBowled = legal
	sig.PowerplayShare = float64(pp) / float64(legal)
	sig.MiddleShare = float64(mid) / float64(legal)
	sig.DeathShare = float64(death) / float64(legal)
	sig.StumpingRate = 1000 * float64(stumpings) / float64(legal)
	sig.NoBallRate = 1000 * float64(noballs) / float64(legal)

	// A score above zero leans spin, below zero leans pace.
	score := 0.0
	switch {
	case stumpings >= 3:
		score += 2.0
	case stumpings > 0:
		score += 1.2
	}
	score += 2.0 * (sig.MiddleShare - 0.5)
	score -= 2.0 * (sig.PowerplayShare + sig.DeathShare - 0.5)
	// No-balls lean pace, but only weakly. The rate is expressed per delivery
	// here rather than per thousand so this term stays on the same scale as the
	// share terms above; weighting it per thousand lets a single overstep swamp
	// every other signal.
	score -= 20.0 * float64(noballs) / float64(legal)

	sig.Guess = "pace"
	if score > 0 {
		sig.Guess = "spin"
	}

	// Map the score to a confidence, and damp it hard when the sample is thin.
	conf := absf(score) / 2.5
	conf = min(conf, 0.95)
	if legal < 300 {
		conf *= float64(legal) / 300
	}
	sig.Confidence = conf

	switch {
	case stumpings >= 3:
		sig.Reason = fmt.Sprintf("%d stumpings off %d balls; middle-overs share %.0f%%",
			stumpings, legal, 100*sig.MiddleShare)
	case stumpings > 0:
		sig.Reason = fmt.Sprintf("%d stumping(s); middle-overs share %.0f%%", stumpings, 100*sig.MiddleShare)
	default:
		sig.Reason = fmt.Sprintf("no stumpings; powerplay %.0f%%, middle %.0f%%, death %.0f%%, no-balls %.1f/1000",
			100*sig.PowerplayShare, 100*sig.MiddleShare, 100*sig.DeathShare, sig.NoBallRate)
	}
	return sig
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// writeReview writes the review file a human resolves by hand.
//
// The columns after the evidence are deliberately blank: a reviewer fills in
// batting_hand and bowling_class, and the file is then promoted into
// manual.csv. Nothing here is ever merged automatically.
func writeReview(path string, sigs []Signature) error {
	sort.Slice(sigs, func(i, j int) bool {
		if sigs[i].BallsBowled != sigs[j].BallsBowled {
			return sigs[i].BallsBowled > sigs[j].BallsBowled
		}
		return sigs[i].CricsheetID < sigs[j].CricsheetID
	})

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create review file: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	header := []string{
		"cricsheet_id", "name", "balls_bowled",
		"inferred_family", "confidence", "evidence",
		"source_batting_raw", "source_bowling_raw", "source",
		"powerplay_share", "middle_share", "death_share", "stumping_rate_per_1000", "noball_rate_per_1000",
		"batting_hand", "bowling_class", "reviewer_notes",
	}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write review file: %w", err)
	}
	for _, s := range sigs {
		if err := w.Write([]string{
			s.CricsheetID, s.Name, fmt.Sprint(s.BallsBowled),
			s.Guess, fmt.Sprintf("%.2f", s.Confidence), s.Reason,
			s.BatRaw, s.BowlRaw, s.Source,
			fmt.Sprintf("%.3f", s.PowerplayShare),
			fmt.Sprintf("%.3f", s.MiddleShare),
			fmt.Sprintf("%.3f", s.DeathShare),
			fmt.Sprintf("%.2f", s.StumpingRate),
			fmt.Sprintf("%.2f", s.NoBallRate),
			"", "", "",
		}); err != nil {
			return fmt.Errorf("write review file: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush review file: %w", err)
	}
	return nil
}

// jsonMarshalIndent is a small helper keeping the JSON formatting consistent
// with the other generated reports.
func jsonMarshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return append(b, '\n'), nil
}
