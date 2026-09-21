package balancer

import (
	"fmt"
	"math"
	"testing"

	"football-balancer/internal/model"
)

/* ============================== test helpers ============================== */

func p(id int, name string, attack, defense, goalkeeping, overall float64) model.Player {
	return model.Player{ID: id, Name: name, Attack: attack, Defense: defense, Goalkeeping: goalkeeping, Overall: overall}
}

// lineup builds n players with deterministically varied ratings: mixed profiles,
// no two identical, every rating inside the legal 1..10 band.
func lineup(n int) []model.Player {
	out := make([]model.Player, 0, n)
	for i := 1; i <= n; i++ {
		attack := 4 + float64((i*3)%6)       // 4..9
		defense := 4 + float64((i*5)%6)      // 4..9
		goalkeeping := 1 + float64((i*7)%4)  // 1..4
		overall := 6 + float64((i*11)%30)/10 // 6.0..8.9
		out = append(out, p(i, fmt.Sprintf("P%02d", i), attack, defense, goalkeeping, overall))
	}
	return out
}

// agg mirrors the package-internal accumulator, used by the independent brute
// force below.
type agg struct {
	attack, defense, goalkeeping, overall, weighted float64
	keepers                                         int
	bestKeeper                                      float64
}

func (a *agg) add(pl model.Player, nw model.Weights) {
	a.attack += pl.Attack
	a.defense += pl.Defense
	a.goalkeeping += pl.Goalkeeping
	a.overall += pl.Overall
	a.weighted += nw.Score(pl)
	if pl.IsGoalkeeper() {
		a.keepers++
	}
	if pl.Goalkeeping > a.bestKeeper {
		a.bestKeeper = pl.Goalkeeping
	}
}

// assertPartition checks the invariants every balancing run must satisfy:
// len(teamA) == teamSize, len(teamB) == teamSize, teamA ∩ teamB = ∅ and
// teamA ∪ teamB == the selected pool, with aggregates matching the roster.
func assertPartition(t *testing.T, res *model.BalanceResult, in []model.Player, teamSize int) {
	t.Helper()

	if len(res.TeamA.Players) != teamSize {
		t.Fatalf("len(teamA) = %d, want %d", len(res.TeamA.Players), teamSize)
	}
	if len(res.TeamB.Players) != teamSize {
		t.Fatalf("len(teamB) = %d, want %d", len(res.TeamB.Players), teamSize)
	}

	counts := make(map[int]int, len(in))
	for _, pl := range append(append([]model.Player{}, res.TeamA.Players...), res.TeamB.Players...) {
		counts[pl.ID]++
	}
	for _, pl := range in {
		if counts[pl.ID] != 1 {
			t.Errorf("player %d (%s) appears %d times: the two teams must partition the pool exactly",
				pl.ID, pl.Name, counts[pl.ID])
		}
	}
	if len(counts) != len(in) {
		t.Errorf("output holds %d distinct players, input had %d", len(counts), len(in))
	}

	nw := res.Weights.Normalized()
	for _, tc := range []struct {
		label string
		team  model.Team
	}{{"teamA", res.TeamA}, {"teamB", res.TeamB}} {
		var want agg
		for _, pl := range tc.team.Players {
			want.add(pl, nw)
		}
		near(t, tc.label+".attack", tc.team.Attack, want.attack, 0.06)
		near(t, tc.label+".defense", tc.team.Defense, want.defense, 0.06)
		near(t, tc.label+".goalkeeping", tc.team.Goalkeeping, want.goalkeeping, 0.06)
		near(t, tc.label+".overall", tc.team.Overall, want.overall, 0.06)
		near(t, tc.label+".weightedScore", tc.team.WeightedScore, want.weighted, 0.01)
		if tc.team.Goalkeepers != want.keepers {
			t.Errorf("%s.goalkeepers = %d, want %d", tc.label, tc.team.Goalkeepers, want.keepers)
		}
	}
}

func near(t *testing.T, label string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %v, want %v (±%v)", label, got, want, tolerance)
	}
}

/* ============================== happy paths =============================== */

func TestSplitStandardTeamSizes(t *testing.T) {
	for _, tc := range []struct{ players, teamSize int }{
		{10, 5}, {12, 6}, {14, 7}, {16, 8}, {18, 9}, {20, 10},
	} {
		t.Run(fmt.Sprintf("%dv%d", tc.teamSize, tc.teamSize), func(t *testing.T) {
			in := lineup(tc.players)
			res, err := Split(in, tc.teamSize, model.DefaultWeights())
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			assertPartition(t, res, in, tc.teamSize)

			if res.Method != MethodExhaustive {
				t.Errorf("method = %q, want %q for %d players", res.Method, MethodExhaustive, tc.players)
			}
			if want := combinations(tc.players-1, tc.teamSize-1); res.Explored != want {
				t.Errorf("explored = %d, want C(%d,%d) = %d", res.Explored, tc.players-1, tc.teamSize-1, want)
			}
			if res.Balance <= 90 {
				t.Errorf("balance = %.1f%%, the exhaustive search should easily beat 90%%", res.Balance)
			}
			if res.TeamSize != tc.teamSize {
				t.Errorf("teamSize = %d, want %d", res.TeamSize, tc.teamSize)
			}
		})
	}
}

// TestMatchesBruteForce re-derives the optimum with an independent enumeration
// to prove the search returns the global minimum of the objective.
func TestMatchesBruteForce(t *testing.T) {
	in := lineup(12)
	nw := model.DefaultWeights().Normalized()

	best := math.Inf(1)
	var walk func(pos, chosen int, mask uint64)
	walk = func(pos, chosen int, mask uint64) {
		if chosen == 6 {
			if v := bruteObjective(in, mask, nw); v < best {
				best = v
			}
			return
		}
		for i := pos; i < len(in); i++ {
			walk(i+1, chosen+1, mask|1<<uint(i))
		}
	}
	walk(0, 0, 0)

	res, err := Split(in, 6, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if math.Abs(res.Objective-best) > 1e-6 {
		t.Errorf("objective = %v, brute force minimum = %v", res.Objective, best)
	}
}

func bruteObjective(in []model.Player, mask uint64, nw model.Weights) float64 {
	var a, b agg
	for i, pl := range in {
		team := &b
		if mask&(1<<uint(i)) != 0 {
			team = &a
		}
		team.add(pl, nw)
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
	return WeightedScoreCoefficient*d.weighted +
		AttackDiffCoefficient*d.attack +
		DefenseDiffCoefficient*d.defense +
		GoalkeepingDiffCoefficient*d.goalkeeping +
		OverallDiffCoefficient*d.overall +
		GoalkeeperCountCoefficient*float64(d.keepers) +
		GoalkeeperGapCoefficient*d.keeperGap
}

func TestSearchIsDeterministic(t *testing.T) {
	in := lineup(14)
	first, err := Split(in, 7, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	second, err := Split(in, 7, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	for i := range first.TeamA.Players {
		if first.TeamA.Players[i].ID != second.TeamA.Players[i].ID {
			t.Fatal("two runs on the same pool produced different teams")
		}
	}
}

func TestIdenticalPlayersProduceAPerfectBalance(t *testing.T) {
	in := make([]model.Player, 0, 12)
	for i := 1; i <= 12; i++ {
		in = append(in, p(i, fmt.Sprintf("Same%d", i), 7, 6, 2, 7.4))
	}
	res, err := Split(in, 6, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	assertPartition(t, res, in, 6)
	if res.Difference != 0 || res.Balance != 100 {
		t.Errorf("identical players should balance perfectly, got diff=%v balance=%v", res.Difference, res.Balance)
	}
	if res.Objective != 0 {
		t.Errorf("objective = %v, want 0", res.Objective)
	}
}

func TestDecimalRatingsAreHandledExactly(t *testing.T) {
	in := lineup(10)
	var totalAttack float64
	for i := range in {
		in[i].Attack = 5.5 + float64(i)*0.1
		in[i].Defense = 6.4 - float64(i)*0.05
		in[i].Overall = 7.25
		totalAttack += in[i].Attack
	}
	res, err := Split(in, 5, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	assertPartition(t, res, in, 5)

	if got := res.TeamA.Attack + res.TeamB.Attack; math.Abs(got-totalAttack) > 0.06 {
		t.Errorf("attack totals %v + %v = %v, want %v", res.TeamA.Attack, res.TeamB.Attack, got, totalAttack)
	}
	// One decimal for sums, two for scores: never more.
	for _, v := range []float64{res.TeamA.Attack, res.TeamA.Defense, res.TeamB.Goalkeeping, res.TeamB.Overall} {
		if math.Abs(v*10-math.Round(v*10)) > 1e-9 {
			t.Errorf("%v is reported with more than one decimal", v)
		}
	}
	for _, v := range []float64{res.TeamA.WeightedScore, res.TeamB.WeightedScore} {
		if math.Abs(v*100-math.Round(v*100)) > 1e-9 {
			t.Errorf("weighted score %v is reported with more than two decimals", v)
		}
	}
}

/* ============================== validation =============================== */

func TestSplitRejectsWrongPlayerCount(t *testing.T) {
	in := lineup(11)
	_, err := Split(in, 6, model.DefaultWeights())
	if err == nil {
		t.Fatal("expected an error for 11 players in a 6 vs 6")
	}
	if want := "expected exactly 12 players for 6 vs 6, got 11"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestSplitRejectsOddPool(t *testing.T) {
	if _, err := Split(lineup(13), 6, model.DefaultWeights()); err == nil {
		t.Fatal("expected an error for an odd-sized pool")
	}
}

func TestSplitRejectsTeamSizeOutOfRange(t *testing.T) {
	for _, size := range []int{0, 1, 11, 20} {
		if _, err := Split(lineup(size*2), size, model.DefaultWeights()); err == nil {
			t.Errorf("team size %d should be rejected", size)
		}
	}
}

func TestSplitRejectsBadPayloads(t *testing.T) {
	outOfRange := lineup(4)
	outOfRange[2].Attack = 11.5
	if _, err := Split(outOfRange, 2, model.DefaultWeights()); err == nil {
		t.Error("out-of-range rating should be rejected")
	}

	zeroRating := lineup(4)
	zeroRating[1].Defense = 0
	if _, err := Split(zeroRating, 2, model.DefaultWeights()); err == nil {
		t.Error("a rating below 1 should be rejected")
	}

	duplicated := lineup(4)
	duplicated[3].ID = duplicated[0].ID
	if _, err := Split(duplicated, 2, model.DefaultWeights()); err == nil {
		t.Error("duplicate player id should be rejected")
	}

	nameless := lineup(4)
	nameless[1].Name = "   "
	if _, err := Split(nameless, 2, model.DefaultWeights()); err == nil {
		t.Error("blank name should be rejected")
	}

	negative := lineup(4)
	if _, err := Split(negative, 2, model.Weights{Attack: -10, Defense: 50}); err == nil {
		t.Error("negative weights should be rejected")
	}
}

func TestWeightsAreNormalisedToHundred(t *testing.T) {
	res, err := Split(lineup(10), 5, model.Weights{Attack: 3, Defense: 3, Goalkeeping: 1, Overall: 3})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	want := model.DefaultWeights() // 30/30/10/30 describes the same profile as 3/3/1/3
	if res.Weights != want {
		t.Errorf("echoed weights = %+v, want %+v", res.Weights, want)
	}
}

func TestZeroWeightsFallBackToEqualImportance(t *testing.T) {
	in := lineup(10)
	res, err := Split(in, 5, model.Weights{})
	if err != nil {
		t.Fatalf("Split with all-zero weights: %v", err)
	}
	assertPartition(t, res, in, 5)
	want := model.Weights{Attack: 25, Defense: 25, Goalkeeping: 25, Overall: 25}
	if res.Weights != want {
		t.Errorf("echoed weights = %+v, want %+v", res.Weights, want)
	}
}

func TestSingleSidedWeightIsAccepted(t *testing.T) {
	in := lineup(10)
	res, err := Split(in, 5, model.Weights{Overall: 100})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	assertPartition(t, res, in, 5)
	if want := (model.Weights{Overall: 100}); res.Weights != want {
		t.Errorf("echoed weights = %+v, want %+v", res.Weights, want)
	}
	// With overall as the only criterion, the overall gap must be at least as
	// tight as under the default profile.
	loose, err := Split(in, 5, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	gap := func(r *model.BalanceResult) float64 { return math.Abs(r.TeamA.Overall - r.TeamB.Overall) }
	if gap(res) > gap(loose)+1e-9 {
		t.Errorf("overall-only weights gave a worse overall gap (%v) than the default profile (%v)", gap(res), gap(loose))
	}
}

/* ============================ weights & GK ============================== */

func TestUnequalWeightsDriveTheSplit(t *testing.T) {
	// Two goalscorers and two defenders. A sane split always pairs one of each;
	// the weights decide which axis is protected when they cannot all be equal.
	in := []model.Player{
		p(1, "Striker1", 10, 1, 1, 5),
		p(2, "Striker2", 10, 1, 1, 5),
		p(3, "Defender1", 1, 10, 1, 5),
		p(4, "Defender2", 1, 10, 1, 5),
	}

	res, err := Split(in, 2, model.Weights{Attack: 100})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	assertPartition(t, res, in, 2)
	if res.TeamA.Attack != res.TeamB.Attack {
		t.Errorf("attack-only weights must split the strikers: %v vs %v", res.TeamA.Attack, res.TeamB.Attack)
	}
	if res.Objective != 0 {
		t.Errorf("a perfectly attack-balanced split should score 0, got %v", res.Objective)
	}

	res, err = Split(in, 2, model.Weights{Defense: 70, Goalkeeping: 30})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if res.TeamA.Defense != res.TeamB.Defense {
		t.Errorf("defense-weighted split failed: %v vs %v", res.TeamA.Defense, res.TeamB.Defense)
	}
}

func TestGoalkeepersAreSpreadAcrossTeams(t *testing.T) {
	// Three keeper-capable players across two teams of five: the best reachable
	// spread is 2/1, so 3/0 must never come out of the search.
	in := []model.Player{
		p(1, "GK1", 5, 5, 9, 6.0),
		p(2, "GK2", 5, 5, 8, 6.0),
		p(3, "GK3", 5, 5, 7, 6.0),
		p(4, "F1", 7, 6, 1, 7.0),
		p(5, "F2", 7, 6, 1, 7.0),
		p(6, "F3", 6, 7, 1, 7.0),
		p(7, "F4", 6, 7, 1, 7.0),
		p(8, "F5", 8, 5, 1, 7.2),
		p(9, "F6", 5, 8, 1, 7.2),
		p(10, "F7", 6, 6, 1, 6.8),
	}
	res, err := Split(in, 5, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	assertPartition(t, res, in, 5)

	if d := int(math.Abs(float64(res.TeamA.Goalkeepers - res.TeamB.Goalkeepers))); d > 1 {
		t.Errorf("keepers stacked: %d vs %d", res.TeamA.Goalkeepers, res.TeamB.Goalkeepers)
	}
	if res.TeamA.Goalkeepers == 0 || res.TeamB.Goalkeepers == 0 {
		t.Errorf("one team has no goalkeeper-capable player: %d vs %d", res.TeamA.Goalkeepers, res.TeamB.Goalkeepers)
	}
	if res.TeamA.BestGoalkeeping == 0 || res.TeamB.BestGoalkeeping == 0 {
		t.Error("both teams should report their best keeper")
	}
}

func TestObjectivePrefersSpreadKeepersOverMarginallyBetterScore(t *testing.T) {
	stacked := diff{weighted: 0.20, keepers: 2, keeperGap: 7}
	spread := diff{weighted: 0.40, keepers: 0, keeperGap: 0}
	if Objective(stacked) <= Objective(spread) {
		t.Errorf("the objective should pay a small score difference to spread keepers: stacked=%.3f spread=%.3f",
			Objective(stacked), Objective(spread))
	}
}

func TestGoalkeepingIsNotTreatedAsOverall(t *testing.T) {
	// Same overall on both sides, but one team hoards the goalkeeping: the
	// category terms must make that arrangement worse than a neutral one.
	hoarded := diff{weighted: 0, overall: 0, goalkeeping: 14, keepers: 2, keeperGap: 6}
	neutral := diff{weighted: 0, overall: 0, goalkeeping: 2, keepers: 0, keeperGap: 0}
	if Objective(hoarded) <= Objective(neutral) {
		t.Errorf("goalkeeping imbalance must be penalised independently: %v vs %v",
			Objective(hoarded), Objective(neutral))
	}
}

/* ================================ search ================================= */

func TestExhaustiveEvaluatesEachUnorderedSplitOnce(t *testing.T) {
	// 12 players in teams of 6 is C(12,6) = 924 ordered choices but only 462
	// unordered splits: mirror images must not be evaluated twice.
	in := lineup(12)
	res, err := Split(in, 6, model.DefaultWeights())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if res.Explored != 462 {
		t.Errorf("explored = %d, want 462", res.Explored)
	}
}

func TestCombinations(t *testing.T) {
	for _, tc := range []struct {
		n, k int
		want int64
	}{
		{12, 6, 924}, {14, 7, 3432}, {16, 8, 12870}, {18, 9, 48620}, {20, 10, 184756},
		{11, 5, 462}, {5, 0, 1}, {0, 0, 1}, {3, 5, 0}, {-1, 2, 0},
	} {
		if got := combinations(tc.n, tc.k); got != tc.want {
			t.Errorf("C(%d,%d) = %d, want %d", tc.n, tc.k, got, tc.want)
		}
	}
	if got := combinations(200, 100); got != math.MaxInt64 {
		t.Errorf("C(200,100) = %d, want saturation at MaxInt64", got)
	}
}

func TestLocalSearchFallbackStillBalances(t *testing.T) {
	// The fallback exists for pools larger than the exhaustive ceiling. Pairwise
	// swap hill climbing can stop at a local optimum, so it is held to "usable
	// and close" rather than to global optimality. Measured gap on these pools:
	// at most +0.42 objective, and it matches the exact optimum at 5 of 6 sizes.
	for _, size := range []int{5, 6, 7, 8, 9, 10} {
		in := lineup(size * 2)
		fb, err := SplitWith(in, Options{
			TeamSize:                  size,
			Weights:                   model.DefaultWeights(),
			MaxExhaustiveCombinations: 1, // forces the fallback path
		})
		if err != nil {
			t.Fatalf("%dv%d SplitWith: %v", size, size, err)
		}
		assertPartition(t, fb, in, size)
		if fb.Method != MethodLocalSearch {
			t.Errorf("method = %q, want %q", fb.Method, MethodLocalSearch)
		}
		if fb.Balance < 99 {
			t.Errorf("%dv%d fallback balance = %.1f%%, expected a usable result", size, size, fb.Balance)
		}

		exact, err := Split(in, size, model.DefaultWeights())
		if err != nil {
			t.Fatalf("%dv%d Split: %v", size, size, err)
		}
		if fb.Objective > exact.Objective+1.0 {
			t.Errorf("%dv%d fallback objective %v drifted more than 1.0 away from the exact optimum %v",
				size, size, fb.Objective, exact.Objective)
		}
	}
}

func TestObjectiveCoefficientsAreAdditive(t *testing.T) {
	d := diff{weighted: 10, attack: 10, defense: 10, goalkeeping: 10, overall: 10, keepers: 10, keeperGap: 10}
	want := 10 * (WeightedScoreCoefficient + AttackDiffCoefficient + DefenseDiffCoefficient +
		GoalkeepingDiffCoefficient + OverallDiffCoefficient + GoalkeeperCountCoefficient + GoalkeeperGapCoefficient)
	if got := Objective(d); math.Abs(got-want) > 1e-9 {
		t.Errorf("Objective = %v, want %v", got, want)
	}
	if got := Objective(diff{}); got != 0 {
		t.Errorf("a perfect split should score 0, got %v", got)
	}
}

/* ============================== micro benchmarks ========================= */

func BenchmarkSplit5v5(b *testing.B) {
	in := lineup(10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Split(in, 5, model.DefaultWeights()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSplit10v10(b *testing.B) {
	in := lineup(20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Split(in, 10, model.DefaultWeights()); err != nil {
			b.Fatal(err)
		}
	}
}
