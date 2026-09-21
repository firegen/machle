// Package api wires the domain layers to HTTP. It deliberately contains no
// business logic: validation lives in model, balancing in balancer, and
// persistence behind storage.Repository.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"football-balancer/internal/balancer"
	"football-balancer/internal/model"
	"football-balancer/internal/storage"
)

// maxBodyBytes caps every JSON request body.
const maxBodyBytes = 1 << 20 // 1 MiB

// Server exposes the REST API and the static frontend.
type Server struct {
	repo  storage.Repository
	api   *http.ServeMux
	index http.Handler
}

// New builds a Server. static may be nil to skip the frontend routes.
func New(repo storage.Repository, static http.FileSystem) *Server {
	s := &Server{repo: repo, api: http.NewServeMux()}
	s.api.HandleFunc("GET /api/health", s.handleHealth)
	s.api.HandleFunc("GET /api/config", s.handleConfig)
	s.api.HandleFunc("GET /api/players", s.handleListPlayers)
	s.api.HandleFunc("POST /api/players", s.handleCreatePlayer)
	s.api.HandleFunc("PUT /api/players/{id}", s.handleUpdatePlayer)
	s.api.HandleFunc("DELETE /api/players/{id}", s.handleDeletePlayer)
	s.api.HandleFunc("POST /api/balance", s.handleBalance)
	// Subtree fallback for anything the routes above do not cover: it keeps every
	// /api/ answer JSON and stops an API typo from reaching the frontend files.
	s.api.HandleFunc("/api/", s.handleUnrouted)

	if static != nil {
		s.index = http.FileServer(static)
	}
	return s
}

// apiMethods lists what each endpoint accepts, so a known path hit with the
// wrong verb can be answered with 405 and an Allow header instead of a
// misleading 404.
var apiMethods = map[string][]string{
	"/api/health":  {"GET"},
	"/api/config":  {"GET"},
	"/api/players": {"GET", "POST"},
	"/api/balance": {"POST"},
}

func (s *Server) handleUnrouted(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	allowed, known := apiMethods[path]
	if !known && strings.HasPrefix(path, "/api/players/") {
		// /api/players/{id} is a real endpoint even though the ID is dynamic.
		if id := strings.TrimPrefix(path, "/api/players/"); !strings.Contains(id, "/") {
			if _, err := strconv.Atoi(id); err == nil {
				allowed, known = []string{"PUT", "DELETE"}, true
			}
		}
	}
	if known {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("%s is not allowed on %s", r.Method, path))
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("no such endpoint: %s %s", r.Method, r.URL.Path))
}

// ServeHTTP routes /api/... to the REST API and everything else to the
// frontend, keeping the two namespaces apart.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.api.ServeHTTP(w, r)
		return
	}
	if s.index != nil {
		s.index.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("frontend not mounted"))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// configResponse lets the frontend render the same rules the server applies
// instead of duplicating magic numbers in JavaScript.
type configResponse struct {
	Weights             model.Weights `json:"weights"`
	Coefficients        coefficients  `json:"coefficients"`
	TeamSizes           []int         `json:"teamSizes"`
	MinTeamSize         int           `json:"minTeamSize"`
	MaxTeamSize         int           `json:"maxTeamSize"`
	GoalkeeperThreshold float64       `json:"goalkeeperThreshold"`
	Rating              ratingBounds  `json:"rating"`
	ExhaustiveLimit     int           `json:"maxExhaustiveCombinations"`
}

type coefficients struct {
	WeightedScore   float64 `json:"weightedScore"`
	Attack          float64 `json:"attack"`
	Defense         float64 `json:"defense"`
	Goalkeeping     float64 `json:"goalkeeping"`
	Overall         float64 `json:"overall"`
	GoalkeeperCount float64 `json:"goalkeeperCount"`
	GoalkeeperGap   float64 `json:"goalkeeperGap"`
}

type ratingBounds struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	sizes := make([]int, 0, balancer.MaxTeamSize)
	for n := 5; n <= balancer.MaxTeamSize; n++ {
		sizes = append(sizes, n)
	}
	writeJSON(w, http.StatusOK, configResponse{
		Weights: model.DefaultWeights(),
		Coefficients: coefficients{
			WeightedScore:   balancer.WeightedScoreCoefficient,
			Attack:          balancer.AttackDiffCoefficient,
			Defense:         balancer.DefenseDiffCoefficient,
			Goalkeeping:     balancer.GoalkeepingDiffCoefficient,
			Overall:         balancer.OverallDiffCoefficient,
			GoalkeeperCount: balancer.GoalkeeperCountCoefficient,
			GoalkeeperGap:   balancer.GoalkeeperGapCoefficient,
		},
		TeamSizes:           sizes,
		MinTeamSize:         balancer.MinTeamSize,
		MaxTeamSize:         balancer.MaxTeamSize,
		GoalkeeperThreshold: model.GoalkeeperThreshold,
		Rating:              ratingBounds{Min: model.MinRating, Max: model.MaxRating},
		ExhaustiveLimit:     balancer.DefaultMaxExhaustiveCombinations,
	})
}

func (s *Server) handleListPlayers(w http.ResponseWriter, _ *http.Request) {
	players, err := s.repo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, players)
}

func (s *Server) handleCreatePlayer(w http.ResponseWriter, r *http.Request) {
	var p model.Player
	if err := readJSON(w, r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p.ID = 0 // the server owns IDs
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := s.repo.Add(p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdatePlayer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var p model.Player
	if err := readJSON(w, r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p.ID = id
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.repo.Update(p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeletePlayer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.repo.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// balanceRequest is the POST /api/balance payload.
type balanceRequest struct {
	TeamSize int            `json:"teamSize"`
	Players  []model.Player `json:"players"`
	Weights  *model.Weights `json:"weights,omitempty"`
}

func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	var req balanceRequest
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
	result, err := balancer.Split(req.Players, req.TeamSize, weights)
	if err != nil {
		// Every balancing failure is a client input problem: wrong player
		// count, bad rating, duplicate ID.
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func pathID(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.PathValue("id"))
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id must be a positive integer, got %q", raw)
	}
	return id, nil
}

// readJSON decodes at most maxBodyBytes of the request body and rejects
// anything after the first JSON value.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is empty")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("request body is empty")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return fmt.Errorf("request body is larger than %d bytes", maxBodyBytes)
		}
		return fmt.Errorf("invalid JSON body: %v", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body contains trailing data")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, storage.ErrInvalid):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"could not encode response"}`, http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}
