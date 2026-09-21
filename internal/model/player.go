// Package model holds the domain types shared by the storage, balancing and
// HTTP layers. It has no dependency on transport or persistence concerns.
package model

import (
	"fmt"
	"math"
	"strings"
)

// Rating bounds for every individual attribute.
const (
	MinRating = 1.0
	MaxRating = 10.0
)

// GoalkeeperThreshold is the Goalkeeping rating from which a player is
// considered goalkeeper-capable. It is an indication used for balancing and for
// the UI badge, never a hard rule that forces a player into goal.
const GoalkeeperThreshold = 5.0

// Player is a single footballer with individual ratings.
type Player struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Attack      float64 `json:"attack"`
	Defense     float64 `json:"defense"`
	Goalkeeping float64 `json:"goalkeeping"`
	Overall     float64 `json:"overall"`
}

// Validate reports whether the player can be stored or balanced.
func (p Player) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required")
	}
	for _, r := range []struct {
		label string
		value float64
	}{
		{"attack", p.Attack},
		{"defense", p.Defense},
		{"goalkeeping", p.Goalkeeping},
		{"overall", p.Overall},
	} {
		if err := validateRating(r.label, r.value); err != nil {
			return err
		}
	}
	return nil
}

func validateRating(label string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s must be a finite number", label)
	}
	if v < MinRating || v > MaxRating {
		return fmt.Errorf("%s must be between %g and %g, got %g", label, MinRating, MaxRating, v)
	}
	return nil
}

// IsGoalkeeper reports whether the ratings suggest the player can keep goal.
func (p Player) IsGoalkeeper() bool {
	return p.Goalkeeping >= GoalkeeperThreshold
}

// Weights are the relative importances of the four attributes. They are
// accepted on any scale (percent points, ratios, ...) and normalised before use.
type Weights struct {
	Attack      float64 `json:"attack"`
	Defense     float64 `json:"defense"`
	Goalkeeping float64 `json:"goalkeeping"`
	Overall     float64 `json:"overall"`
}

// DefaultWeights is the shipped configuration: 30/30/10/30.
func DefaultWeights() Weights {
	return Weights{Attack: 30, Defense: 30, Goalkeeping: 10, Overall: 30}
}

// Sum returns the raw (pre-normalisation) total of the weights.
func (w Weights) Sum() float64 {
	return w.Attack + w.Defense + w.Goalkeeping + w.Overall
}

// ValidateNonNegative rejects weights that cannot form a proportion. An
// all-zero profile is allowed here: callers may still normalise it into four
// equally important attributes.
func (w Weights) ValidateNonNegative() error {
	for _, e := range []struct {
		label string
		value float64
	}{
		{"attack", w.Attack},
		{"defense", w.Defense},
		{"goalkeeping", w.Goalkeeping},
		{"overall", w.Overall},
	} {
		if math.IsNaN(e.value) || math.IsInf(e.value, 0) {
			return fmt.Errorf("weight %s must be a finite number", e.label)
		}
		if e.value < 0 {
			return fmt.Errorf("weight %s must not be negative, got %g", e.label, e.value)
		}
	}
	return nil
}

// Validate is the input-boundary check used by the API: the weights must be
// usable *and* say something, so an all-zero profile is rejected with a message
// the user can act on.
func (w Weights) Validate() error {
	if err := w.ValidateNonNegative(); err != nil {
		return err
	}
	if w.Sum() <= 0 {
		return fmt.Errorf("all weights are zero: give at least one attribute a weight above 0")
	}
	return nil
}

// Normalized scales the weights so that they add up to 1. A zeroed profile
// degrades gracefully into four equally important attributes instead of
// producing a division by zero.
func (w Weights) Normalized() Weights {
	sum := w.Sum()
	if sum <= 0 {
		return Weights{Attack: 0.25, Defense: 0.25, Goalkeeping: 0.25, Overall: 0.25}
	}
	return Weights{
		Attack:      w.Attack / sum,
		Defense:     w.Defense / sum,
		Goalkeeping: w.Goalkeeping / sum,
		Overall:     w.Overall / sum,
	}
}

// AsPercent scales the weights so that they add up to 100, which is what the
// UI displays and echoes back to the API.
func (w Weights) AsPercent() Weights {
	sum := w.Sum()
	if sum <= 0 {
		return Weights{Attack: 25, Defense: 25, Goalkeeping: 25, Overall: 25}
	}
	f := 100 / sum
	return Weights{
		Attack:      RoundTo(w.Attack*f, 2),
		Defense:     RoundTo(w.Defense*f, 2),
		Goalkeeping: RoundTo(w.Goalkeeping*f, 2),
		Overall:     RoundTo(w.Overall*f, 2),
	}
}

// Score returns the weighted contribution of one player. w must already be
// normalized (see Normalized), so the result stays on the 1..10 rating scale.
func (w Weights) Score(p Player) float64 {
	return p.Attack*w.Attack + p.Defense*w.Defense + p.Goalkeeping*w.Goalkeeping + p.Overall*w.Overall
}

// Team is one side of a generated match plus its aggregated statistics.
type Team struct {
	Players         []Player `json:"players"`
	Attack          float64  `json:"attack"`
	Defense         float64  `json:"defense"`
	Goalkeeping     float64  `json:"goalkeeping"`
	Overall         float64  `json:"overall"`
	WeightedScore   float64  `json:"weightedScore"`
	Goalkeepers     int      `json:"goalkeepers"`
	BestGoalkeeping float64  `json:"bestGoalkeeping"`
}

// NewTeam aggregates a group of players. weights must be normalized.
func NewTeam(players []Player, weights Weights) Team {
	t := Team{Players: append([]Player(nil), players...)}
	for _, p := range players {
		t.Attack += p.Attack
		t.Defense += p.Defense
		t.Goalkeeping += p.Goalkeeping
		t.Overall += p.Overall
		t.WeightedScore += weights.Score(p)
		if p.IsGoalkeeper() {
			t.Goalkeepers++
		}
		if p.Goalkeeping > t.BestGoalkeeping {
			t.BestGoalkeeping = p.Goalkeeping
		}
	}
	return t
}

// BalanceResult is the answer of the team generator.
type BalanceResult struct {
	TeamA      Team    `json:"teamA"`
	TeamB      Team    `json:"teamB"`
	Balance    float64 `json:"balance"`
	Difference float64 `json:"difference"`
	Objective  float64 `json:"objective"`
	TeamSize   int     `json:"teamSize"`
	Method     string  `json:"method"`
	Explored   int64   `json:"explored"`
	Weights    Weights `json:"weights"`
}

// BalancePercent converts an absolute score difference into a 0..100 balance
// percentage relative to the average score of both teams.
func BalancePercent(a, b float64) float64 {
	mean := (a + b) / 2
	if mean <= 0 {
		return 100
	}
	balance := (1 - math.Abs(a-b)/mean) * 100
	switch {
	case balance < 0:
		return 0
	case balance > 100:
		return 100
	default:
		return balance
	}
}

// RoundTo rounds v to a fixed number of decimals. Ratings are shown with one
// decimal, money-like aggregates with two; never more, to avoid implying a
// precision the input does not have.
func RoundTo(v float64, places int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	f := math.Pow(10, float64(places))
	return math.Round(v*f) / f
}
