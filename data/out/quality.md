# Manhattan data quality report

Generated 2026-08-27T14:01:21Z from `data\raw\ipl_json`.
Source licence: Open Data Commons Attribution License 1.0 (http://opendatacommons.org/licenses/by/1.0/), Cricsheet, by Stephen Rushe

## Corpus

| | |
|---|---:|
| Match files | 1243 |
| Matches included | 1237 |
| Matches skipped | 6 |
| Deliveries | 295215 |
| Legal deliveries | 284130 |
| Wickets | 14678 |
| Players | 964 |
| Venues | 60 |
| Teams | 19 |

## Attribute coverage

| Attribute | Coverage |
|---|---:|
| delivery.batter_resolved | 100.00% |
| delivery.bowler_resolved | 100.00% |
| match.city | 95.88% |
| match.result_winner | 98.46% |
| match.toss_winner | 100.00% |
| match.venue | 100.00% |
| player.batting_hand | 0.00% |
| player.bowling_type | 0.00% |
| player.cricinfo_id | 100.00% |

## Entity resolution

Cricsheet embeds a per-match `registry.people` map from display name to a
stable person identifier, so name drift across seasons is resolved
upstream. The corpus keys every player by that identifier, never by name.

- People register rows: 18468
- Players in the corpus: 964
- Cricinfo id coverage: 100.00%
- Names in deliveries absent from their match registry: 0
- Corpus players absent from the people register: 0

### Ambiguous display names (1)

One name, more than one cricketer. Keying rates by name would merge them.

| Name | Identifiers |
|---|---|
| Harmeet Singh | `0bf15e52`, `2a72fd4f` |

### Identifiers with multiple display names (3)

| Identifier | Names |
|---|---|
| `12314277` | Arshad Khan, Arshad Khan (2) |
| `21d4e29b` | NA Saini, Navdeep Saini |
| `d7423da1` | S Arora, Salil Arora |

## Integrity

| Check | Count |
|---|---:|
| Legal-ball index disagrees with `actual_delivery` | 0 |
| Overs miscounted by the umpire | 8 |
| Deliveries with more than one wicket | 0 |
| `runs.total` disagrees with its components | 0 |
| Super-over innings (excluded from modelling) | 34 |
| Abandoned matches (no result) | 6 |
| Innings carrying a target | 1237 |

Miscounted overs (an umpire signalled five or seven balls; the
`actual_delivery` cross-check is legitimately expected to fail here):

- 1136564.json innings 1 over 11: 7 legal balls
- 335987.json innings 0 over 7: 5 legal balls
- 335994.json innings 1 over 10: 7 legal balls
- 336015.json innings 1 over 14: 5 legal balls
- 392198.json innings 1 over 10: 7 legal balls
- 419155.json innings 0 over 18: 7 legal balls
- 501202.json innings 0 over 5: 5 legal balls
- 501255.json innings 1 over 9: 5 legal balls

## Warnings (1)

- batting handedness and bowling type are absent from Cricsheet and are not yet sourced; the matchup model cannot be trained until they are

## Skipped matches (6)

- 1359519.json: 1 innings (no result)
- 1473492.json: 1 innings (no result)
- 1473495.json: 1 innings (no result)
- 1527685.json: 1 innings (no result)
- 501265.json: 1 innings (no result)
- 829763.json: 1 innings (no result)

