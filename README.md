# ⚽ Football Team Balancer

Maintain a roster of footballers with individual ratings, select a group of
players, and split them into two teams that are as evenly matched as possible.
Every draw is saved as a **match** you can score and rate afterwards, so the app
also keeps a per-player history.

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

Prefer a container? Jump to [Running with Docker](#running-with-docker).

Flags:

| Flag      | Default               | Purpose                              |
|-----------|-----------------------|--------------------------------------|
| `-addr`   | `:8080`               | listen address                        |
| `-data`   | `data/players.json`   | roster file (created on first run)    |
| `-matches`| `data/matches.json`   | saved fixtures + ratings (created on first run) |

```bash
go run ./cmd/server -addr 127.0.0.1:9000 -data /tmp/roster.json
```

`data/players.json` is written automatically with the 20 example players if it
does not exist, so a fresh clone works immediately. Deleting it restores the
example roster on the next start.

Requires Go 1.22 or newer (the router uses method-aware patterns); developed on
Go 1.26.

---

## Running with Docker

```bash
docker compose up -d --build      # then open http://localhost:8080
```

| Task                 | Command |
|----------------------|---------|
| Logs / stop / restart | `docker compose logs -f` · `down` · `restart` |
| Different host port   | `HOST_PORT=8085 docker compose up -d` |
| Rebuild after edits   | `docker compose up -d --build` |
| Run the suite in a container | `docker build --target test .` |
| Plain Docker          | `docker build -t football-balancer . && docker run --rm -p 8080:8080 -v balancer-data:/data football-balancer` |

The image is multi-stage: Go compiles a static binary (`CGO_ENABLED=0`), the UI
is baked into it by `web/embed.go`, and only that binary plus an Alpine base
lands in the runtime layer — no Go toolchain, no separate web server. The
container runs as a non-root user, ships a `/api/health` healthcheck, and the
Go binary is PID 1, so `docker stop` triggers the same graceful shutdown as
Ctrl-C.

**Where the data lives:** the container reads `-data /data/players.json` and
`-matches /data/matches.json`, both on the named volume
(`football-balancer-data`) that survives rebuilds and image upgrades. The roster
is seeded with the 20 example players on first start; the history starts empty.
The runtime image holds nothing but the binary and that empty `/data`, so the
repo's own `data/*.json` — used by local `go run` and by the tests — is never
baked in or shadowed.

```bash
docker volume inspect football-balancer-data            # where it is on the host
docker run --rm -v football-balancer-data:/d alpine cat /d/players.json   # read it
docker compose down                                     # keeps the volume
docker compose down -v                                  # deletes the roster — careful
```

Prefer a bind mount if you want to edit `./data/players.json` directly: replace
`players:/data` with `./data:/data` in `docker-compose.yml`, and make sure the
directory is writable by uid 1000 (`sudo chown -R 1000:1000 data`).

---

## Using the UI

The interface is **Bulgarian**; the API and its JSON error strings stay
English. In practice that is invisible during normal use — the browser validates
the selection count and every rating before calling the server — so an English
message only appears in edge cases such as saving a player another tab has just
deleted. Player names and ratings are data, not UI text, and are never
translated or rewritten.

1. **Играчи** (Players) — each row shows Име / Ат / За / ГК / Об with a checkbox.
   🧤 marks a player whose Goalkeeping is at or above the goalkeeper threshold
   (5), meaning they are goalkeeper-*capable*.
2. **Select** — `Избери всички` (all), `Изчисти избора` (clear), or tick rows by
   hand. The status line reads `12 избрани · нужни са 12 за 6 срещу 6`;
   `Създай отборите` activates only when the count is exactly `teamSize × 2`,
   and the reason is spelled out when it is not.
3. **Играчи в отбор** (players per side) — 5 срещу 5 up to 10 срещу 10.
4. **Тежест на показателите** (weights) — Атака / Защита / Голкипер / Общо on any
   scale. The line under the inputs shows them normalised to 100 % live as you
   type, and the server normalises them again independently. Four presets:
   `Балансирани`, `Само полеви`, `Акцент вратари`, `Равни`.
5. **Създай отборите** — Отбор А 🔵 and Отбор Б 🟠 appear with per-category
   totals, weighted score, goalkeeper counts, the balance percentage, and a
   category-by-category comparison table (`Показател · Отбор А · Отбор Б ·
   Разлика`).
6. **Fine-tune by hand** — `Премести в отбор Б` / `Премести в отбор А` on any
   player. Totals, weighted score, balance and objective recalculate immediately,
   and a note records what no longer matches the solver's answer (a hand
   adjustment, or weights changed after generating). `Възстанови генерираното`
   brings back the optimiser's split.
7. **Roster** — `Нов играч` / `Редактирай` / `Изтрий` (add, edit, delete).
   Ratings are validated client-side and again on the server (1 ≤ rating ≤ 10,
   decimals allowed).
8. **Мачове** — generating a draw writes a pending fixture automatically. Open
   `Резултат и оценки` on a card to set the date, both scores, a 1–10 mark per
   player and ⭐ the player of the match. Each roster row gains a `Мачове` cell
   (`appearances · awards · average`) that opens that player's history.

| On screen | Meaning | API field |
|-----------|---------|-----------|
| Атака / Защита / Голкипер / Общо | the four attributes | `attack` `defense` `goalkeeping` `overall` |
| Претеглен резултат | weighted team score | `weightedScore` |
| Баланс | balance percentage | `balance` |
| Разлика | weighted-score difference | `difference` |
| Целева стойност | objective being minimised | `objective` |
| пълен преглед / локално търсене | exhaustive / local search | `method` |
| варианта | partitions evaluated | `explored` |

---

## Project layout

```
football-balancer/
├── cmd/
│   └── server/main.go          # flags, HTTP server, graceful shutdown
├── internal/
│   ├── model/player.go         # Player, Weights, Team, BalanceResult, validation
│   ├── model/match.go          # Match, statuses, ratings, player history
│   ├── balancer/
│   │   ├── balancer.go         # objective function + exact/heuristic search
│   │   └── balancer_test.go    # 20 tests incl. brute-force cross-check
│   ├── storage/
│   │   ├── storage.go          # Repository + MatchRepository interfaces
│   │   ├── file.go             # shared atomic JSON read/write
│   │   ├── json.go             # roster store
│   │   ├── seed.go             # 20 example players
│   │   ├── matches.go          # match history store
│   │   └── json_test.go        # persistence, corruption, concurrency
│   └── api/
│       ├── handlers.go         # routes + encoding only, no business logic
│       ├── matches.go          # match endpoints, line-up resolution
│       └── handlers_test.go    # HTTP contract incl. 400/404/405 behaviour
├── web/
│   ├── index.html
│   ├── style.css
│   ├── app.js
│   ├── embed.go                # go:embed so the UI ships inside the binary
│   └── embed_test.go           # ids used by app.js exist in index.html
├── data/players.json
├── data/matches.json
├── go.mod
├── Dockerfile                    # multi-stage: build → test → 22 MB runtime
├── docker-compose.yml            # port 8080, named volume for the roster
├── .dockerignore
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
| `GET /api/matches`          | saved fixtures, newest first              |
| `POST /api/matches`         | record a drawn line-up                    |
| `GET /api/matches/{id}`     | one fixture                               |
| `PUT /api/matches/{id}`     | set the score, ratings, award, date       |
| `DELETE /api/matches/{id}`  | remove a fixture                          |
| `GET /api/players/{id}/history` | one player's appearances and aggregates |
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

## Matches, results and player history

Generating teams is not the end of a session, so every draw is also written as a
**match**. It records who was picked, how even the two sides were, the final
score, a 1–10 mark per player and one ⭐ player of the match.

### Lifecycle

| Status (API / UI)   | Meaning                                       | Requires |
|---------------------|-----------------------------------------------|----------|
| `upcoming` / `предстоящ` | drawn, nobody has played it yet           | nothing |
| `played` / `изигран`  | the result is in                              | both scores |
| `rated` / `оценен`    | the result and a mark for **every** player    | both scores + all ratings |

The status is never trusted from the client: `Match.Validate` rejects a
`rated` match that is missing ratings, and a `played` match without a score, so
the stored history cannot drift into a state the aggregates would misread. When
a request omits the status, the server derives the most advanced one the content
supports. Half a score (`team A: 2`, `team B: null`) is rejected outright — the
choice is no result or a complete one.

### What is frozen, and what may still change

A match stores copies of its players (`MatchPlayer{ID, Name}`) plus the team
totals computed at draw time. Renaming or deleting a player afterwards never
rewrites a past fixture, and deleting a player never deletes their matches.

The one part that stays editable while `upcoming` is the **line-up**: re-drawing
the pending fixture follows hand adjustments, so the stored match is the one you
actually play. As soon as a score exists the composition freezes; `clearResult`
reopens it deliberately.

A re-draw keeps the match's own weight profile and its team size — moving people
between the sides is a new draw, silently turning a 5v5 into a 3v3 is not.

### Recording rule (why you do not get a duplicate per click)

`POST /api/matches` reuses an unfinished match when it already holds exactly the
same two teams, in either order (`SameLineup` — nobody has home advantage, so
A/B swapped is the same fixture). Pressing `Създай отборите` twice on the same
selection therefore keeps one pending match; a genuinely different split gets a
new row; and once a fixture has a result, the next draw is a new match.

The browser pushes manual adjustments into the pending match 400 ms after the
last move, so dragging five players across is one write instead of five. A
one-sided move leaves 5 v 7, which is not a fixture at all — rather than quietly
keeping the older even draw on file, the results panel says exactly that.

### End-of-match ratings

`rating` is a *result*, not an input: it is stored per match and aggregated into
`GET /api/players/{id}/history`, and it never touches `attack` / `defense` /
`goalkeeping` / `overall`. The teams you balance next are built from the same
ratings you chose, not from last week's form nobody agreed to. (An explicit
"apply this rating to the player" action is easy to add later; auto-drifting the
roster is not, because it would make balancing depend on match history without
asking.)

Aggregates count **played** matches only — pending draws are excluded, so
"3 мача · 7.4" means three games actually played. `averageRating` averages the
marks that exist, `lastRatings` is the five most recent, and `wins/draws/losses`
are read from each side's point of view. Stray ratings for players who are no
longer in the line-up are ignored rather than dividing by them.

### One session end to end

```bash
# record a drawn 6 v 6 from the current roster
curl -s localhost:8080/api/players | jq '{teamSize: 6, weights: {attack:30,defense:30,goalkeeping:10,overall:30},
  teamA: [1,7,8,9,10,12], teamB: [2,3,4,5,6,11]}'   | curl -s -X POST localhost:8080/api/matches -H 'Content-Type: application/json' --data @- | jq '{id, status, balance}'
#  → { "id": 1, "status": "upcoming", "balance": 99.8 }

# the result, everyone's mark and the ⭐
curl -s -X PUT localhost:8080/api/matches/1 -H 'Content-Type: application/json' -d '{
  "goalsA": 4, "goalsB": 2, "manOfTheMatch": 7,
  "ratings": [{"playerId":1,"rating":7.5},{"playerId":7,"rating":9},{"playerId":8,"rating":6}] }' | jq .status
#  → "played"   (three of twelve rated — "rated" would be rejected)

curl -s localhost:8080/api/players/7/history | jq '{matches: .summary.matches, average: .summary.averageRating,
  record: [.summary.wins, .summary.draws, .summary.losses], awards: .summary.manOfTheMatch}'
#  → { "matches": 1, "average": 9, "record": [1, 0, 0], "awards": 1 }
```

---

## Persistence

`internal/storage` exposes one interface per entity, and it is the only thing
the rest of the program sees:

```go
type Repository interface {
	List() ([]model.Player, error)
	Get(id int) (model.Player, error)
	Add(p model.Player) (model.Player, error)
	Update(p model.Player) (model.Player, error)
	Delete(id int) error
}
```

`JSONStore` (roster) and `MatchJSONStore` (history) implement them with an
in-memory slice, a mutex, and atomic writes (temp file → fsync → rename), so a
crash can never leave a half-written data file behind. Separate interfaces and
separate files on purpose: `data/players.json` is editable current state, while
`data/matches.json` is the record of what happened, which roster edits must
never disturb.

Player IDs are a monotonic high-water mark: deleting the last player does not
free its ID for reuse, which keeps a stale browser tab from pointing at the
wrong person. Match IDs work the same way.

A PostgreSQL backend (or CSV, or an in-memory fake for tests) only has to
satisfy those five methods per entity, and `cmd/server` is where you pick which
implementation to construct. A real database would model matches and ratings as
related tables behind the same interface.

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

The match layer is covered the same way: status transitions and the rules that
keep `status` honest (a `rated` match must actually hold a mark for every
player), frozen line-ups that survive roster edits, re-draw guards, aggregate
maths (appearances, averages, form window, wins/draws/losses), plus an HTTP
lifecycle test that reopens the data files from disk.

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
| Saved line-ups / presets      | a third `Repository`, same pattern as `MatchJSONStore` |

Constraints to remember when extending: partitions travel as `uint64` bitmasks
(fine up to 63 players, so `MaxTeamSize ≤ 31`), and every attribute the balancer
should respect must be added in four places — `model.Player`, `diff`,
`Objective`, and the weights form in the UI.
