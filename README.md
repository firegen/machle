# ⚽ Football Team Balancer

Maintain a roster of footballers with individual ratings, select a group of
players, and split them into two teams that are as evenly matched as possible.

The balancing is **exact** for every supported team size: all distinct
partitions are evaluated and the one with the lowest imbalance is returned.
No random restarts, no "good enough" guesses, no flickering results — the same
input always produces the same teams.

* **Backend:** Go (standard library only)
* **Frontend:** HTML + CSS + vanilla JavaScript, no framework, no build step
* **Transport:** JSON REST API
* **Runtime:** a single Go process serving both API and UI
* **Persistence:** `data/players.json` (no database required)

---

## Quick start

```bash
go run ./cmd/server
```

Then open **http://localhost:8080**

Flags:

| Flag      | Default               | Purpose                              |
|-----------|-----------------------|--------------------------------------|
| `-addr`   | `:8080`               | listen address                        |
| `-data`   | `data/players.json`   | roster file (created on first run)    |

```bash
go run ./cmd/server -addr 127.0.0.1:9000 -data /tmp/roster.json
```

`data/players.json` is written automatically with the 20 example players if it
does not exist, so a fresh clone works immediately. Deleting it restores the
example roster on the next start.

Requires Go 1.22 or newer (the router uses method-aware patterns); developed on
Go 1.26.

---

## Using the UI

1. **Players** — every player is a row with Attack / Defense / Goalkeeping /
   Overall and a checkbox. 🧤 marks a player whose Goalkeeping is at or above
   the goalkeeper threshold (5), meaning they are goalkeeper-*capable*.
2. **Select players** — `Select all`, `Clear selection`, or tick rows by hand.
   The status line shows `N selected · M needed for 6 vs 6`; `Generate teams`
   activates only when the count is exactly `teamSize × 2`, and the reason is
   spelled out when it is not.
3. **Team size** — 5v5, 6v6, 7v7, 8v8, 9v9 or 10v10.
4. **Weights** — Attack / Defense / Goalkeeping / Overall on any scale. The UI
   shows the values normalised to 100 % live as you type, and the server
   normalises them again independently. Four presets cover common setups.
5. **Generate teams** — Team A 🔵 and Team B 🟠 appear with per-category
   totals, weighted score, goalkeeper counts, the balance percentage, and a
   category-by-category comparison table.
6. **Fine-tune by hand** — `Move to Team B` / `Move to Team A` on any player.
   Totals, weighted score, balance and objective recalculate immediately in the
   browser, and a note records what no longer matches the solver's answer (a
   hand adjustment, or weights changed after generating). `Reset to generated`
   brings back the optimiser's split.
7. **Manage the roster** — add, edit and delete players; ratings are validated
   client-side and again on the server (1 ≤ rating ≤ 10, decimals allowed).

---

## Project layout

```
football-balancer/
├── cmd/
│   └── server/main.go          # flags, HTTP server, graceful shutdown
├── internal/
│   ├── model/player.go         # Player, Weights, Team, BalanceResult, validation
│   ├── balancer/
│   │   ├── balancer.go         # objective function + exact/heuristic search
│   │   └── balancer_test.go    # 20 tests incl. brute-force cross-check
│   ├── storage/
│   │   ├── storage.go          # Repository interface (swap for Postgres here)
│   │   ├── json.go             # atomic JSON file store
│   │   ├── seed.go             # 20 example players
│   │   └── json_test.go        # persistence, corruption, concurrency
│   └── api/
│       ├── handlers.go         # routes + encoding only, no business logic
│       └── handlers_test.go    # HTTP contract incl. 400/404/405 behaviour
├── web/
│   ├── index.html
│   ├── style.css
│   ├── app.js
│   ├── embed.go                # go:embed so the UI ships inside the binary
│   └── embed_test.go           # ids used by app.js exist in index.html
├── data/players.json
├── go.mod
└── README.md
```

Dependency direction is one-way: `api → balancer/storage → model`. Handlers
translate HTTP to and from domain calls, the balancer knows nothing about
storage, and storage knows nothing about balancing.

Two additions beyond the requested layout, both for packaging: `web/embed.go`
(embedding a directory is only possible from a package inside it, and the spec
keeps `main.go` under `cmd/`), and the small `storage/storage.go` /
`storage/seed.go` split so the JSON implementation sits behind an interface.

---

## REST API

| Method & path               | Purpose                                  |
|-----------------------------|------------------------------------------|
| `GET /api/players`          | full roster                              |
| `POST /api/players`         | add a player (server assigns the ID)      |
| `PUT /api/players/{id}`     | edit a player                            |
| `DELETE /api/players/{id}`  | remove a player                          |
| `POST /api/balance`         | split the selected players into two teams |
| `GET /api/config`           | defaults, limits, objective coefficients  |
| `GET /api/health`           | liveness                                 |

### `POST /api/balance`

```json
{
  "teamSize": 6,
  "players": [
    { "id": 1, "name": "Ivan", "attack": 9, "defense": 6, "goalkeeping": 1, "overall": 8 }
  ],
  "weights": { "attack": 30, "defense": 30, "goalkeeping": 10, "overall": 30 }
}
```

`players` must contain exactly `teamSize × 2` entries. `weights` is optional
(defaults to 30/30/10/30) and may be expressed on any scale.

```json
{
  "teamA": {
    "players": [ { "id": 1, "name": "Ivan", "attack": 9, "defense": 6, "goalkeeping": 1, "overall": 8 }, "…" ],
    "attack": 42, "defense": 43, "goalkeeping": 12, "overall": 44.8,
    "weightedScore": 40.14, "goalkeepers": 1, "bestGoalkeeping": 7
  },
  "teamB": { "…": "same shape" },
  "balance": 99.8,
  "difference": 0.08,
  "objective": 0.45,
  "teamSize": 6,
  "method": "exhaustive",
  "explored": 462,
  "weights": { "attack": 30, "defense": 30, "goalkeeping": 10, "overall": 30 }
}
```

Errors are JSON: `{"error":"expected exactly 12 players for 6 vs 6, got 11"}`
with status 400; unknown IDs give 404, wrong verbs give 405 with an `Allow`
header, unknown `/api/` paths give 404 — never the frontend.

```bash
curl -s localhost:8080/api/players | jq '{teamSize: 6, players: .[:12], weights: {attack:30,defense:30,goalkeeping:10,overall:30}}' \
  | curl -s -X POST localhost:8080/api/balance -H 'Content-Type: application/json' --data @- | jq '.balance, .difference'
```

---

## How the balancing works

### 1. Weighted player score

Weights are normalised to sum to 1 (`30/30/10/30` → `0.3/0.3/0.1/0.3`), then
each player contributes

```
score(player) = attack·w_attack + defense·w_defense + goalkeeping·w_gk + overall·w_overall
```

A team's weighted score is the sum of its players' scores, so it stays on the
1–10 rating scale (a team of six ≈ 6 × 7.5 ≈ 45). If every weight is zero the
profile degrades to four equally important attributes instead of dividing by
zero — the API rejects that input up front with a clearer message, the balancer
itself stays tolerant.

### 2. Objective

Minimising only the weighted-score difference would let two teams cancel out
across categories: +5 attack and −5 defense nets to zero. So the individual
gaps are penalised as well, and goalkeeping gets its own terms:

```
objective = 1.00 · |Δ weighted score|
          + 0.12 · |Δ attack total|
          + 0.12 · |Δ defense total|
          + 0.08 · |Δ goalkeeping total|
          + 0.15 · |Δ overall total|
          + 0.50 · |Δ goalkeeper-capable head count|
          + 0.20 · |Δ best goalkeeping rating|
```

Every coefficient is a named constant in `internal/balancer/balancer.go` and
the values are published by `GET /api/config`, so the frontend mirrors the
server rather than hard-coding them. Lower is better; a perfect split scores 0.

The last two terms are what implement "goalkeepers are special":

* `GoalkeeperCountCoefficient` pushes goalkeeper-capable players apart, so
  GK 8 + GK 8 beats GK 16 + GK 0 even when the overall points are identical.
* `GoalkeeperGapCoefficient` keeps the *best* keeper on each side comparable.

Threshold 5 is treated as an indicator, never a rule: nobody is forced into
goal, and a strong outfielder with a mediocre Goalkeeping rating is balanced
like any other player.

### 3. Search

For `n` players in teams of `k = n/2` there are `C(n,k)` choices for Team A,
but half of them are the same fixture seen from the other dugout
(`A = X, B = Y` ≡ `A = Y, B = X`). Pinning player 0 to Team A removes the
mirrors, leaving `C(n−1, k−1)` candidates:

| n  | k   | evaluated splits |
|----|-----|------------------|
| 10 | 5   | 126              |
| 12 | 6   | 462              |
| 14 | 7   | 1 716            |
| 16 | 8   | 6 435            |
| 18 | 9   | 24 310           |
| 20 | 10  | 92 378           |

All of them are evaluated — this is an exhaustive optimum, not a search that
hopes to find one. Each candidate is scored in one pass over the pool
(`O(n)` float additions), and the lowest objective wins. Ties resolve to the
first candidate found, and combination order is fixed, so results are
deterministic.

When `C(n−1, k−1)` exceeds `DefaultMaxExhaustiveCombinations` (1,000,000 —
unreachable with today's team sizes, meant for a future 12v12 or 3+ team
mode), the balancer switches to a deterministic greedy seed plus hill-climbing
over swaps and reports `"method":"local-search"`. It always returns a valid
partition; it just stops promising global optimality.

Measured on this machine: **10 v 10 ≈ 15 ms**, 5 v 5 ≈ 0.015 ms
(`go test -bench . ./internal/balancer/`).

### 4. Balance percentage

```
balance = max(0, 100 · (1 − |A − B| / ((A + B) / 2)))
```

i.e. the weighted-score gap relative to the average team strength. Numbers are
rounded where they are produced — one decimal for category totals, two for
scores, one for the balance — so the UI shows `99.8%`, never `99.834729182%`.

### 5. Worked example

The example roster, first 12 players, default weights:

```
Team A 🔵 Ivan, Martin, Alex, Viktor, Kaloyan, Boris
Team B 🟠 Peter, Georgi, Dimitar, Nikolay, Stefan, Teodor

Attack       42.0  vs  42.0     Goalkeeping    12.0  vs  13.0
Defense      43.0  vs  43.0     Overall        44.8  vs  44.2
weighted     40.14 vs  40.06    keepers 🧤      1    vs   1

balance 99.8%   difference 0.08   objective 0.45   462 splits evaluated
```

---

## Persistence

`internal/storage.Repository` is the only thing the rest of the program sees:

```go
type Repository interface {
	List() ([]model.Player, error)
	Get(id int) (model.Player, error)
	Add(p model.Player) (model.Player, error)
	Update(p model.Player) (model.Player, error)
	Delete(id int) error
}
```

`JSONStore` implements it with an in-memory slice, a mutex, and atomic writes
(temp file → fsync → rename), so a crash can never leave a half-written roster.
Player IDs are a monotonic high-water mark: deleting the last player does not
free its ID for reuse, which keeps a stale browser tab from pointing at the
wrong person. A `POSTGRESStore` (or CSV, or an in-memory fake for tests) only
has to satisfy the same five methods, and `cmd/server` is where you would pick
which one to construct.

## Tests

```bash
go test ./...            # unit + HTTP contract tests
go test -race ./...
go vet ./...
go build ./...
go test -bench . ./internal/balancer/
```

Coverage of the balancing algorithm specifically: 5v5 → 10v10, invalid player
counts and team sizes, all-zero weights, unequal weights, single-attribute
weights, strong goalkeeper imbalance, identical players, decimal ratings, the
`teamA ∩ teamB = ∅` / `teamA ∪ teamB = pool` / `len = teamSize` invariants,
that no mirror split is evaluated twice, determinism, and a cross-check of the
chosen split against an independently written brute-force enumeration.

## Example roster

`data/players.json` ships with:

| Player    | Attack | Defense | Goalkeeping | Overall |
|-----------|--------|---------|-------------|---------|
| Ivan      | 9      | 6       | 1           | 8.0     |
| Peter     | 7      | 8       | 1           | 7.5     |
| Georgi    | 5      | 9       | 1           | 7.0     |
| Dimitar   | 8      | 5       | 8 🧤        | 7.5     |
| Nikolay   | 6      | 7       | 1           | 6.5     |
| Stefan    | 9      | 7       | 1           | 8.2     |
| Martin    | 8      | 6       | 1           | 7.6     |
| Alex      | 6      | 9       | 1           | 7.4     |
| Viktor    | 10     | 5       | 1           | 8.0     |
| Kaloyan   | 5      | 8       | 1           | 6.8     |
| Teodor    | 7      | 7       | 1           | 7.5     |
| Boris     | 4      | 9       | 7 🧤        | 7.0     |
| Anton     | 8      | 7       | 1           | 7.8     |
| Daniel    | 6      | 6       | 1           | 6.6     |
| Mihail    | 7      | 5       | 1           | 6.9     |
| Radoslav  | 5      | 8       | 8 🧤        | 7.2     |
| Hristo    | 9      | 4       | 1           | 7.3     |
| Vasil     | 6      | 8       | 1           | 7.3     |
| Kristian  | 8      | 8       | 1           | 8.0     |
| Todor     | 3      | 9       | 9 🧤        | 7.0     |

## Later, and where it plugs in

Deliberately not implemented, but already accommodated:

| Feature                    | Where it goes |
|----------------------------|---------------|
| CSV / Excel import-export  | new methods next to `Repository`, or an `internal/importer` package + a handler |
| PostgreSQL                 | second `Repository` implementation, selected in `cmd/server` |
| Positions / preferred position | extra fields on `model.Player`; the objective takes more `Δ` terms |
| Availability, fatigue      | per-player multipliers applied in `prepare()` |
| Player chemistry           | needs pair terms, so `evaluate()` (it already walks the whole pool) |
| 3+ teams, tournament mode  | masks become team-index arrays; raise `DefaultMaxExhaustiveCombinations` and lean on `searchLocalSearch` |
| Goalkeeper-specific rules  | `model.GoalkeeperThreshold` + the two keeper coefficients |
| Saved line-ups, match history | another `Repository` for a different entity |

Constraints to remember when extending: partitions travel as `uint64` bitmasks
(fine up to 63 players, so `MaxTeamSize ≤ 31`), and every attribute the balancer
should respect must be added in four places — `model.Player`, `diff`,
`Objective`, and the weights form in the UI.
