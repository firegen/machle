package api

import (
	"fmt"
	"net/http"
	"time"

	"football-balancer/internal/model"
)

// createMatchRequest records a drawn fixture. The client sends only player IDs:
// names and ratings are read back from the roster by the server, so a match
// snapshot cannot be skewed by a stale browser tab.
type createMatchRequest struct {
	TeamSize int            `json:"teamSize"`
	TeamA    []int          `json:"teamA"`
	TeamB    []int          `json:"teamB"`
	Weights  *model.Weights `json:"weights,omitempty"`
	Date     *time.Time     `json:"date,omitempty"`
}

func (s *Server) handleListMatches(w http.ResponseWriter, _ *http.Request) {
	matches, err := s.matches.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	model.SortMatchesNewestFirst(matches)
	if matches == nil {
		matches = []model.Match{}
	}
	writeJSON(w, http.StatusOK, matches)
}

func (s *Server) handleGetMatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	m, err := s.matches.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleCreateMatch(w http.ResponseWriter, r *http.Request) {
	var req createMatchRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	weights := model.DefaultWeights()
	if req.Weights != nil {
		weights = *req.Weights
	}
	if err := weights.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	roster, err := s.repo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	byID := make(map[int]model.Player, len(roster))
	for _, p := range roster {
		byID[p.ID] = p
	}
	teamA, err := resolveLineup("team A", req.TeamA, byID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	teamB, err := resolveLineup("team B", req.TeamB, byID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Re-generating the same draw must not pile up duplicates: reuse the
	// unfinished match that holds exactly these teams (either way round).
	existing, err := s.matches.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, m := range existing {
		if m.Status == model.MatchUpcoming && m.SameLineup(req.TeamA, req.TeamB) {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}

	m, err := model.NewMatch(teamA, teamB, weights, derefTime(req.Date))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := s.matches.Add(m)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// matchUpdateRequest carries the mutable parts of a saved match: the score, the
// ratings, the award and the date. Line-ups may only be replaced while the match
// has no result, and a match can be cleared back to that state.
type matchUpdateRequest struct {
	Date               *time.Time            `json:"date,omitempty"`
	Status             *string               `json:"status,omitempty"`
	GoalsA             *int                  `json:"goalsA,omitempty"`
	GoalsB             *int                  `json:"goalsB,omitempty"`
	Ratings            *[]model.PlayerRating `json:"ratings,omitempty"`
	ManOfTheMatch      *int                  `json:"manOfTheMatch,omitempty"`
	ClearManOfTheMatch bool                  `json:"clearManOfTheMatch,omitempty"`
	ClearResult        bool                  `json:"clearResult,omitempty"`
	TeamA              []int                 `json:"teamA,omitempty"`
	TeamB              []int                 `json:"teamB,omitempty"`
}

func (s *Server) handleUpdateMatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req matchUpdateRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.GoalsA != nil && *req.GoalsA < 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("team A goals must not be negative"))
		return
	}
	if req.GoalsB != nil && *req.GoalsB < 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("team B goals must not be negative"))
		return
	}
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown match status %q", *req.Status))
		return
	}
	if (len(req.TeamA) == 0) != (len(req.TeamB) == 0) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("re-drawing a match needs both teamA and teamB"))
		return
	}

	stored, err := s.matches.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	if len(req.TeamA) > 0 {
		roster, err := s.repo.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		byID := make(map[int]model.Player, len(roster))
		for _, p := range roster {
			byID[p.ID] = p
		}
		teamA, err := resolveLineup("team A", req.TeamA, byID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		teamB, err := resolveLineup("team B", req.TeamB, byID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		// Re-drawing keeps the match's own weight profile: changing the profile is
		// what generating a fresh fixture is for.
		if err := stored.Redraw(teamA, teamB, stored.Weights, req.Date); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	if req.ClearResult {
		if err := stored.ClearResult(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	if err := stored.Apply(model.MatchResult{
		Date:          req.Date,
		Status:        req.Status,
		GoalsA:        req.GoalsA,
		GoalsB:        req.GoalsB,
		Ratings:       req.Ratings,
		ManOfTheMatch: req.ManOfTheMatch,
		ClearMotm:     req.ClearManOfTheMatch,
	}); err != nil {
		// Every rejection here is inconsistent input: unknown player, a rating out
		// of range, or a status the stored data cannot support.
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.matches.Update(stored)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteMatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.matches.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePlayerHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	player, err := s.repo.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	matches, err := s.matches.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, model.HistoryOf(matches, player))
}

// resolveLineup maps requested IDs onto roster entries, rejecting players who
// are not in the roster.
func resolveLineup(label string, ids []int, byID map[int]model.Player) ([]model.Player, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s has no players", label)
	}
	seen := make(map[int]bool, len(ids))
	out := make([]model.Player, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("%s lists player %d twice", label, id)
		}
		seen[id] = true
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%s refers to player %d, who is not in the roster", label, id)
		}
		out = append(out, p)
	}
	return out, nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
