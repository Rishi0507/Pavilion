# Manhattan data quality report

Generated 2026-08-28T04:41:02Z from `data\raw\ipl_json`.
Source licence: Ball-by-ball data: Cricsheet (cricsheet.org), by Stephen Rushe, under ODC-BY 1.0 (http://opendatacommons.org/licenses/by/1.0/). Player attributes: English Wikipedia, under CC BY-SA 4.0 (https://creativecommons.org/licenses/by-sa/4.0/).

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
| player.batting_hand | 97.93% |
| player.bowling_class | 96.28% |
| player.cricinfo_id | 100.00% |

## Player attributes

Cricsheet carries neither batting handedness nor bowling type. Both are
sourced separately by `parattr` into a checked-in table with per-row
provenance. Coverage below is measured against the players the game can
actually deal, not the whole corpus.

| | |
|---|---:|
| Eligible players (>= 200 balls faced, or >= 120 bowled) | 483 |
| Eligible bowlers | 349 |
| Batting handedness known | 473 |
| Bowling class known | 336 |
| Attribute rows sourced | 473 |
| Attribute rows set by hand | 0 |
| **Dealable players** | **471** |
| Dealable as a bowler | 336 |
| Dealable as a batter | 225 |

### Excluded from the dealable pool (12)

These players clear the volume threshold but have no resolved
attributes, so the game will not deal them. A player with no
Wikipedia article is generally not one a daily game about
recognisable cricketers should be putting on screen; a high-volume
name here is worth resolving by hand in `manual.csv` instead.

| Player | Identifier | Bowled | Faced | Last season | Missing |
|---|---|---:|---:|---:|---|
| P Awana | `1a0c3177` | 747 | 14 | 2014 | bowling class |
| DS Rathi | `13fc5c6d` | 546 | 8 | 2026 | batting hand, bowling class |
| V Nigam | `5ffc0565` | 288 | 71 | 2026 | batting hand, bowling class |
| K Kartikeya | `f6d8a7ab` | 282 | 17 | 2025 | bowling class |
| Brijesh Sharma | `133bbd61` | 273 | 4 | 2026 | batting hand, bowling class |
| Shivang Kumar | `7b44eb3e` | 234 | 45 | 2026 | batting hand, bowling class |
| Yash Raj Punja | `02dfebbe` | 180 | 0 | 2026 | batting hand, bowling class |
| PP Hinge | `ece7b6b3` | 156 | 9 | 2026 | batting hand, bowling class |
| DP Vijaykumar | `acd4f5dc` | 152 | 1 | 2008 | batting hand, bowling class |
| Ashok Sharma | `50c09020` | 126 | 4 | 2026 | batting hand, bowling class |
| Naman Dhir | `fffa744b` | 52 | 432 | 2026 | batting hand |
| Priyansh Arya | `b5797845` | 0 | 435 | 2026 | batting hand |

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

## Warnings (3)

- 10 of 483 eligible players have no batting handedness
- 13 of 349 eligible bowlers have no bowling class
- 12 eligible players are excluded from the dealable pool for want of attributes

## Skipped matches (6)

- 1359519.json: 1 innings (no result)
- 1473492.json: 1 innings (no result)
- 1473495.json: 1 innings (no result)
- 1527685.json: 1 innings (no result)
- 501265.json: 1 innings (no result)
- 829763.json: 1 innings (no result)

