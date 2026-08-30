package api

import (
	"fmt"
	"strings"
)

// ShareText builds the thing a player pastes into a group chat.
//
// It is plain text with emoji squares and no link preview to depend on, because
// the artifact has to survive being pasted anywhere. The grid is the same object
// the player watched fill in while playing: progress bar, score and share card
// are one thing rather than three.
func ShareText(r FinishResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pavilion %s · target %d\n", r.Date, r.Target)

	fmt.Fprintf(&b, "Defend  %s  ", emoji(r.DefendGrid))
	if r.Defended {
		fmt.Fprintf(&b, "won by %d\n", r.DefendMargin)
	} else {
		fmt.Fprintf(&b, "lost\n")
	}

	fmt.Fprintf(&b, "Chase   %s  ", emoji(r.ChaseGrid))
	if r.Chased {
		fmt.Fprintf(&b, "won\n")
	} else {
		fmt.Fprintf(&b, "lost by %d\n", r.ChaseMargin)
	}

	if r.Day.Runs > 1 {
		fmt.Fprintf(&b, "\n%.0f%% defended it, %.0f%% chased it\n",
			100*r.Day.DefendRate, 100*r.Day.ChaseRate)
	}
	if r.Streak > 1 {
		fmt.Fprintf(&b, "streak %d\n", r.Streak)
	}
	return b.String()
}

// emoji renders a grid of grade names as coloured squares.
func emoji(grades []string) string {
	var b strings.Builder
	for _, g := range grades {
		switch g {
		case "good":
			b.WriteString("\U0001F7E9")
		case "bad":
			b.WriteString("\U0001F7E5")
		default:
			b.WriteString("\U0001F7E8")
		}
	}
	return b.String()
}
