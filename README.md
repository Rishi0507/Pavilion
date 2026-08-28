# Manhattan

A daily T20 cricket puzzle. One target score per day, the same for every player
in the world. You play both innings of that target: first you defend it with
five bowlers and a four-over limit each, then you chase it with a fixed budget
of high-intent overs.

The game ships as **Par**. `Manhattan` is the project and repository name, after
the per-over bar chart that gives the game its visual language.

**Status: milestone 9 of 11. Playable.** `make play`

---

## Quick start

```sh
make data    # download the Cricsheet IPL archive and people register
make etl     # build the binary corpus and the data quality report
make attrs   # resolve batting handedness and bowling type
make rates   # fit the hierarchical shrunk player rates
make features # export the training matrix
make model   # train and calibrate the ball outcome model (needs uv)
make check   # fail if data quality has regressed
make test    # run the test suite

# ask the corpus questions
go run ./cmd/parquery dist -bowler "JJ Bumrah" -phase death -vs-hand LHB
go run ./cmd/parquery matchup -batter "V Kohli" -bowler "JJ Bumrah"
go run ./cmd/parquery worst -batter "N Pooran"
go run ./cmd/parquery graph
```

On Windows without `make` on PATH, use `mingw32-make`, or run the underlying
commands directly:

```sh
go run ./cmd/paretl
go test ./...
```

`make etl` writes three files into `data/out/`:

| File | |
|---|---|
| `corpus.bin` | the ball-by-ball corpus, loaded whole into memory at boot |
| `quality.json` | the data quality report, machine-readable, diffed in CI |
| `quality.md` | the same report, for humans |

`make attrs` writes into `data/attributes/`:

| File | |
|---|---|
| `players.csv` | the attribute table, keyed by Cricsheet id, committed |
| `manual.csv` | hand-authored corrections; always wins over sourced values |
| `review_inferred.csv` | unresolved players with a guess and its evidence, for a human |
| `coverage.json` | per-attribute coverage |

`corpus.bin` and the HTTP cache are build artifacts and are not committed. The
quality reports and the attribute table are committed, so that a regression in
coverage shows up as a diff.

### The quality gate

`make check` rebuilds the report and compares it to `data/quality_baseline.json`,
failing on any regression: attribute coverage falling, the corpus shrinking, or
a new integrity failure. It compares substantive fields only, because the report
carries a generation timestamp and would otherwise differ on every run. An
intended change is accepted deliberately with `make baseline`.

---

## Data source and licence

All ball-by-ball data comes from [Cricsheet](https://cricsheet.org), compiled by
Stephen Rushe.

**Licence: Open Data Commons Attribution License 1.0 (ODC-BY 1.0).**
<http://opendatacommons.org/licenses/by/1.0/>

**This needs a human re-check before launch.** The ODC-BY 1.0 statement appears
on <https://cricsheet.org/register/> and *only* there. It is absent from the
downloads page, absent from the JSON format documentation, and absent from the
site homepage; all three were checked. A licence that is stated in exactly one
non-obvious place is a licence worth confirming with the maintainer directly
before anything ships publicly.

The practical consequence is better than expected. ODC-BY is an
**attribution-only** licence, not a share-alike one: derived databases and
models may be distributed under whatever terms we choose, provided Cricsheet is
attributed. Neither ODbL nor CC BY-SA applies, and there is no copyleft
obligation on the corpus, the trained models, or the game.

## Attribution

Both sources are credited in the application footer and in every generated
artifact. The required footer text is:

> Ball-by-ball data from **Cricsheet** (cricsheet.org), by Stephen Rushe, used
> under ODC-BY 1.0. Player attributes from **Wikipedia**, used under CC BY-SA 4.0.
> An unofficial fan project, not affiliated with the BCCI or the IPL.

Player attributes come from English Wikipedia and are therefore **CC BY-SA 4.0**,
which is share-alike. This matters and differs from the Cricsheet position: the
attribute table is a derived work of Wikipedia text and carries a share-alike
obligation, whereas the ball-by-ball corpus does not. The two are kept in
separate files for exactly this reason, so the obligation stays scoped to
`data/attributes/` rather than spreading to the corpus or the models.

## Legal

An unofficial fan project, not affiliated with, endorsed by, or connected to the
BCCI, the IPL, or any franchise. Player names are used as factual data. Team
names, crests and kit colours are trademarked and are not used: the game refers
to sides by city.

---

## Architecture

### Layout

```
cmd/
  parsrv/     HTTP server
  paretl/     Cricsheet ingest -> binary corpus + data quality report
  parpuzzle/  daily puzzle generation and Monte Carlo validation
internal/
  corpus/     in-memory ball-by-ball store, struct-of-arrays
  graph/      player and matchup property graph, CSR adjacency
  sim/        the match engine: pure, deterministic, no I/O
  model/      inference wrappers
  puzzle/     daily puzzle types, target selection
  session/    run state, HMAC tokens, anti-replay
  api/        handlers and DTOs
  store/      Postgres for runs, results, leaderboards
web/          frontend
ml/           model training, pinned environment
data/         raw downloads and generated artifacts (not committed)
```

### Three things that are not negotiable

**Randomness is pre-committed, not rolled.** Every random draw is derived
statelessly from a counter keyed by the day and the ball's coordinates, never
from a sequential stream. Two players who make identical decisions get identical
results, and the luck met in over 17 does not depend on what was done in over 5.
Each ball consumes exactly one uniform draw, mapped through an inverse CDF whose
shape depends on the chosen matchup, so the draw stays fixed while the decision
changes what it means.

**The server is authoritative.** The client never simulates. It sends a decision
and receives the resolved over. Runs are HMAC-signed sessions with a monotonic
decision counter; out-of-order and replayed decisions are rejected.

**The daily target is chosen by simulation.** Candidate puzzles are Monte
Carloed offline against a reference policy and accepted only if both halves are
competitive and the decisions carry real weight. Puzzles are never generated at
request time.

### Storage

The corpus is not in a database. IPL ball-by-ball is under 300,000 deliveries
and roughly 8 MB encoded, so it is held entirely in memory as parallel typed
slices and scanned directly. The dominant query is an aggregation over a filter,
which is a columnar scan, not a traversal; a graph or SQL database would add a
query planner and lose to a linear scan over eight megabytes.

A property graph is built in process, over CSR adjacency, for the things that
genuinely are graph problems: the matchup network and player similarity.
Postgres holds runs, results and leaderboards, which are genuinely relational.

---

## Milestones

1. **ETL** — Cricsheet to binary corpus, data quality report, entity resolution ✅
2. **Corpus and CSR matchup graph, sub-10ms aggregation** ✅
3. **Hierarchical shrinkage on player rates** ✅
4. **Ball outcome model, calibrated** ✅
5. **The simulator: deterministic, pure, tested** ✅
6. **Defend half, playable and ugly** ✅ — the gate; see findings
7. **Win probability model and the share grid** ✅
8. **Chase half** ✅
9. **Daily pipeline** ✅
10. Design pass
11. Leaderboards, stats, sharing, streaks

## Milestone 9: the daily pipeline

Candidates are Monte Carloed offline against a reference policy, and only
survivors reach the queue. Nothing is generated at request time, because a
puzzle generated on demand cannot be checked, and an unchecked puzzle is how a
day arrives where everyone defends comfortably and the comparison means nothing.

The whole tuple is validated, not the target. The attack, the chasing side and
the ground are fixed before the simulation runs, and the decision spread is
measured over the attack actually dealt.

**The band is satisfiable but narrow, exactly as feared.** A sweep across four
attacks and six targets shows the two halves moving in opposition:

| attack | target | defend | chase | spread |
|---:|---:|---:|---:|---:|
| 0 | 180 | 0.247 | 0.550 | 0.0202 |
| 0 | **195** | **0.397** | **0.470** | 0.0221 |
| 0 | 210 | 0.557 | 0.297 | 0.0122 |

That was the objection raised before any code was written, and it is now
measured rather than guessed. It also made three of the pipeline's own numbers
wrong, each chosen before anything had been measured:

- **The decision-spread threshold was invented.** It was set to 0.030; nothing
  in the entire sweep exceeds 0.023, so every candidate was rejected, including
  the well-balanced ones. It is now 0.018, calibrated against the observed range
  of 0.006 to 0.023.
- **The reference chase policy was the worst of three already measured.** It
  chased 40.7% where simply spending the attacking budget managed 46.3%. Using
  it as the yardstick made every chase look harder than it is, which pushed the
  target search toward scores only the defending half could win, and starved the
  queue.
- **The target was sampled rather than solved.** For a fixed tuple the defend
  rate rises with the score and the chase rate falls, so their difference is
  monotone and the crossing point can be bisected. Solving instead for an even
  *defence* puts the target near 210, where defending is a coin toss but chasing
  wins barely 30%. Equalising the two lands inside the band by construction.

The sweep tool is kept rather than thrown away: a search that rejects everything
is useless without a way to see what it was rejecting.

With all three corrected, candidates land in the band routinely and the
remaining rejections are on the quality filter alone, which is what a quality
filter is for. A queued day looks like this:

    2026-08-29  target 202 at Eden Gardens
      attack: M Theekshana, Sakib Hussain, J Little, AK Markram, Yash Thakur
      reference play: defends 36.5%, chases 44.9%, decision spread 0.0193

Note the attack: two front-line bowlers, a left-arm quick, and Markram's
part-time off spin. Finding four overs for the fifth bowler is the problem the
day is actually setting.

## Milestones 5 to 8: the game

    Par 2026-08-28 · target 195
    Defend  🟩🟥🟥🟨🟥🟥🟥🟥🟥🟨🟨🟨🟨🟨🟨🟥  lost by 9 wickets
    Chase   🟨🟨🟩🟥🟩🟩🟩🟨🟨🟨🟥🟥🟥🟥🟨🟥🟨🟨🟨🟥  lost by 31

### The simulator

Every draw is derived statelessly from the ball's coordinates, keyed by the day.
There is no generator threaded through the innings whose position depends on how
many balls have been bowled; there is a keyed function from (innings, over,
delivery) to a number, and exactly one draw is consumed per delivery, mapped
through an inverse CDF whose shape depends on the decision taken.

That is what makes the daily comparison mean anything: the number is fixed by
where the ball sits, and the decision changes only what it is worth. Three tests
pin it — a thousand repetitions of one decision sequence producing identical
output, a check that no two coordinates share a draw, and a property test over
two hundred shuffled bowling orders confirming every over's luck stays put.

The delivery index counts every ball including wides, not legal balls only.
Using the legal index would give two deliveries in an over containing an extra
the same coordinate, and they would share a draw.

**The tests found a real design flaw.** Five bowlers of four overs is exactly
twenty, with no slack, so picking greedily can reach the nineteenth over with
overs left only for the bowler who just bowled: no legal move. Real captains
plan around it; a daily puzzle that lets someone walk into an unwinnable
position through an innocuous choice is unfair. `LegalBowlers` now offers only
choices that leave the innings completable.

### The gate

Judged by whether the decision carries weight it passes; judged by the specific
tactic the brief names it half passes, and the difference is worth stating.

Four hundred automated runs per policy, defending 210:

| Policy | Defended |
|---|---:|
| Bowl the best available every over | 57.5% |
| Hold the two best back for the death | 57.2% |
| Arbitrary legal choice | 48.5% |
| Bowl the worst available | 46.5% |

Playing well is worth about **nine points of win probability** over choosing
arbitrarily. Two quite different good strategies tie, so there is no single
trivial answer.

**Getting there took two corrections, and the first attempt failed.** The
original build showed the best policy at 51.5% against an arbitrary one at
52.0%: the decision was decoration. Two causes, both real:

- **The puzzle dealt five elite internationals.** Rabada, Shami, Ashwin, Jadeja
  and Curran are interchangeable, and measurement says so: barely a run an over
  between them. A real T20 side has three or four bowlers it trusts and a fifth
  who is a batter who bowls a bit, and hiding that fifth bowler's four overs is
  the oldest captaincy problem in the format. Attacks are now stratified by
  shrunk death-overs economy, taking from the ends rather than evenly.
- **The outcome model compressed bowler quality.** It is trained to predict one
  delivery, where the situation dominates and bowler identity is a weak signal.
  It is right about that and it is calibrated, but it flattened the spread to
  about a run an over in *every* phase, where the shrunk rate table measures
  1.00 in the powerplay, 1.72 through the middle and 1.76 at the death. A
  constant spread is fatal here: if the best bowler is best everywhere, there is
  nothing to weigh. The engine now tilts the model's distribution until its
  expected runs match what the rates say, which restores the level without
  disturbing the situational response. The spread now runs 0.43 in the powerplay
  to 2.03 at the death.

### The chase half

Six attacking overs in twenty. Three hundred runs per policy, chasing 195:

| Policy | Chased |
|---|---:|
| Attack in the first six overs | 46.3% |
| Attack in the last six | 46.0% |
| Attack when the rate demands it | 40.7% |
| Never attack | 26.3% |

**Spending the budget matters enormously and the timing does not.** Twenty
points between using the tokens and hoarding them; nothing between using them
early and late. That is a weaker puzzle than the defend half, and it is an
honest result rather than one to tune away. The likely cause is that the tilt
model of intent is too blunt: a fixed shift toward boundaries and wickets does
not capture that attacking is far more valuable when the required rate is
climbing than when it is not. Worth revisiting before launch.

### Win probability

Held out on IPL 2025 and 2026: log loss 0.425 against a base rate of 0.688,
AUC 0.904, Brier 0.137. Monotonicity is constrained during training and
re-verified in Go, because a probability that rises when you need more runs
would colour a good over red and players notice that immediately.

Three cases are decided by the rules of cricket rather than the model, because
trees cannot extrapolate and would otherwise answer them from whichever leaf the
inputs landed in: the target reached, no wickets or balls left, and needing more
runs than there are balls to score six off. Before that rule, the model gave a
hopeless chase a 14% chance.

**One thing did not work.** Chases succeed markedly more often in 2025 and 2026
than a model trained on earlier seasons expects: it predicts 46.5% where 55.8%
actually happened. Weighting recent seasons more heavily helps a little at 0.95
per season and hurts at 0.85, because shrinking the effective sample costs more
than the era correction gains. The residual gap is left documented rather than
tuned around.

Python and Go agree to 1.11e-16.

## Milestone 4 findings

A LightGBM multiclass model over the nine outcomes a delivery can produce,
trained on 240,140 deliveries and evaluated on the 28,228 balls of IPL 2025 and
2026, which the model never sees.

| Held-out log loss | |
|---|---:|
| Population baseline | 1.67528 |
| Model, uncalibrated | 1.61842 |
| **Model, calibrated** | **1.61665** |

3.5% better than predicting the population average. That is a modest-sounding
number and it is the right one to expect: a single delivery is close to
irreducibly random, and anything claiming a large improvement here is reading
its own answer. Mean expected calibration error is 0.0072 after temperature
scaling; reliability diagrams for all nine outcomes are committed in
[`ml/reports/`](ml/reports/).

### The first version was worse than the baseline

It scored 2.475 against a baseline of 1.675, while validating at 1.44. A gap
that size is never distribution shift.

The cause was the matchup features, which held **39% of total gain**. The graph
stores career head-to-head totals, so the edge "Kohli has 159 off Bumrah in 108
balls" already contains the delivery being predicted. The model learned to read
the answer off its own features, and on seasons absent from the graph the crutch
disappeared.

The fix was to featurise on an expanding window: head-to-head records
accumulate match by match and are folded in only once a match is complete, and
the rate table is refitted for every season on the seasons strictly before it,
nineteen fits in all. `TestWalkDoesNotLeakTheCurrentMatch` is the regression
guard. After the fix the important features are situational, as they should be:
score, balls remaining, how long the striker has been in, the over.

### No ONNX

The brief specified ONNX. This serves the model by reading LightGBM's own text
format and walking the trees in pure Go, because `onnxruntime-go` needs cgo and
a bundled shared library, which turns a static cross-compilable binary into a
platform-specific one with a native dependency, and buys nothing for 585
decision trees.

The result is worth the deviation: **Python and Go agree to 2.22e-16**, machine
epsilon, against a required tolerance of 1e-6. `TestPythonGoParity` checks 500
held-out rows against the probabilities Python produced at export time.

Inference is 36 microseconds a ball. That is ample for serving; puzzle
validation simulates millions of deliveries and will want batched prediction
across simulations, which belongs with that work.

### No train/serve skew by construction

Features are computed once, in Go, by `internal/features`, and both the training
exporter and the server call it. The usual way a model works in the notebook and
fails in production is two feature implementations drifting apart; here the only
thing that can differ is the model evaluation, which is exactly what the parity
test pins.

## Milestone 3 findings

Every player rate the simulator will see is a posterior mean under a
Dirichlet-multinomial whose prior concentration is fitted per cell by marginal
likelihood. Rates are cut by role, phase, and matchup class: for a bowler the
opposition is the batter's handedness, for a batter it is pace against spin.
Twelve cells in all.

**This is done in Go with empirical Bayes rather than in PyMC.** The simulator
needs posterior means, not credible intervals, and for point estimates the
marginal-likelihood fit lands in the same place as MCMC while staying
deterministic, dependency-free and fast enough to run inside the pipeline
(123 ms for the whole table). If uncertainty intervals or a deeper hierarchy
are needed later, NumPyro is the upgrade and the cell structure carries over
unchanged.

The fitted concentrations run from 107 to 291 deliveries. The number is directly
interpretable: a bowler needs about 270 death-over balls, roughly 45 overs,
before his own record outweighs the population in his rating.

**The sanity check the brief demands passes.** Sorted by shrunk death-overs
economy, the top of the table is:

| # | Bowler | Balls | Shrunk econ | Raw econ | Weight |
|---:|---|---:|---:|---:|---:|
| 1 | SP Narine | 1081 | 8.39 | 7.60 | 0.68 |
| 2 | SL Malinga | 1118 | 8.71 | 8.03 | 0.68 |
| 3 | JJ Bumrah | 1431 | 9.02 | 8.58 | 0.74 |
| 4 | R Ashwin | 606 | 9.17 | 8.41 | 0.55 |
| 5 | DW Steyn | 626 | 9.25 | 8.60 | 0.59 |

Malinga and Bumrah in the top three is what anyone who watches the IPL would
predict. The batting leaderboards behave the same way: de Villiers, Russell,
Tim David, Pant and Buttler at the death; Suryavanshi, Head, Abhishek Sharma
and Narine in the powerplay.

**And the shrinkage earns its keep.** Shreyas Gopal has bowled 56 death-over
balls at an economy of 6.70. Unshrunk, that would make him the best death bowler
in the game by a distance, on nine overs of evidence. His posterior is 9.66,
with a weight of 0.11: the model reports, correctly, that almost nothing about
him is known.

Full leaderboards, including every fitted concentration, are in
[`data/out/rates.md`](data/out/rates.md).

## Milestone 2 findings

Aggregation, the matchup graph, and `parquery`. Measured on the full corpus:

| Operation | |
|---|---:|
| Filtered aggregation over 295,215 deliveries | **1.17 ms** |
| The same with an attribute mask | 1.56 ms |
| Matchup lookup, CSR binary search | **34.5 ns** |
| Corpus load at boot | 14 ms |
| Graph build at boot | 59 ms |

The milestone asked for a sub-10ms answer to "Bumrah's death-overs distribution
against left-handers". It runs in 1.17 ms, scanning every delivery with no
index. That is the whole argument for keeping the corpus in memory rather than
in a database.

**The matchup effects are real and computed, not asserted.** Ashwin's off-spin
turns away from a left-hander, which is the harder matchup for the batter, and
the corpus shows exactly that:

| R Ashwin | Economy | Strike rate against |
|---|---:|---:|
| vs left-handers | 6.76 | 108.7 |
| vs right-handers | 7.68 | 121.5 |

Phase behaviour comes out with the right shape too: economy 8.09 / 7.95 / 10.06
across powerplay, middle and death, with dot rate falling monotonically
(44.2% → 29.0% → 22.9%) and boundary rate U-shaped (20.4% → 14.2% → 20.6%) as
field restrictions give way to consolidation and then to all-out attack.

The graph holds **31,325 matchup edges** over 964 player nodes, in both
directions, built once at boot and never mutated.

Attribute coverage over the 483 eligible players: **97.9% batting hand, 96.3%
bowling class**, from 12 HTTP requests. 471 players are dealable; the 12
exclusions are listed in the quality report.

## Milestone 1 findings

The full report is in [`data/out/quality.md`](data/out/quality.md).

- **1,237 matches, 295,215 deliveries, 964 players**, IPL 2008 to 2026. Six
  abandoned matches with no second innings are excluded.
- **Entity resolution is effectively free.** Every match file embeds a
  `registry.people` map from display name to a stable person identifier, so
  cross-season name drift is resolved upstream. 100% of deliveries resolve to a
  registered player; 100% of players carry a Cricinfo id for later attribute
  sourcing.
- **One display name is genuinely ambiguous.** "Harmeet Singh" is two different
  cricketers. The corpus keys players by identifier, never by name, so they stay
  distinct; keying by name would have merged them silently.
- **Over indexing is verified, not assumed.** Wides and no-balls do not advance
  the legal-ball count, so an over can run to nine deliveries. The ETL computes
  the legal-ball index independently and cross-checks it against Cricsheet's
  `actual_delivery` on every ball: 0 mismatches across 295,215 deliveries, with
  the 8 umpire-miscounted overs exempted explicitly.
- **The season label cannot be trusted.** Cricsheet labels IPL 2008 as
  `2007/08` and IPL 2020 as `2020/21`. The edition year is taken from the match
  date instead; taking the later half of the label would misdate IPL 2020 and
  shift any held-out-season split by a year.
- **The known gap is now closed.** Batting handedness and bowling type are
  sourced for the 483 eligible players: **97.9% batting hand, 95.1% bowling
  class**, in 12 HTTP requests, with 17 bowlers left for human review.
