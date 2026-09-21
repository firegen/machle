package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"football-balancer/internal/model"
)

// JSONStore keeps the roster in a single JSON file, fully loaded in memory.
//
// Writes go to a temporary file that is then renamed over the target, so a
// crash mid-write can never leave a half-written roster behind.
type JSONStore struct {
	mu      sync.RWMutex
	path    string
	players []model.Player
	// lastID is a high-water mark: an ID freed by a delete is never handed out
	// again while the process runs, so a stale selection in a browser tab can
	// never silently point at a different player.
	lastID int
}

// compile-time proof that JSONStore is a Repository.
var _ Repository = (*JSONStore)(nil)

// NewJSONStore opens path, creating and seeding it with DefaultPlayers when it
// does not exist yet.
func NewJSONStore(path string) (*JSONStore, error) {
	s := &JSONStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JSONStore) load() error {
	data, err := os.ReadFile(s.path)
	seeded := false
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.players = DefaultPlayers()
		seeded = true
	case err != nil:
		return fmt.Errorf("read %s: %w", s.path, err)
	default:
		var players []model.Player
		if err := json.Unmarshal(data, &players); err != nil {
			return fmt.Errorf("parse %s: %w (fix or delete the file to restore the example roster)", s.path, err)
		}
		if players == nil {
			players = []model.Player{}
		}
		s.players = players
	}

	s.sortByID()
	for _, p := range s.players {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("%s: player %q is invalid: %w", s.path, p.Name, err)
		}
	}
	if err := s.checkIDs(); err != nil {
		return err
	}

	if seeded {
		return s.save() // write the example roster out so the file exists
	}
	return nil
}

// checkIDs validates the part of the file contract that ID allocation and the
// balancing bitset depend on.
func (s *JSONStore) checkIDs() error {
	s.lastID = 0
	seen := make(map[int]bool, len(s.players))
	for _, p := range s.players {
		if p.ID <= 0 {
			return fmt.Errorf("%s: player %q has id %d; ids must be positive", s.path, p.Name, p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("%s: duplicate player id %d", s.path, p.ID)
		}
		seen[p.ID] = true
		if p.ID > s.lastID {
			s.lastID = p.ID
		}
	}
	return nil
}

// save writes the roster atomically: a temp file in the same directory is
// fsync'ed and then renamed over the target. The caller must hold the write lock.
func (s *JSONStore) save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(s.players, "", "  ")
	if err != nil {
		return fmt.Errorf("encode players: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".players-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(name)
		}
	}()

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	committed = true
	return nil
}

// List returns a copy of the roster so callers cannot mutate stored state.
func (s *JSONStore) List() ([]model.Player, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Player, len(s.players))
	copy(out, s.players)
	return out, nil
}

func (s *JSONStore) Get(id int) (model.Player, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.indexOf(id)
	if !ok {
		return model.Player{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return s.players[i], nil
}

// Add assigns the next unused ID and persists the roster.
func (s *JSONStore) Add(p model.Player) (model.Player, error) {
	p.Name = strings.TrimSpace(p.Name)
	if err := p.Validate(); err != nil {
		return model.Player{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastID++
	p.ID = s.lastID
	s.players = append(s.players, p)
	if err := s.save(); err != nil {
		s.players = s.players[:len(s.players)-1]
		s.lastID--
		return model.Player{}, err
	}
	return p, nil
}

// Update replaces an existing player, keeping the roster order stable.
func (s *JSONStore) Update(p model.Player) (model.Player, error) {
	p.Name = strings.TrimSpace(p.Name)
	if err := p.Validate(); err != nil {
		return model.Player{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.indexOf(p.ID)
	if !ok {
		return model.Player{}, fmt.Errorf("%w: %d", ErrNotFound, p.ID)
	}
	previous := s.players[i]
	s.players[i] = p
	if err := s.save(); err != nil {
		s.players[i] = previous
		return model.Player{}, err
	}
	return p, nil
}

// Delete removes a player by ID.
func (s *JSONStore) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.indexOf(id)
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	previous := s.players
	s.players = append(append([]model.Player{}, previous[:i]...), previous[i+1:]...)
	if err := s.save(); err != nil {
		s.players = previous
		return err
	}
	return nil
}

func (s *JSONStore) indexOf(id int) (int, bool) {
	for i, p := range s.players {
		if p.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (s *JSONStore) sortByID() {
	sort.SliceStable(s.players, func(i, j int) bool { return s.players[i].ID < s.players[j].ID })
}
