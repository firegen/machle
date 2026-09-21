// Package storage abstracts player persistence. The HTTP and balancing layers
// only ever see the Repository interface, so swapping the JSON file for
// PostgreSQL (or anything else) means adding one implementation, not touching
// business logic.
package storage

import (
	"errors"

	"football-balancer/internal/model"
)

// ErrNotFound is returned when a player ID does not exist.
var ErrNotFound = errors.New("player not found")

// ErrInvalid wraps a model validation failure so callers can tell bad payloads
// apart from real storage faults without string matching.
var ErrInvalid = errors.New("invalid player")

// Repository is the contract every storage backend implements.
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
