package model

import (
	"math"
	"strings"
	"testing"
)

func TestPlayerValidate(t *testing.T) {
	ok := Player{ID: 1, Name: "Ivan", Attack: 9, Defense: 6, Goalkeeping: 1, Overall: 8}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid player rejected: %v", err)
	}
	decimal := Player{ID: 1, Name: "Ivan", Attack: 9, Defense: 6, Goalkeeping: 1, Overall: 7.5}
	if err := decimal.Validate(); err != nil {
		t.Errorf("decimal overall rejected: %v", err)
	}
	edges := Player{ID: 1, Name: "Ivan", Attack: 1, Defense: 10, Goalkeeping: 1, Overall: 10}
	if err := edges.Validate(); err != nil {
		t.Errorf("the 1 and 10 bounds are inclusive, got %v", err)
	}

	bad := []struct {
		label  string
		player Player
		field  string
	}{
		{"empty name", Player{Name: "", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5}, "name"},
		{"blank name", Player{Name: "   ", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5}, "name"},
		{"attack high", Player{Name: "A", Attack: 10.1, Defense: 5, Goalkeeping: 1, Overall: 5}, "attack"},
		{"defense low", Player{Name: "A", Attack: 5, Defense: 0.9, Goalkeeping: 1, Overall: 5}, "defense"},
		{"goalkeeping zero", Player{Name: "A", Attack: 5, Defense: 5, Goalkeeping: 0, Overall: 5}, "goalkeeping"},
		{"overall negative", Player{Name: "A", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: -1}, "overall"},
		{"nan", Player{Name: "A", Attack: math.NaN(), Defense: 5, Goalkeeping: 1, Overall: 5}, "attack"},
		{"inf", Player{Name: "A", Attack: math.Inf(1), Defense: 5, Goalkeeping: 1, Overall: 5}, "attack"},
	}
	for _, tc := range bad {
		err := tc.player.Validate()
		if err == nil {
			t.Errorf("%s: expected a validation error", tc.label)
			continue
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s: error %q should name the offending field %q", tc.label, err, tc.field)
		}
	}
}

func TestIsGoalkeeper(t *testing.T) {
	if !(Player{Goalkeeping: GoalkeeperThreshold}).IsGoalkeeper() {
		t.Errorf("Goalkeeping %g should count as keeper-capable", GoalkeeperThreshold)
	}
	if (Player{Goalkeeping: GoalkeeperThreshold - 0.1}).IsGoalkeeper() {
		t.Error("just below the threshold should not count as keeper-capable")
	}
	if !(Player{Attack: 10, Defense: 10, Goalkeeping: 10, Overall: 10}).IsGoalkeeper() {
		t.Error("an elite all-rounder is still keeper-capable; the rating is an indicator, not a position")
	}
}

func TestWeightsNormalized(t *testing.T) {
	nw := DefaultWeights().Normalized()
	if sum := nw.Attack + nw.Defense + nw.Goalkeeping + nw.Overall; math.Abs(sum-1) > 1e-12 {
		t.Errorf("normalized weights sum to %v, want 1", sum)
	}
	if math.Abs(nw.Goalkeeping-0.1) > 1e-12 {
		t.Errorf("goalkeeping weight = %v, want 0.1", nw.Goalkeeping)
	}
	// Another scale describing the same profile normalises identically.
	if got := (Weights{Attack: 3, Defense: 3, Goalkeeping: 1, Overall: 3}).Normalized(); got != nw {
		t.Errorf("3/3/1/3 normalises to %+v, want %+v", got, nw)
	}
	// Zeroed input must not divide by zero; it degrades into equal importance.
	if got := (Weights{}).Normalized(); got != (Weights{Attack: 0.25, Defense: 0.25, Goalkeeping: 0.25, Overall: 0.25}) {
		t.Errorf("zero weights normalise to %+v, want four equal shares", got)
	}
}

func TestWeightsAsPercentSumsToHundred(t *testing.T) {
	for _, w := range []Weights{
		DefaultWeights(),
		{Attack: 1, Defense: 1, Goalkeeping: 1, Overall: 1},
		{Attack: 7, Defense: 13, Goalkeeping: 1, Overall: 3},
		{Attack: 0, Defense: 0, Goalkeeping: 100, Overall: 0},
		{Attack: 1, Defense: 1, Goalkeeping: 1},
	} {
		p := w.AsPercent()
		if sum := p.Attack + p.Defense + p.Goalkeeping + p.Overall; math.Abs(sum-100) > 0.05 {
			t.Errorf("%+v → %+v sums to %v, want 100", w, p, sum)
		}
	}
	if got := (Weights{}).AsPercent(); got != (Weights{Attack: 25, Defense: 25, Goalkeeping: 25, Overall: 25}) {
		t.Errorf("zero weights as percent = %+v, want four equal shares", got)
	}
}

func TestWeightsValidate(t *testing.T) {
	if err := DefaultWeights().Validate(); err != nil {
		t.Errorf("default weights rejected: %v", err)
	}
	if err := (Weights{}).Validate(); err == nil {
		t.Error("all-zero weights must be rejected at the API boundary")
	}
	negative := Weights{Attack: -1, Defense: 2}
	if err := negative.Validate(); err == nil {
		t.Error("negative weights must be rejected")
	}
	if err := negative.ValidateNonNegative(); err == nil {
		t.Error("ValidateNonNegative must catch negatives too")
	}
	// The balancer is deliberately more tolerant than the API here.
	if err := (Weights{}).ValidateNonNegative(); err != nil {
		t.Errorf("zero weights are acceptable to the balancer, got %v", err)
	}
}

func TestWeightsScore(t *testing.T) {
	nw := DefaultWeights().Normalized()
	ivan := Player{Attack: 9, Defense: 6, Goalkeeping: 1, Overall: 8}
	// 9·0.3 + 6·0.3 + 1·0.1 + 8·0.3 = 2.7 + 1.8 + 0.1 + 2.4
	if got, want := nw.Score(ivan), 7.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("Score = %v, want %v", got, want)
	}
	// A single-minded profile reduces the score to that attribute.
	if got := (Weights{Attack: 100}).Normalized().Score(ivan); math.Abs(got-9) > 1e-9 {
		t.Errorf("attack-only score = %v, want 9", got)
	}
}

func TestNewTeamAggregates(t *testing.T) {
	nw := DefaultWeights().Normalized()
	players := []Player{
		{ID: 1, Name: "Ivan", Attack: 9, Defense: 6, Goalkeeping: 1, Overall: 8},
		{ID: 20, Name: "Todor", Attack: 3, Defense: 9, Goalkeeping: 9, Overall: 7},
	}
	team := NewTeam(players, nw)

	if team.Attack != 12 || team.Defense != 15 || team.Goalkeeping != 10 || team.Overall != 15 {
		t.Errorf("sums = %v/%v/%v/%v, want 12/15/10/15",
			team.Attack, team.Defense, team.Goalkeeping, team.Overall)
	}
	if want := nw.Score(players[0]) + nw.Score(players[1]); math.Abs(team.WeightedScore-want) > 1e-12 {
		t.Errorf("weighted score = %v, want %v", team.WeightedScore, want)
	}
	if team.Goalkeepers != 1 {
		t.Errorf("goalkeepers = %d, want 1", team.Goalkeepers)
	}
	if team.BestGoalkeeping != 9 {
		t.Errorf("bestGoalkeeping = %v, want 9", team.BestGoalkeeping)
	}
	if len(team.Players) != 2 {
		t.Errorf("roster length = %d, want 2", len(team.Players))
	}

	team.Players[0].Name = "Mutated"
	if players[0].Name != "Ivan" {
		t.Error("NewTeam aliased the caller's slice")
	}
}

func TestNewTeamOfNobody(t *testing.T) {
	team := NewTeam(nil, DefaultWeights().Normalized())
	if team.WeightedScore != 0 || team.Goalkeepers != 0 || team.BestGoalkeeping != 0 {
		t.Errorf("empty team should aggregate to zero, got %+v", team)
	}
}

func TestBalancePercent(t *testing.T) {
	for _, tc := range []struct {
		a, b, want float64
	}{
		{45, 45, 100},
		{0, 0, 100},
		{10, 20, 100 * (1 - 10.0/15.0)},
		{0, 10, 0},
	} {
		if got := BalancePercent(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("BalancePercent(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	// The displayed figure for the worked example: 99.8%, not 99.834729182%.
	if got := RoundTo(BalancePercent(40.14, 40.06), 1); got != 99.8 {
		t.Errorf("displayed balance = %v, want 99.8", got)
	}
	// Always inside 0..100, and symmetric in either direction.
	for _, tc := range []struct{ a, b float64 }{{1, 100}, {100, 1}, {-5, -5}, {7, -7}, {0.1, 0}} {
		got := BalancePercent(tc.a, tc.b)
		if got < 0 || got > 100 {
			t.Errorf("BalancePercent(%v, %v) = %v, outside 0..100", tc.a, tc.b, got)
		}
		if other := BalancePercent(tc.b, tc.a); math.Abs(got-other) > 1e-12 {
			t.Errorf("BalancePercent is not symmetric: %v vs %v", got, other)
		}
	}
}

func TestRoundTo(t *testing.T) {
	for _, tc := range []struct{ in, want1, want2 float64 }{
		{99.834729182, 99.8, 99.83},
		{7.25, 7.3, 7.25},
		{-0.05, -0.1, -0.05},
		{0, 0, 0},
	} {
		if got := RoundTo(tc.in, 1); math.Abs(got-tc.want1) > 1e-12 {
			t.Errorf("RoundTo(%v, 1) = %v, want %v", tc.in, got, tc.want1)
		}
		if got := RoundTo(tc.in, 2); math.Abs(got-tc.want2) > 1e-12 {
			t.Errorf("RoundTo(%v, 2) = %v, want %v", tc.in, got, tc.want2)
		}
	}
	if !math.IsNaN(RoundTo(math.NaN(), 1)) {
		t.Error("NaN should pass through rather than panic")
	}
	if !math.IsInf(RoundTo(math.Inf(1), 2), 1) {
		t.Error("+Inf should pass through rather than become NaN")
	}
}
