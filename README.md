# Pavilion

A deterministic T20 match engine and daily decision problem, built over roughly
260,000 ball by ball IPL deliveries.

One target per day, identical for every player in the world. You play both
innings of it: first you defend the score under a five bowler, four over
allocation constraint, then you chase the same score under a budget of six
attacking overs. An instance counts as solved only when both halves are won.

Play is evaluated on decision quality rather than realised outcome. Every choice
is scored against the alternatives available in that state, before the delivery
is resolved, so a well played defeat can outrank a fortunate win.

**Status:** all features implemented, running locally, no deployment. Two things
are outstanding before it could face the public:

- Seven validated days are queued, covering 2026-08-30 to 2026-09-05. Past that
  the server falls back to generating an unvalidated instance and flags it in
  the interface. Regenerating the queue is a manual step with no scheduler
  behind it.
- `PAVILION_SECRET` is still a development default. See
  [Configuration](#configuration).

Neither blocks local play.

```sh
go run ./cmd/pavsrv     # then open http://127.0.0.1:8080
```

---

## Contents

- [Quick start](#quick-start)
- [Configuration](#configuration)
- [How the game works](#how-the-game-works)
- [Architecture](#architecture)
- [Repository layout](#repository-layout)
- [Data sources and licensing](#data-sources-and-licensing)
- [Attribution](#attribution)
- [Legal](#legal)
- [Development](#development)

---

## Quick start

Pavilion needs a corpus, a fitted rate table and two trained models before it
can serve an instance. The pipeline is reproducible from public data, and each
step is a Make target.

GNU Make is a convenience rather than a requirement: every target is a one line
`go run`, and the Makefile shows the exact command. Note that on Windows the
binary is frequently installed as `mingw32-make` rather than `make`, in which
case substitute it below.

```sh
make data      # download the Cricsheet IPL archive and the people register
make etl       # build the binary corpus and the data quality report
make attrs     # resolve batting handedness and bowling type
make rates     # fit the hierarchical shrunk player rates
make graph     # build the batter versus bowler matchup graph
make features  # export the training matrix
make model     # train and calibrate the models (requires uv)
make check     # fail if data quality has regressed
make puzzles   # search for and validate the daily queue
make serve     # run the game on http://127.0.0.1:8080
```

`make model` needs [uv](https://github.com/astral-sh/uv). Everything else is Go
with no cgo, so the server is a single static binary.

### Asking the corpus questions

```sh
go run ./cmd/pavquery dist -bowler "JJ Bumrah" -phase death -vs-hand LHB
go run ./cmd/pavquery matchup -batter "V Kohli" -bowler "JJ Bumrah"
go run ./cmd/pavquery worst -batter "N Pooran"
```

---

## Configuration

Copy `.env.example` to `.env` and fill it in. The server reads plain environment
variables and does not parse `.env` itself, so load it into your shell however
you prefer.

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `PAVILION_SECRET` | Before public deployment | a development value, with a warning | Master secret. Every day's deliveries are derived from it. |
| `PAVILION_ADDR` | No | `127.0.0.1:8080` | Listen address. |

`PAVILION_SECRET` is the one setting that matters for correctness rather than
convenience. Each day's key is derived from it, and with the key and the model a
client could compute every ball of the day before making a single decision.
Generate one with `openssl rand -hex 32`. Starting without it logs a warning and
uses a development default, which is fine on a laptop and unacceptable anywhere
else.

---

## How the game works

### The daily target is solved, not chosen

Overnight, a generator simulates thousands of full games at candidate targets
and keeps only situations where a competent player wins between 35 and 65
percent of the time **and** where the bowling choice measurably changes the
result. A target you would win nine times in ten is not a contest. The search
covers the whole tuple of target, attack, chasing side and venue, because the
attack you are dealt is part of the problem rather than set dressing.

### Entropy is pre committed

Nothing is rolled when you click. Every delivery has a fixed coordinate of day,
innings, over, ball and the decision taken for that over, and its random number
is derived from the day's key and that coordinate. There is no generator whose
position depends on how many balls have been bowled.

Two consequences follow, and both are enforced by tests. Two players who make
the same decisions meet exactly the same deliveries, byte for byte. And the luck
in the seventeenth over cannot depend on anything done in the fifth, so
"unlucky" and "wrong" stay distinguishable and a score reflects choices rather
than an accumulated dice history.

Including the decision in the coordinate is a correction to an earlier design
that drew from the coordinate alone and let the choice move only the
distribution. That reads well on paper and played badly: a wicket occupies three
to eight percent of the distribution, swapping bowlers moves that boundary by a
point or two, and a draw inside the wicket bucket therefore stayed a wicket
almost regardless of the choice. Measured across four very different bowling
policies, the first nine overs produced an identical pattern of wickets. See
[technical-architecture.md](docs/technical-architecture.md#7-determinism).

### Scoring rates decisions

Summing win probability changes across an innings telescopes to the final
result, which measures luck. Instead, before each ball, the engine evaluates
every option that was available and scores you on the gap between what you chose
and the best alternative. A careful player who loses can score positively.

---

## Architecture

Two documents describe the system in detail:

- **[System architecture](docs/system-architecture.md)**: the components, the
  request and data flows, the trust boundary, and the runtime model.
- **[Technical architecture](docs/technical-architecture.md)**: the corpus
  format, the statistical model, the simulator, determinism, and the decisions
  behind each.

The short version:

```mermaid
flowchart LR
  CS[(Cricsheet<br/>ball by ball)] --> ETL[pavetl]
  WD[(Wikidata and<br/>Wikipedia)] --> ATTR[pavattr]
  ETL --> CORPUS[(Columnar corpus)]
  ATTR --> ATTRS[(Attribute table)]
  CORPUS --> RATES[pavrates]
  CORPUS --> GRAPH[Matchup graph]
  CORPUS --> FEAT[pavfeat] --> TRAIN[Python training] --> MODELS[(Models)]
  CORPUS & ATTRS & RATES & GRAPH & MODELS --> ENGINE[Engine]
  ENGINE --> GEN[pavpuzzle] --> QUEUE[(Validated queue)]
  ENGINE & QUEUE --> SRV[pavsrv] --> WEB[Browser]
```

Three properties are load bearing and are enforced by tests:

1. **Entropy is pre committed from a coordinate**, of ball and decision, never
   drawn from a sequential stream.
2. **The server is authoritative.** The browser sends a choice and renders a
   result. It never simulates a delivery and never receives the day's key.
3. **The daily target is chosen by Monte Carlo**, offline, never at request
   time.

---

## Repository layout

```
cmd/            Command line entry points
  pavetl        Cricsheet archive to binary corpus
  pavattr       Batting hand and bowling type resolution
  pavrates      Hierarchical shrunk rate fitting
  pavfeat       Training matrix export
  pavquery      Corpus queries
  pavpuzzle     Daily instance generation and validation
  pavplay       Terminal client
  pavsrv        The game server
internal/
  corpus        Columnar delivery store and aggregation
  attr          Player attributes and normalisation
  rates         Empirical Bayes shrinkage
  graph         Batter versus bowler matchup graph
  features      Feature extraction with an expanding window
  model         Pure Go inference for the trained models
  sim           Deterministic delivery simulator
  engine        Prediction, intent, instance construction, ratings
  puzzle        Monte Carlo validation and the daily queue
  session       Signed run tokens and anti replay
  store         Results database
  api           HTTP handlers
web/            Templates and static assets
ml/             Python training, pinned with uv
data/           Inputs, generated artifacts and models
docs/           Architecture documentation
```

---

## Data sources and licensing

### Ball by ball data

All deliveries come from [Cricsheet](https://cricsheet.org), compiled by Stephen
Rushe, under the **Open Data Commons Attribution License 1.0 (ODC-BY 1.0)**:
<http://opendatacommons.org/licenses/by/1.0/>

> **This requires a human re-check before any public launch.** The ODC-BY 1.0
> statement appears on <https://cricsheet.org/register/> and only there. It is
> absent from the downloads page, from the JSON format documentation and from
> the site homepage; all three were checked. A licence stated in exactly one non
> obvious place is worth confirming with the maintainer directly.

ODC-BY is an attribution only licence rather than a share alike one. Derived
databases and models may be distributed under terms of our choosing provided
Cricsheet is attributed. There is no copyleft obligation on the corpus, the
trained models or the game.

### Player attributes

Batting handedness and bowling type come from English Wikipedia, resolved
through Wikidata property P2697, and are therefore **CC BY-SA 4.0**, which is
share alike. This differs from the Cricsheet position and matters: the attribute
table is a derived work of Wikipedia text and carries a share alike obligation,
whereas the ball by ball corpus does not. The two are kept in separate files so
the obligation stays scoped to `data/attributes/` rather than spreading to the
corpus or the models.

ESPNcricinfo is deliberately **not** used. Its `robots.txt` returns 403 to non
browser clients, `www.espn.com/robots.txt` disallows `anthropic-ai` entirely,
and the Disney terms of use prohibit accessing the site by automated means or
contributing to any collection of data or database.

---

## Attribution

Both sources are credited in the application footer and in every generated
artifact. The required text is:

> Ball by ball data from **Cricsheet** (cricsheet.org), by Stephen Rushe, used
> under ODC-BY 1.0. Player attributes from **Wikipedia**, used under CC BY-SA
> 4.0. An unofficial fan project, not affiliated with the BCCI or the IPL.

---

## Legal

An unofficial fan project, not affiliated with, endorsed by, or connected to the
BCCI, the IPL, or any franchise.

Player names are used as factual data. Team names are used nominatively, as a
statement of where a player played, and club colours appear only as a subdued
tint on a card edge or a stand. **No crests, wordmarks, kit designs or other
franchise marks are reproduced anywhere**, and no page is styled to resemble an
official club product. No player photographs are used; players are represented
by initials set in the interface typeface.

---

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make check    # data quality gate
make dev      # serve with assets read from disk, so a refresh picks up edits
```

The test suite covers the properties that are easy to break silently:

- entropy is derived from coordinates, and earlier decisions never move later luck
- a different choice genuinely produces a different delivery
- the attacking budget binds both sides equally
- a full length innings never serialises a null where an array is expected
- the bowler scheduler can always complete twenty overs
- training features never contain the ball being predicted
- Go and Python inference agree on fixed parity fixtures
- the leaderboard ranks solved runs above unsolved ones

### Requirements

- Go 1.27 or later
- [uv](https://github.com/astral-sh/uv), for model training only

The server has no cgo dependency. SQLite is `modernc.org/sqlite`, a pure Go
implementation, and model inference is a hand written tree evaluator rather than
ONNX or a C library.
