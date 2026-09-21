package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"football-balancer/internal/model"
	"football-balancer/internal/storage"
)

type harness struct {
	ts   *httptest.Server
	path string
	dir  string
}

func newTestServer(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "players.json")
	store, err := storage.NewJSONStore(path)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	ts := httptest.NewServer(New(store, nil))
	t.Cleanup(ts.Close)
	return &harness{ts: ts, path: path, dir: dir}
}

func (h *harness) url(path string) string { return h.ts.URL + path }

func (h *harness) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.url(path), jsonReader(t, body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// doRaw sends an unprocessed body, for malformed-payload tests.
func (h *harness) doRaw(t *testing.T, method, path, raw string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.url(path), bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func jsonReader(t *testing.T, body any) io.Reader {
	t.Helper()
	if body == nil {
		return nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(data)
}

func decodePlayers(t *testing.T, res *http.Response) []model.Player {
	t.Helper()
	var out []model.Player
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode player list: %v", err)
	}
	return out
}

func decodePlayer(t *testing.T, res *http.Response) model.Player {
	t.Helper()
	var out model.Player
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode player: %v", err)
	}
	return out
}

func (h *harness) roster(t *testing.T) []model.Player {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/players", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/players = %d, want 200", res.StatusCode)
	}
	return decodePlayers(t, res)
}

func TestPlayerCRUDRoundTrip(t *testing.T) {
	h := newTestServer(t)

	seeded := h.roster(t)
	if len(seeded) != 20 {
		t.Fatalf("roster has %d players, want the 20 examples", len(seeded))
	}
	if seeded[0].Name != "Ivan" || seeded[19].Name != "Todor" {
		t.Errorf("roster order changed: first=%q last=%q", seeded[0].Name, seeded[19].Name)
	}

	res := h.do(t, http.MethodPost, "/api/players", model.Player{
		Name: "Newcomer", Attack: 7.5, Defense: 6.5, Goalkeeping: 2, Overall: 7.1,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d, want 201", res.StatusCode)
	}
	created := decodePlayer(t, res)
	if created.ID != 21 {
		t.Errorf("assigned id = %d, want 21", created.ID)
	}

	// Read back from disk: this proves persistence, not just in-memory state.
	raw, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("read roster file: %v", err)
	}
	if !bytes.Contains(raw, []byte("Newcomer")) {
		t.Error("the new player was not written to the JSON file")
	}
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatalf("list data dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "players.json" {
		t.Errorf("atomic write left debris behind: %v", names(entries))
	}

	created.Name = "Renamed"
	created.Overall = 8.4
	res = h.do(t, http.MethodPut, "/api/players/21", created)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d, want 200", res.StatusCode)
	}
	if got := decodePlayer(t, res); got.Name != "Renamed" || got.Overall != 8.4 {
		t.Errorf("update returned %+v", got)
	}

	res = h.do(t, http.MethodDelete, "/api/players/21", nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", res.StatusCode)
	}
	if left := h.roster(t); len(left) != 20 {
		t.Errorf("roster holds %d players after delete, want 20", len(left))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestPlayerValidationAndNotFound(t *testing.T) {
	h := newTestServer(t)

	cases := []struct {
		label    string
		method   string
		path     string
		body     any
		wantCode int
	}{
		{"rating above 10", http.MethodPost, "/api/players",
			model.Player{Name: "Overclocked", Attack: 11, Defense: 5, Goalkeeping: 1, Overall: 6}, http.StatusBadRequest},
		{"rating below 1", http.MethodPost, "/api/players",
			model.Player{Name: "Underdog", Attack: 5, Defense: 0.4, Goalkeeping: 1, Overall: 6}, http.StatusBadRequest},
		{"blank name", http.MethodPost, "/api/players",
			model.Player{Name: "   ", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 6}, http.StatusBadRequest},
		{"unknown id", http.MethodPut, "/api/players/9999",
			model.Player{Name: "Ghost", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 6}, http.StatusNotFound},
		{"non-numeric id", http.MethodDelete, "/api/players/not-a-number", nil, http.StatusBadRequest},
		{"delete unknown id", http.MethodDelete, "/api/players/4242", nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			res := h.do(t, tc.method, tc.path, tc.body)
			if res.StatusCode != tc.wantCode {
				t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, res.StatusCode, tc.wantCode)
			}
		})
	}

	res := h.doRaw(t, http.MethodPost, "/api/players", "{oops")
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", res.StatusCode)
	}
	res = h.doRaw(t, http.MethodPost, "/api/players", "")
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", res.StatusCode)
	}
	res = h.doRaw(t, http.MethodPost, "/api/players", `{"name":"x","attack":5,"defense":5,"goalkeeping":1,"overall":6} {"name":"y"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("trailing data = %d, want 400", res.StatusCode)
	}
}

func TestBalanceEndpoint(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)
	picked := roster[:12]

	res := h.do(t, http.MethodPost, "/api/balance", map[string]any{
		"teamSize": 6,
		"players":  picked,
		"weights":  map[string]float64{"attack": 30, "defense": 30, "goalkeeping": 10, "overall": 30},
	})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("POST /api/balance = %d: %s", res.StatusCode, body)
	}
	var out model.BalanceResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	if len(out.TeamA.Players) != 6 || len(out.TeamB.Players) != 6 {
		t.Fatalf("team sizes %d/%d, want 6/6", len(out.TeamA.Players), len(out.TeamB.Players))
	}
	seen := map[int]bool{}
	for _, pl := range append(append([]model.Player{}, out.TeamA.Players...), out.TeamB.Players...) {
		if seen[pl.ID] {
			t.Errorf("player %d landed in both teams", pl.ID)
		}
		seen[pl.ID] = true
	}
	for _, want := range picked {
		if !seen[want.ID] {
			t.Errorf("selected player %d (%s) is missing from the output", want.ID, want.Name)
		}
	}
	if out.Balance < 95 {
		t.Errorf("balance = %.1f%% on the example roster, expected >= 95%%", out.Balance)
	}
	if out.Method != "exhaustive" {
		t.Errorf("method = %q, want exhaustive for 12 players", out.Method)
	}
	if out.Explored != 462 {
		t.Errorf("explored = %d, want 462", out.Explored)
	}
	if want := (model.Weights{Attack: 30, Defense: 30, Goalkeeping: 10, Overall: 30}); out.Weights != want {
		t.Errorf("echoed weights = %+v", out.Weights)
	}
	t.Logf("6v6 example → A %v | B %v | balance %.1f%% diff %.2f obj %.4f keepers %d/%d",
		playerNames(out.TeamA.Players), playerNames(out.TeamB.Players), out.Balance, out.Difference, out.Objective,
		out.TeamA.Goalkeepers, out.TeamB.Goalkeepers)

	// 8 keeper-capable players exist in the first 16; a goalkeeper-heavy
	// profile must give every side someone who can keep goal.
	res = h.do(t, http.MethodPost, "/api/balance", map[string]any{
		"teamSize": 8,
		"players":  roster[:16],
		"weights":  map[string]float64{"attack": 10, "defense": 10, "goalkeeping": 60, "overall": 20},
	})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("goalkeeper-heavy balance = %d: %s", res.StatusCode, body)
	}
	var gk model.BalanceResult
	if err := json.NewDecoder(res.Body).Decode(&gk); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if gk.TeamA.Goalkeepers == 0 || gk.TeamB.Goalkeepers == 0 {
		t.Errorf("a team got no keeper-capable player: %d vs %d", gk.TeamA.Goalkeepers, gk.TeamB.Goalkeepers)
	}
	if want := (model.Weights{Attack: 10, Defense: 10, Goalkeeping: 60, Overall: 20}); gk.Weights != want {
		t.Errorf("echoed weights = %+v", gk.Weights)
	}
}

func TestBalanceOmittedWeightsUseTheDefaults(t *testing.T) {
	h := newTestServer(t)
	res := h.do(t, http.MethodPost, "/api/balance", map[string]any{
		"teamSize": 5,
		"players":  h.roster(t)[:10],
	})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("balance = %d: %s", res.StatusCode, body)
	}
	var out model.BalanceResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := model.DefaultWeights(); out.Weights != want {
		t.Errorf("weights = %+v, want the %+v default", out.Weights, want)
	}
}

func TestBalanceRejectsBadRequests(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"wrong count", map[string]any{"teamSize": 6, "players": roster[:11]}},
		{"empty pool", map[string]any{"teamSize": 6, "players": []model.Player{}}},
		{"team size too small", map[string]any{"teamSize": 1, "players": roster[:2]}},
		{"all weights zero", map[string]any{"teamSize": 5, "players": roster[:10],
			"weights": map[string]float64{"attack": 0, "defense": 0, "goalkeeping": 0, "overall": 0}}},
		{"negative weight", map[string]any{"teamSize": 5, "players": roster[:10],
			"weights": map[string]float64{"attack": -30, "defense": 130}}},
		{"duplicate players", map[string]any{"teamSize": 5, "players": append(append([]model.Player{}, roster[:9]...), roster[0])}},
		{"invalid rating in pool", map[string]any{"teamSize": 5, "players": func() []model.Player {
			bad := append([]model.Player{}, roster[:10]...)
			bad[3].Goalkeeping = 12
			return bad
		}()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, "/api/balance", tc.payload)
			if res.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(res.Body)
				t.Fatalf("status = %d, want 400 (%s)", res.StatusCode, body)
			}
			var body map[string]string
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["error"] == "" {
				t.Error("error responses must carry a human-readable message")
			}
			t.Logf("%s → %s", tc.name, body["error"])
		})
	}
}

func TestConfigAndHealth(t *testing.T) {
	h := newTestServer(t)

	res := h.do(t, http.MethodGet, "/api/config", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/config = %d", res.StatusCode)
	}
	var cfg configResponse
	if err := json.NewDecoder(res.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if want := []int{5, 6, 7, 8, 9, 10}; !equalInts(cfg.TeamSizes, want) {
		t.Errorf("teamSizes = %v, want %v", cfg.TeamSizes, want)
	}
	if cfg.Coefficients.Attack != 0.12 || cfg.Coefficients.Goalkeeping != 0.08 {
		t.Errorf("coefficients drifted: %+v", cfg.Coefficients)
	}
	if cfg.GoalkeeperThreshold != model.GoalkeeperThreshold {
		t.Errorf("goalkeeperThreshold = %v", cfg.GoalkeeperThreshold)
	}
	if cfg.Rating.Min != 1 || cfg.Rating.Max != 10 {
		t.Errorf("rating bounds = %+v", cfg.Rating)
	}
	if want := model.DefaultWeights(); cfg.Weights != want {
		t.Errorf("default weights = %+v, want %+v", cfg.Weights, want)
	}

	res = h.do(t, http.MethodGet, "/api/health", nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("health = %d", res.StatusCode)
	}

	// Unknown API routes must answer with JSON, never with the frontend.
	res = h.do(t, http.MethodGet, "/api/nope", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown API route = %d, want 404", res.StatusCode)
	}
	if body := readAll(t, res); !bytes.Contains(body, []byte("no such endpoint")) {
		t.Errorf("unknown API route body = %s, want a JSON error", body)
	}
}

func TestRoutingErrors(t *testing.T) {
	h := newTestServer(t)

	cases := []struct {
		label      string
		method     string
		path       string
		wantCode   int
		wantAllow  string
		wantInBody string
	}{
		{"unknown endpoint", http.MethodGet, "/api/nope", http.StatusNotFound, "", "no such endpoint"},
		{"unknown nested endpoint", http.MethodGet, "/api/players/1/extra", http.StatusNotFound, "", "no such endpoint"},
		{"wrong verb on collection", http.MethodPatch, "/api/players", http.StatusMethodNotAllowed, "GET, POST", "not allowed"},
		{"wrong verb on config", http.MethodDelete, "/api/config", http.StatusMethodNotAllowed, "GET", "not allowed"},
		{"wrong verb on balance", http.MethodGet, "/api/balance", http.StatusMethodNotAllowed, "POST", "not allowed"},
		{"wrong verb on a player", http.MethodPatch, "/api/players/5", http.StatusMethodNotAllowed, "PUT, DELETE", "not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			res := h.do(t, tc.method, tc.path, nil)
			if res.StatusCode != tc.wantCode {
				t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, res.StatusCode, tc.wantCode)
			}
			if got := res.Header.Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
			if !strings.Contains(string(readAll(t, res)), tc.wantInBody) {
				t.Errorf("body should mention %q", tc.wantInBody)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
		})
	}
}

func readAll(t *testing.T, res *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func TestServesFrontend(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"index.html": "<!doctype html><title>Football Team Balancer</title>",
		"app.js":     "console.log('balancer');",
		"style.css":  "body{color:#fff}",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	store, err := storage.NewJSONStore(filepath.Join(dir, "players.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	ts := httptest.NewServer(New(store, http.Dir(dir)))
	t.Cleanup(ts.Close)

	for path, want := range map[string]string{
		"/":          "Football Team Balancer",
		"/app.js":    "console.log",
		"/style.css": "body{color",
	} {
		res, err := http.Get(ts.URL + path) //nolint:noctx // test client
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("GET %s did not serve %q", path, want)
		}
	}

	res, err := http.Get(ts.URL + "/missing.css") //nolint:noctx // test client
	if err != nil {
		t.Fatalf("GET /missing.css: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset = %d, want 404", res.StatusCode)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func playerNames(players []model.Player) []string {
	out := make([]string, 0, len(players))
	for _, p := range players {
		out = append(out, p.Name)
	}
	return out
}
