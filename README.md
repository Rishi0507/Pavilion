# Manhattan

A daily T20 cricket puzzle. One target score per day, the same for every player
in the world. You play both innings of that target: first you defend it with
five bowlers and a four-over limit each, then you chase it with a fixed budget
of high-intent overs.

The game ships as **Par**. `Manhattan` is the project and repository name, after
the per-over bar chart that gives the game its visual language.

**Status: milestone 2 of 11.** Not yet playable.

---

## Quick start

```sh
make data    # download the Cricsheet IPL archive and people register
make etl     # build the binary corpus and the data quality report
make attrs   # resolve batting handedness and bowling type
make check   # fail if data quality has regressed
make test    # run the test suite
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
2. Corpus and CSR matchup graph, sub-10ms aggregation
   - player attributes sourced, quality gate wired ✅
   - CSR matchup graph and the query CLI — in progress
3. Hierarchical shrinkage on player rates
4. Ball outcome model, calibrated
5. The simulator: deterministic, pure, tested
6. **Defend half, playable and ugly** — the gate: if choosing the 17th over is
   not fun in isolation, nothing later fixes it
7. Win probability model and the share grid
8. Chase half
9. Daily pipeline
10. Design pass
11. Leaderboards, stats, sharing, streaks

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
