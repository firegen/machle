package storage

import (
	"fmt"
	"sort"
	"sync"

	"football-balancer/internal/model"
)

// MatchJSONStore keeps saved fixtures in their own JSON file, separate from the
// roster: a match is a historical record and must never be rewritten by roster
// edits.
type MatchJSONStore struct {
	mu      sync.RWMutex
	path    string
	matches []model.Match
	lastID  int // high-water mark, so a deleted match ID is not reused
}

// compile-time proof that MatchJSONStore is a MatchRepository.
var _ MatchRepository = (*MatchJSONStore)(nil)

// NewMatchJSONStore opens path, creating an empty file when it is absent.
func NewMatchJSONStore(path string) (*MatchJSONStore, error) {
	s := &MatchJSONStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *MatchJSONStore) load() error {
	var matches []model.Match
	missing, err := readJSONFile(s.path, &matches)
	if err != nil {
		return err
	}
	if matches == nil {
		matches = []model.Match{}
	}
	s.matches = matches
	s.sortByID()

	highest := 0
	for _, m := range s.matches {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("%s: match %d is invalid: %w", s.path, m.ID, err)
		}
		if m.ID <= 0 {
			return fmt.Errorf("%s: matches must have positive ids", s.path)
		}
		if m.ID > highest {
			highest = m.ID
		}
	}
	s.lastID = highest

	if missing {
		return s.save() // materialise an empty list so the file is discoverable
	}
	return nil
}

func (s *MatchJSONStore) save() error { return writeJSONFile(s.path, s.matches) }

// List returns a copy of every saved match, oldest first.
func (s *MatchJSONStore) List() ([]model.Match, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Match, len(s.matches))
	copy(out, s.matches)
	return out, nil
}

// Get returns one match by ID.
func (s *MatchJSONStore) Get(id int) (model.Match, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.indexOf(id)
	if !ok {
		return model.Match{}, fmt.Errorf("%w: match %d", ErrNotFound, id)
	}
	return s.matches[i], nil
}

// Add assigns the next unused ID and persists the list.
func (s *MatchJSONStore) Add(m model.Match) (model.Match, error) {
	if err := m.Validate(); err != nil {
		return model.Match{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastID++
	m.ID = s.lastID
	s.matches = append(s.matches, m)
	if err := s.save(); err != nil {
		s.matches = s.matches[:len(s.matches)-1]
		s.lastID--
		return model.Match{}, err
	}
	return m, nil
}

// Update replaces a stored match in place, keeping the list order.
func (s *MatchJSONStore) Update(m model.Match) (model.Match, error) {
	if err := m.Validate(); err != nil {
		return model.Match{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.indexOf(m.ID)
	if !ok {
		return model.Match{}, fmt.Errorf("%w: match %d", ErrNotFound, m.ID)
	}
	previous := s.matches[i]
	s.matches[i] = m
	if err := s.save(); err != nil {
		s.matches[i] = previous
		return model.Match{}, err
	}
	return m, nil
}

// Delete removes a match by ID.
func (s *MatchJSONStore) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.indexOf(id)
	if !ok {
		return fmt.Errorf("%w: match %d", ErrNotFound, id)
	}
	previous := s.matches
	s.matches = append(append([]model.Match{}, previous[:i]...), previous[i+1:]...)
	if err := s.save(); err != nil {
		s.matches = previous
		return err
	}
	return nil
}

func (s *MatchJSONStore) indexOf(id int) (int, bool) {
	for i, m := range s.matches {
		if m.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (s *MatchJSONStore) sortByID() {
	sort.SliceStable(s.matches, func(i, j int) bool { return s.matches[i].ID < s.matches[j].ID })
}
