package sim

import (
	"errors"
	"fmt"

	"pavilion/internal/attr"
	"pavilion/internal/corpus"
	"pavilion/internal/features"
)

// This package imports corpus, attr and features for their type vocabulary
// only: the outcome enum, the handedness and bowling classes, and the shape of
// a match situation. Duplicating those definitions would be worse than the
// dependency, and none of them brings I/O, a clock or a logger into the engine.
// The engine itself remains a pure function from state and decision to state.

// MaxOvers is the length of an innings.
const MaxOvers = 20

// MaxOversPerBowler is the limit that makes the defend half a puzzle rather
// than a preference: five bowlers, four overs each, exactly twenty overs, so
// every over spent early is one unavailable at the death.
const MaxOversPerBowler = 4

// Wickets is the number a side has to lose.
const Wickets = 10

// MaxAttacks is the chase half's budget of high-intent overs.
//
// Six of twenty. The constraint is what makes the chase a puzzle rather than a
// slider: attacking is always tempting and usually correct in isolation, so
// without a budget every over would be the same choice. With one, spending an
// attack early costs you the option at the death, which is the mirror of the
// bowling decision.
const MaxAttacks = 6

// Player is one cricketer as the engine sees them.
type Player struct {
	ID    corpus.PlayerID
	Name  string
	Hand  attr.Hand
	Class attr.BowlClass

	// Team is the side the player is best known for, abbreviated, and Years is
	// the span they played across. Neither affects a single ball; they are here
	// so that the names on screen are recognisable as people rather than rows,
	// which for a player who retired a decade ago is most of what places them.
	Team  string
	Years string
}

// Puzzle is one day's fixed problem. It is identical for every player.
type Puzzle struct {
	Date   string
	Target uint16
	Venue  corpus.VenueID

	// Attack is the five bowlers dealt to defend with.
	Attack []Player
	// Batting is the chasing side's order, used by the defend half.
	Batting []Player
}

// Intent is what the batting side is trying to do in an over.
type Intent uint8

const (
	Block Intent = iota
	Rotate
	Attack
)

func (i Intent) String() string {
	switch i {
	case Block:
		return "block"
	case Attack:
		return "attack"
	}
	return "rotate"
}

// lambda is the tilt strength for each intent. Blocking trades runs for
// survival, attacking the reverse; rotating leaves the model's own view of the
// situation untouched.
func (i Intent) lambda() float64 {
	switch i {
	case Block:
		return -0.30
	case Attack:
		return 0.34
	}
	return 0
}

// ParRate is the run rate an ordinary over produces without anyone taking a
// risk for it. It is the line above which attacking is necessary and below
// which it is a choice.
const ParRate = 8.5

// recklessness is how much extra a wicket costs for attacking when the chase
// did not require it, and freeRate is the required rate below which that cost
// starts to apply at all.
//
// A fixed tilt made the timing of the attacking overs worth nothing: spending
// them in the first six overs and spending them in the last six both chased
// about 46% of the time, because the tilt shifted the distribution by the same
// amount wherever it was applied. That is not how a chase works. Swinging at
// everything while the required rate is under control is how wickets are thrown
// away for runs nobody needed; swinging when the rate has climbed is simply the
// risk the situation already forced on you.
//
// So the run side of attacking stays constant and the wicket side does not.
// The first attempt at this overcorrected. Measured over the middle overs, an
// attacking over bought about 1.2 extra runs and, with the rate under control,
// almost three times the chance of a wicket: at a required rate of three the
// wicket chance went from 2.8% to 7.9%, which is not a decision anybody should
// make and so not a decision worth offering. Worse, the penalty began at par
// itself, so an ordinary chase that was merely on schedule was already being
// charged for aggression.
//
// The cost now starts only once the chase is comfortably ahead of the rate, and
// it is roughly a third of what it was. Attacking is a genuine choice across
// most of an innings and a bad one only when the runs are not needed.
const (
	recklessness = 1.8
	freeRate     = 7.0
)

// riskFactor returns the multiplier on the chance of a wicket for an intent, in
// a chase needing a given rate.
func riskFactor(i Intent, required float64) float64 {
	if i != Attack {
		return 1
	}
	if required >= freeRate {
		return 1
	}
	// Zero at the free rate, rising as the chase gets easier and attacking
	// becomes less and less necessary.
	surplus := (freeRate - required) / freeRate
	return 1 + recklessness*surplus
}

// ApplyIntent tilts a base outcome distribution for an intent, given how fast
// the batting side still has to score.
//
// Rotating leaves the model's own view alone. The others move the distribution
// toward or away from risk, and attacking additionally raises the chance of a
// wicket when the situation did not call for it.
func ApplyIntent(base []float64, i Intent, required float64, dst []float64) {
	Tilt(base, aggressionValue, i.lambda(), dst)

	f := riskFactor(i, required)
	if f == 1 {
		return
	}

	// Raise the wicket probability and take the difference off everything else
	// in proportion, so the result is still a distribution and the shape of the
	// scoring outcomes is untouched.
	w := dst[corpus.Wicket]
	extra := w*f - w
	rest := 1 - w
	if rest <= 0 || extra <= 0 {
		return
	}
	if w+extra >= 1 {
		extra = 1 - w - 1e-9
	}
	scale := (rest - extra) / rest
	for k := range dst {
		if corpus.Outcome(k) == corpus.Wicket {
			continue
		}
		dst[k] *= scale
	}
	dst[corpus.Wicket] = w + extra
}

// RequiredRate returns the rate the batting side still has to score at.
func (s *State) RequiredRate() float64 {
	if s.BallsLeft() <= 0 {
		return ParRate
	}
	return 6 * float64(s.RunsNeeded()) / float64(s.BallsLeft())
}

// aggressionValue scores each outcome by how much it reflects going after the
// bowling. A wicket sits alongside a boundary because attacking buys both.
var aggressionValue = []float64{
	corpus.Dot:    0,
	corpus.One:    0.5,
	corpus.Two:    1.0,
	corpus.Three:  1.5,
	corpus.Four:   2.0,
	corpus.Six:    3.0,
	corpus.Wicket: 1.5,
	corpus.Wide:   0,
	corpus.NoBall: 0,
}

// Predictor supplies the base outcome distribution for a situation.
//
// It is an interface so the engine depends on nothing that reads a file. The
// production implementation wraps the calibrated model; tests supply fixed
// distributions and get an engine with no model at all.
type Predictor interface {
	Probabilities(s features.State, dst []float64) error
}

// Delivery is one resolved ball.
type Delivery struct {
	Over     uint8
	Delivery uint8 // ordinal within the over, counting extras
	Legal    bool
	Outcome  corpus.Outcome
	Runs     uint8 // runs added to the score, including extras
	Batter   Player
	Bowler   Player
	Wicket   bool
}

// State is the engine's view of an innings in progress.
type State struct {
	Puzzle *Puzzle

	Score      uint16
	Wickets    uint8
	LegalBalls uint16
	Over       uint8

	// Batting order positions. Striker and NonStriker index Puzzle.Batting.
	Striker    int
	NonStriker int
	NextBatter int

	BallsFaced       []uint16 // per batting position
	RunsScored       []uint16
	OversBowled      []uint8 // per bowler in Puzzle.Attack
	PartnershipRuns  uint16
	PartnershipBalls uint16

	// LastBowler is the bowler of the previous over; a bowler may not bowl two
	// in succession.
	LastBowler int

	// AttacksUsed counts high-intent overs spent.
	AttacksUsed uint8

	// LimitAttacks enforces the budget, in both halves.
	//
	// It used to apply only when the player was batting, on the reasoning that
	// the budget is the chase half's puzzle rather than a law of cricket. That
	// was a mistake, and rebalancing attacking exposed it: the AI side could
	// attack in all twenty overs while the player had six tokens, so the two
	// halves were not the same problem and their win rates were not comparable
	// numbers. Defending against an opponent with unlimited aggression fell to
	// a quarter of games while chasing sat above a half, and the puzzle
	// generator, which requires both halves to be a contest, could not find a
	// target that satisfied it at any score.
	//
	// The game's own description is "same score, other side". This makes that
	// true.
	LimitAttacks bool

	Done bool
}

// AttacksLeft returns the unspent budget of high-intent overs.
func (s *State) AttacksLeft() int { return MaxAttacks - int(s.AttacksUsed) }

// ErrNoAttacksLeft is returned when the attack budget is exhausted.
var ErrNoAttacksLeft = errors.New("sim: no high-intent overs left")

// NewPlayerChase starts the chase half, where the player bats.
//
// It is the same innings as the defend half, from the other chair.
func NewPlayerChase(p *Puzzle) *State { return NewChase(p) }

// NewChase starts an innings chasing the target under the attacking-over
// budget, whoever is batting.
func NewChase(p *Puzzle) *State {
	return &State{
		LimitAttacks: true,
		Puzzle:      p,
		Striker:     0,
		NonStriker:  1,
		NextBatter:  2,
		BallsFaced:  make([]uint16, len(p.Batting)),
		RunsScored:  make([]uint16, len(p.Batting)),
		OversBowled: make([]uint8, len(p.Attack)),
		LastBowler:  -1,
	}
}

// RunsNeeded returns how many more runs the batting side needs to win.
func (s *State) RunsNeeded() int {
	n := int(s.Puzzle.Target) - int(s.Score)
	if n < 0 {
		return 0
	}
	return n
}

// BallsLeft returns legal balls remaining in the innings.
func (s *State) BallsLeft() int {
	n := MaxOvers*6 - int(s.LegalBalls)
	if n < 0 {
		return 0
	}
	return n
}

// Won reports whether the chasing side has reached the target.
func (s *State) Won() bool { return s.Score >= s.Puzzle.Target }

// LegalBowlers returns the indices of bowlers who may bowl the next over.
//
// A bowler is offered only if choosing him still leaves the rest of the innings
// completable. Five bowlers of four overs is exactly twenty, with no slack, so
// a player picking greedily can otherwise reach the nineteenth over with overs
// left only for the bowler who just bowled, and no legal move. A captain is
// expected to plan around that; a daily puzzle that lets someone walk into an
// unwinnable position through an innocuous-looking choice is just unfair.
func (s *State) LegalBowlers() []int {
	var out []int
	remaining := make([]int, len(s.Puzzle.Attack))
	for i := range s.Puzzle.Attack {
		remaining[i] = MaxOversPerBowler - int(s.OversBowled[i])
	}

	for i := range s.Puzzle.Attack {
		if remaining[i] <= 0 || i == s.LastBowler {
			continue
		}
		remaining[i]--
		ok := completable(remaining, i)
		remaining[i]++
		if ok {
			out = append(out, i)
		}
	}
	return out
}

// completable reports whether the overs still owed can be bowled out without
// anyone bowling twice in a row, given who bowled last.
//
// This is the classic question of rearranging a multiset so no two neighbours
// match. Such an arrangement exists exactly when no single bowler owes more
// than half the remaining overs, rounded up. The extra wrinkle is the bowler
// who just finished: when one bowler owes exactly half of an odd number of
// overs he must take every odd position, the first included, so if that bowler
// is also the one who just bowled there is no legal arrangement at all.
func completable(remaining []int, last int) bool {
	n := 0
	most, who := 0, -1
	for i, r := range remaining {
		if r < 0 {
			return false
		}
		n += r
		if r > most {
			most, who = r, i
		}
	}
	if n == 0 {
		return true
	}
	if most > (n+1)/2 {
		return false
	}
	if n%2 == 1 && most == (n+1)/2 && who == last {
		return false
	}
	return true
}

// ErrIllegalBowler is returned when a chosen bowler cannot bowl the next over.
var ErrIllegalBowler = errors.New("sim: that bowler cannot bowl this over")

// canPick reports whether giving this bowler the next over leaves the rest of
// the innings bowlable.
func (s *State) canPick(bowler int) bool {
	remaining := make([]int, len(s.Puzzle.Attack))
	for i := range s.Puzzle.Attack {
		remaining[i] = MaxOversPerBowler - int(s.OversBowled[i])
	}
	if remaining[bowler] <= 0 {
		return false
	}
	remaining[bowler]--
	return completable(remaining, bowler)
}

// Over is the result of resolving one over.
type Over struct {
	Number     uint8
	Bowler     Player
	Intent     Intent
	Deliveries []Delivery
	Runs       uint8
	Wickets    uint8
	ScoreAfter uint16
	WktsAfter  uint8
}

// PlayOver resolves one over and returns it.
//
// This is the whole engine. It is a pure function of the state, the decision
// and the day's key: given the same three it produces the same over, on any
// machine, for ever.
func PlayOver(s *State, key DailyKey, bowler int, intent Intent, p Predictor) (Over, error) {
	if s.Done {
		return Over{}, errors.New("sim: the innings is over")
	}
	if bowler < 0 || bowler >= len(s.Puzzle.Attack) {
		return Over{}, fmt.Errorf("%w: no bowler %d", ErrIllegalBowler, bowler)
	}
	if s.OversBowled[bowler] >= MaxOversPerBowler {
		return Over{}, fmt.Errorf("%w: %s has bowled his four",
			ErrIllegalBowler, s.Puzzle.Attack[bowler].Name)
	}
	if bowler == s.LastBowler {
		return Over{}, fmt.Errorf("%w: %s bowled the previous over",
			ErrIllegalBowler, s.Puzzle.Attack[bowler].Name)
	}
	if !s.canPick(bowler) {
		return Over{}, fmt.Errorf("%w: giving %s this over would strand the innings with no legal bowler",
			ErrIllegalBowler, s.Puzzle.Attack[bowler].Name)
	}
	if intent == Attack && s.LimitAttacks && s.AttacksUsed >= MaxAttacks {
		return Over{}, ErrNoAttacksLeft
	}

	bwl := s.Puzzle.Attack[bowler]
	out := Over{Number: s.Over, Bowler: bwl, Intent: intent}

	base := make([]float64, corpus.NumOutcomes)
	tilted := make([]float64, corpus.NumOutcomes)

	var legal uint8
	var deliveryIdx uint8

	for legal < 6 {
		if s.Wickets >= Wickets || s.Won() || s.LegalBalls >= MaxOvers*6 {
			break
		}
		// A defensive bound: an over of nothing but wides would otherwise spin
		// for ever, and the delivery index is a uint8.
		if deliveryIdx >= 200 {
			break
		}

		bat := s.Puzzle.Batting[s.Striker]
		st := features.State{
			Innings:          2,
			Over:             s.Over,
			Score:            s.Score,
			Wickets:          s.Wickets,
			LegalBallsBowled: s.LegalBalls,
			Target:           s.Puzzle.Target,
			StrikerBalls:     s.BallsFaced[s.Striker],
			PartnershipRuns:  s.PartnershipRuns,
			PartnershipBalls: s.PartnershipBalls,
			Batter:           bat.ID,
			Bowler:           bwl.ID,
			BatterHand:       bat.Hand,
			BowlerClass:      bwl.Class,
			Venue:            s.Puzzle.Venue,
		}
		if err := p.Probabilities(st, base); err != nil {
			return Over{}, fmt.Errorf("sim: predict: %w", err)
		}
		ApplyIntent(base, intent, s.RequiredRate(), tilted)

		u := Draw(key, Coord{
			Innings:  2,
			Over:     s.Over,
			Delivery: deliveryIdx,
			Choice:   EncodeChoice(bowler, intent),
		})
		outcome := corpus.Outcome(Sample(tilted, u))

		d := Delivery{
			Over:     s.Over,
			Delivery: deliveryIdx,
			Outcome:  outcome,
			Batter:   bat,
			Bowler:   bwl,
		}

		switch outcome {
		case corpus.Wide, corpus.NoBall:
			d.Runs = 1
			s.Score++
			s.PartnershipRuns++
		case corpus.Wicket:
			d.Legal, d.Wicket = true, true
			legal++
			s.LegalBalls++
			s.BallsFaced[s.Striker]++
			s.PartnershipBalls++
			s.Wickets++
			s.PartnershipRuns, s.PartnershipBalls = 0, 0
			out.Wickets++
			if s.NextBatter < len(s.Puzzle.Batting) {
				s.Striker = s.NextBatter
				s.NextBatter++
			}
		default:
			runs := runsOf(outcome)
			d.Legal = true
			d.Runs = runs
			legal++
			s.LegalBalls++
			s.BallsFaced[s.Striker]++
			s.RunsScored[s.Striker] += uint16(runs)
			s.PartnershipBalls++
			s.PartnershipRuns += uint16(runs)
			s.Score += uint16(runs)
			if runs%2 == 1 {
				s.Striker, s.NonStriker = s.NonStriker, s.Striker
			}
		}

		out.Runs += d.Runs
		out.Deliveries = append(out.Deliveries, d)
		deliveryIdx++
	}

	// Ends change between overs.
	s.Striker, s.NonStriker = s.NonStriker, s.Striker

	if intent == Attack {
		s.AttacksUsed++
	}
	s.OversBowled[bowler]++
	s.LastBowler = bowler
	s.Over++

	if s.Wickets >= Wickets || s.Won() || s.Over >= MaxOvers {
		s.Done = true
	}

	out.ScoreAfter = s.Score
	out.WktsAfter = s.Wickets
	return out, nil
}

func runsOf(o corpus.Outcome) uint8 {
	switch o {
	case corpus.One:
		return 1
	case corpus.Two:
		return 2
	case corpus.Three:
		return 3
	case corpus.Four:
		return 4
	case corpus.Six:
		return 6
	}
	return 0
}

// Result is how a completed run finished.
type Result struct {
	Defended    bool // the chasing side fell short
	Score       uint16
	Wickets     uint8
	BallsUsed   uint16
	Margin      int // runs short when defended, wickets in hand when chased
	MarginRuns  int
	MarginWkts  int
	AllOut      bool
	TargetMet   bool
	OversPlayed uint8
}

// Result summarises a finished innings.
func (s *State) Result() Result {
	r := Result{
		Score:       s.Score,
		Wickets:     s.Wickets,
		BallsUsed:   s.LegalBalls,
		AllOut:      s.Wickets >= Wickets,
		TargetMet:   s.Won(),
		OversPlayed: s.Over,
	}
	if r.TargetMet {
		r.Defended = false
		r.MarginWkts = Wickets - int(s.Wickets)
		r.Margin = r.MarginWkts
	} else {
		r.Defended = true
		r.MarginRuns = int(s.Puzzle.Target) - int(s.Score)
		r.Margin = r.MarginRuns
	}
	return r
}

// TiltFor applies an intent to a base distribution at a given required rate. It
// is exported so the engine can evaluate what an intent would do without
// playing the over.
func TiltFor(base []float64, intent Intent, required float64, dst []float64) {
	ApplyIntent(base, intent, required, dst)
}
