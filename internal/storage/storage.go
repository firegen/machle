// Package storage abstracts player persistence. The HTTP and balancing layers
// only ever see the Repository interface, so swapping the JSON file for
// PostgreSQL (or anything else) means adding one implementation, not touching
// business logic.
package storage

import (
	"errors"

	"football-balancer/internal/model"
)

// ErrNotFound is returned when a player or match ID does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalid wraps a model validation failure so callers can tell bad payloads
// apart from real storage faults without string matching.
var ErrInvalid = errors.New("invalid record")

// Repository is the contract every player storage backend implements.
type Repository interface {
	// List returns all players, ordered by ID.
	List() ([]model.Player, error)
	// Get returns the player with the given ID.
	Get(id int) (model.Player, error)
	// Add stores a new player and returns it with an assigned ID.
	Add(p model.Player) (model.Player, error)
	// Update replaces the player whose ID matches p.ID.
	Update(p model.Player) (model.Player, error)
	// Delete removes a player by ID.
	Delete(id int) error
}

// MatchRepository is the same contract for saved fixtures. It is a separate
// interface on purpose: matches are a different entity with a different file,
// and a future PostgreSQL backend can implement one, the other, or both.
type MatchRepository interface {
	// List returns all saved matches, ordered by ID.
	List() ([]model.Match, error)
	// Get returns one match by ID.
	Get(id int) (model.Match, error)
	// Add stores a new match and returns it with an assigned ID.
	Add(m model.Match) (model.Match, error)
	// Update replaces the match whose ID matches m.ID.
	Update(m model.Match) (model.Match, error)
	// Delete removes a match by ID.
	Delete(id int) error
}
