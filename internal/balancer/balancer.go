// Package balancer splits a group of players into two evenly matched teams.
//
// The search is exact whenever the combination space is small enough, which is
// the case for every team size the UI offers (see DefaultMaxExhaustiveCombinations).
package balancer

import (
	"fmt"
	"math"
	"sort"

	"football-balancer/internal/model"
)

// Objective coefficients. They are exported and reported by GET /api/config so
// the UI can explain and mirror exactly what the server optimises.
//
// The first term is the difference between the two weighted team scores. The
// remaining terms keep individual categories from cancelling each other out:
// a team that is +5 in attack and -5 in defense would otherwise look perfect.
const (
	WeightedScoreCoefficient   = 1.00
	AttackDiffCoefficient      = 0.12
	DefenseDiffCoefficient     = 0.12
	GoalkeepingDiffCoefficient = 0.08
	OverallDiffCoefficient     = 0.15

	// The goalkeeper terms act independently of the weighted score: they push
	// goalkeeper-capable players into different teams instead of letting one
	// side hoard them.
	GoalkeeperCountCoefficient = 0.50 // per unit of keeper-capable head-count difference
	GoalkeeperGapCoefficient   = 0.20 // per point between each side's best keeper
)

// Supported team sizes. The UI offers 5 vs 5 through 10 vs 10; the lower bound
// is relaxed so the balancer stays reusable (and testable) for smaller formats.
const (
	MinTeamSize = 2
	MaxTeamSize = 10
)

// DefaultMaxExhaustiveCombinations caps the exact search. Thanks to the A/B
// symmetry pruning the real work is C(n-1, k-1): 92378 iterations for 10 vs 10,
// far below this ceiling, so every supported team size is solved exactly.
// Larger pools fall back to the local search.
//
// Partitions are carried as uint64 bitmasks: MaxTeamSize keeps the pool at
// 2*MaxTeamSize = 20 players, so a uint64 is comfortably wide enough. Raising
// MaxTeamSize past 31 means widening the mask representation.
const DefaultMaxExhaustiveCombinations = 1_000_000

// Search methods reported in BalanceResult.Method.
const (
	MethodExhaustive  = "exhaustive"
	MethodLocalSearch = "local-search"
)

// Options fully describes one balancing run.
type Options struct {
	TeamSize                  int
	Weights                   model.Weights
	MaxExhaustiveCombinations int // 0 means DefaultMaxExhaustiveCombinations
}

// entry is a player pre-digested for the inner search loop.
type entry struct {
	player   model.Player
	weighted float64
}

// sums holds the category totals of one side.
type sums struct {
	attack      float64
	defense     float64
	goalkeeping float64
	overall     float64
	weighted    float64
	keepers     int
	bestKeeper  float64
}

// candidate is one complete partition of the selected players.
type candidate struct {
	mask      uint64
	objective float64
	diff      diff
}

// diff keeps the absolute difference of every category.
type diff struct {
	attack      float64
	defense     float64
	goalkeeping float64
	overall     float64
	weighted    float64
	keepers     int
	keeperGap   float64
}

// Split balances players into two teams of teamSize, using weights.
func Split(players []model.Player, teamSize int, weights model.Weights) (*model.BalanceResult, error) {
	return SplitWith(players, Options{TeamSize: teamSize, Weights: weights})
}

// SplitWith is Split with full control over the search.
func SplitWith(players []model.Player, opts Options) (*model.BalanceResult, error) {
	// A negative weight would invert the objective rather than de-emphasise an
	// attribute, so it is rejected here too. An all-zero profile is tolerated
	// and read as "every attribute matters equally".
	if err := opts.Weights.ValidateNonNegative(); err != nil {
		return nil, err
	}
	nw := opts.Weights.Normalized()
	entries, err := prepare(players, opts.TeamSize, nw)
	if err != nil {
		return nil, err
	}

	limit := opts.MaxExhaustiveCombinations
	if limit <= 0 {
		limit = DefaultMaxExhaustiveCombinations
	}

	var (
		best     candidate
		explored int64
		method   = MethodExhaustive
	)
	if combinations(len(entries)-1, opts.TeamSize-1) <= int64(limit) {
		best, explored = searchExhaustive(entries, opts.TeamSize)
	} else {
		best, explored = searchLocalSearch(entries, opts.TeamSize)
		method = MethodLocalSearch
	}

	teamA, teamB := splitByMask(entries, best.mask)
	res := &model.BalanceResult{
		TeamA:      model.NewTeam(teamA, nw),
		TeamB:      model.NewTeam(teamB, nw),
		Difference: best.diff.weighted,
		Objective:  best.objective,
		TeamSize:   opts.TeamSize,
		Method:     method,
		Explored:   explored,
		Weights:    opts.Weights.AsPercent(),
	}
	res.Balance = model.BalancePercent(res.TeamA.WeightedScore, res.TeamB.WeightedScore)
	roundResult(res)
	return res, nil
}

// roundResult trims the exported numbers to a sensible precision: the search
// itself keeps full float precision, the client only ever sees one or two
// decimals.
func roundResult(r *model.BalanceResult) {
	for _, t := range []*model.Team{&r.TeamA, &r.TeamB} {
		t.Attack = model.RoundTo(t.Attack, 1)
		t.Defense = model.RoundTo(t.Defense, 1)
		t.Goalkeeping = model.RoundTo(t.Goalkeeping, 1)
		t.Overall = model.RoundTo(t.Overall, 1)
		t.WeightedScore = model.RoundTo(t.WeightedScore, 2)
		t.BestGoalkeeping = model.RoundTo(t.BestGoalkeeping, 1)
	}
	r.Balance = model.RoundTo(r.Balance, 1)
	r.Difference = model.RoundTo(r.Difference, 2)
	r.Objective = model.RoundTo(r.Objective, 4)
}

// prepare validates the input and pre-computes the weighted contribution of
// every player, so the search loops only ever add floats.
func prepare(players []model.Player, teamSize int, nw model.Weights) ([]entry, error) {
	if teamSize < MinTeamSize || teamSize > MaxTeamSize {
		return nil, fmt.Errorf("team size must be between %d and %d, got %d", MinTeamSize, MaxTeamSize, teamSize)
	}
	if len(players) != teamSize*2 {
		return nil, fmt.Errorf("expected exactly %d players for %d vs %d, got %d",
			teamSize*2, teamSize, teamSize, len(players))
	}

	seen := make(map[int]bool, len(players))
	entries := make([]entry, 0, len(players))
	for i, p := range players {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("player %d: %w", i+1, err)
		}
		id := p.ID
		if id <= 0 {
			id = -1 - i // ad-hoc entries without a real ID stay distinguishable
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate player %q (id %d)", p.Name, p.ID)
		}
		seen[id] = true
		entries = append(entries, entry{player: p, weighted: nw.Score(p)})
	}
	return entries, nil
}

// evaluate scores one partition of the pool: mask bits select Team A.
func evaluate(entries []entry, mask uint64) candidate {
	var a, b sums
	for i, e := range entries {
		team := &b
		if mask&(1<<uint(i)) != 0 {
			team = &a
		}
		team.attack += e.player.Attack
		team.defense += e.player.Defense
		team.goalkeeping += e.player.Goalkeeping
		team.overall += e.player.Overall
		team.weighted += e.weighted
		if e.player.IsGoalkeeper() {
			team.keepers++
		}
		if e.player.Goalkeeping > team.bestKeeper {
			team.bestKeeper = e.player.Goalkeeping
		}
	}
	d := diff{
		attack:      math.Abs(a.attack - b.attack),
		defense:     math.Abs(a.defense - b.defense),
		goalkeeping: math.Abs(a.goalkeeping - b.goalkeeping),
		overall:     math.Abs(a.overall - b.overall),
		weighted:    math.Abs(a.weighted - b.weighted),
		keepers:     int(math.Abs(float64(a.keepers - b.keepers))),
		keeperGap:   math.Abs(a.bestKeeper - b.bestKeeper),
	}
	return candidate{mask: mask, objective: Objective(d), diff: d}
}

// Objective turns the category differences of a partition into a single number
// to minimise. Lower is better.
func Objective(d diff) float64 {
	return WeightedScoreCoefficient*d.weighted +
		AttackDiffCoefficient*d.attack +
		DefenseDiffCoefficient*d.defense +
		GoalkeepingDiffCoefficient*d.goalkeeping +
		OverallDiffCoefficient*d.overall +
		GoalkeeperCountCoefficient*float64(d.keepers) +
		GoalkeeperGapCoefficient*d.keeperGap
}

// searchExhaustive evaluates every distinct partition and returns the best one.
//
// Team A and Team B are interchangeable, so player 0 is pinned to Team A and
// only C(n-1, k-1) combinations are visited: each unordered split is seen once.
func searchExhaustive(entries []entry, teamSize int) (candidate, int64) {
	n := len(entries)
	pick := teamSize - 1 // companions of player 0, chosen from indices 1..n-1

	idx := make([]int, pick)
	for i := range idx {
		idx[i] = i + 1
	}

	best := candidate{objective: math.Inf(1)}
	var explored int64
	for {
		mask := uint64(1)
		for _, i := range idx {
			mask |= 1 << uint(i)
		}
		if c := evaluate(entries, mask); c.objective < best.objective {
			best = c
		}
		explored++

		// Next combination in lexicographic order.
		pos := pick - 1
		for pos >= 0 && idx[pos] == n-pick+pos {
			pos--
		}
		if pos < 0 {
			break
		}
		idx[pos]++
		for j := pos + 1; j < pick; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
	return best, explored
}

// searchLocalSearch handles combination spaces too large to enumerate: a
// weighted greedy seed followed by hill climbing over A<->B swaps. It is
// deterministic, so the same pool always produces the same teams.
func searchLocalSearch(entries []entry, teamSize int) (candidate, int64) {
	n := len(entries)

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := entries[order[i]], entries[order[j]]
		if a.weighted != b.weighted {
			return a.weighted > b.weighted
		}
		return a.player.ID < b.player.ID
	})

	// Greedy seed: strongest weighted player first, into the side that is
	// currently weaker, unless that side is already full.
	mask := uint64(0)
	countA, countB := 0, 0
	var sumA, sumB float64
	for _, i := range order {
		toA := sumA <= sumB
		if countA >= teamSize {
			toA = false
		} else if countB >= teamSize {
			toA = true
		}
		if toA {
			mask |= 1 << uint(i)
			countA++
			sumA += entries[i].weighted
		} else {
			countB++
			sumB += entries[i].weighted
		}
	}

	best := evaluate(entries, mask)
	explored := int64(1)
	for {
		improved := false
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			for j := 0; j < n; j++ {
				if mask&(1<<uint(j)) != 0 {
					continue
				}
				next := mask ^ (1 << uint(i)) ^ (1 << uint(j))
				c := evaluate(entries, next)
				explored++
				if c.objective < best.objective-1e-12 {
					best, mask, improved = c, next, true
				}
			}
		}
		if !improved {
			break
		}
	}
	return best, explored
}

// splitByMask turns a bitmask back into two player slices.
func splitByMask(entries []entry, mask uint64) ([]model.Player, []model.Player) {
	a := make([]model.Player, 0, len(entries)/2)
	b := make([]model.Player, 0, len(entries)/2)
	for i, e := range entries {
		if mask&(1<<uint(i)) != 0 {
			a = append(a, e.player)
		} else {
			b = append(b, e.player)
		}
	}
	return a, b
}

// combinations returns C(n, k), saturating at math.MaxInt64 instead of
// overflowing so that very large pools still classify as "not exhaustive".
func combinations(n, k int) int64 {
	if n < 0 || k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	result := int64(1)
	for i := 1; i <= k; i++ {
		factor := int64(n - k + i)
		if result > math.MaxInt64/factor {
			return math.MaxInt64
		}
		result = result * factor / int64(i)
	}
	return result
}
