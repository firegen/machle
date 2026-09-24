package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"football-balancer/internal/model"
	"football-balancer/internal/storage"
)

// createMatch posts a drawn fixture and returns the decoded answer plus the
// status code, which distinguishes a new match (201) from a reused one (200).
func createMatch(t *testing.T, h *harness, teamA, teamB []int, weights map[string]float64) (*http.Response, model.Match) {
	t.Helper()
	payload := map[string]any{"teamA": teamA, "teamB": teamB}
	if weights != nil {
		payload["weights"] = weights
	}
	res := h.do(t, http.MethodPost, "/api/matches", payload)
	var m model.Match
	body := readAll(t, res)
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode match (%s): %v", body, err)
	}
	return res, m
}

func listMatches(t *testing.T, h *harness) []model.Match {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/matches", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/matches = %d", res.StatusCode)
	}
	var out []model.Match
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode match list: %v", err)
	}
	return out
}

func TestMatchLifecycleOverHTTP(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)
	ids := func(ps []model.Player) []int {
		out := make([]int, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	teamA, teamB := ids(roster[:6]), ids(roster[6:12])
	weights := map[string]float64{"attack": 30, "defense": 30, "goalkeeping": 10, "overall": 30}

	// 1. Creating a match snapshots the line-up from the roster.
	res, created := createMatch(t, h, teamA, teamB, weights)
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("POST /api/matches = %d: %s", res.StatusCode, body)
	}
	if created.ID != 1 {
		t.Errorf("id = %d, want 1", created.ID)
	}
	if created.Status != model.MatchUpcoming {
		t.Errorf("status = %q, want %q", created.Status, model.MatchUpcoming)
	}
	if created.TeamSize != 6 || len(created.TeamA.Players) != 6 || len(created.TeamB.Players) != 6 {
		t.Fatalf("line-up = %+v / %+v", created.TeamA.Players, created.TeamB.Players)
	}
	if created.TeamA.Players[0].Name != "Ivan" {
		t.Errorf("team A snapshot = %+v, want the roster name Ivan", created.TeamA.Players[0])
	}
	if created.TeamA.Goals != nil || len(created.Ratings) != 0 {
		t.Error("a new match must have no score and no ratings")
	}
	if created.Balance <= 0 || created.Balance > 100 {
		t.Errorf("balance = %v", created.Balance)
	}
	if want := (model.Weights{Attack: 30, Defense: 30, Goalkeeping: 10, Overall: 30}); created.Weights != want {
		t.Errorf("weights = %+v", created.Weights)
	}

	// 2. Drawing the same teams again reuses the unfinished match.
	res, again := createMatch(t, h, teamB, teamA, weights) // sides swapped
	if res.StatusCode != http.StatusOK {
		t.Errorf("re-drawing the same fixture = %d, want 200 (reused)", res.StatusCode)
	}
	if again.ID != created.ID {
		t.Errorf("reused match id = %d, want %d", again.ID, created.ID)
	}
	if got := listMatches(t, h); len(got) != 1 {
		t.Fatalf("history holds %d matches, want the duplicate to be skipped", len(got))
	}

	// A different split of the same people is a different match.
	tweakedA := append([]int{}, teamA...)
	tweakedB := append([]int{}, teamB...)
	tweakedA[0], tweakedB[0] = tweakedB[0], tweakedA[0]
	res, split := createMatch(t, h, tweakedA, tweakedB, weights)
	if res.StatusCode != http.StatusCreated {
		t.Errorf("re-split = %d, want 201: a re-split is a new fixture", res.StatusCode)
	}
	if len(listMatches(t, h)) != 2 {
		t.Fatalf("history should now hold 2 matches")
	}

	// 3. Set the result on the first match.
	goalsA, goalsB := 5, 3
	res = h.do(t, http.MethodPut, urlMatch(created.ID), map[string]any{"goalsA": goalsA, "goalsB": goalsB})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("PUT result = %d: %s", res.StatusCode, body)
	}
	played := decodeMatch(t, res)
	if played.Status != model.MatchPlayed {
		t.Errorf("status = %q, want %q", played.Status, model.MatchPlayed)
	}
	if played.TeamA.Goals == nil || *played.TeamA.Goals != 5 {
		t.Errorf("goals = %v, want 5", played.TeamA.Goals)
	}

	// 4. Rate everyone → the status advances to rated on its own.
	ratings := make([]map[string]any, 0, 12)
	for i, id := range append(append([]int{}, teamA...), teamB...) {
		ratings = append(ratings, map[string]any{"playerId": id, "rating": 5 + float64(i%5)})
	}
	motm := teamA[0]
	res = h.do(t, http.MethodPut, urlMatch(created.ID), map[string]any{
		"ratings": ratings, "manOfTheMatch": motm,
		"date": "2026-09-20T17:30:00Z",
	})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("PUT ratings = %d: %s", res.StatusCode, body)
	}
	rated := decodeMatch(t, res)
	if rated.Status != model.MatchRated {
		t.Errorf("status = %q, want %q once everyone is rated", rated.Status, model.MatchRated)
	}
	if len(rated.Ratings) != 12 {
		t.Errorf("ratings = %d, want 12", len(rated.Ratings))
	}
	if rated.ManOfTheMatch == nil || *rated.ManOfTheMatch != motm {
		t.Errorf("manOfTheMatch = %v, want %d", rated.ManOfTheMatch, motm)
	}
	if !rated.Date.Equal(time.Date(2026, 9, 20, 17, 30, 0, 0, time.UTC)) {
		t.Errorf("date = %v, want the submitted time", rated.Date)
	}

	// 5. The finished match is no longer a duplicate target: drawing the same
	// teams again starts a new fixture rather than resurrecting the old one.
	if res, _ := createMatch(t, h, teamA, teamB, weights); res.StatusCode != http.StatusCreated {
		t.Errorf("re-draw after finishing = %d, want 201", res.StatusCode)
	}

	// 6. Player history reflects the finished match.
	res = h.do(t, http.MethodGet, playerURL(motm)+"/history", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET history = %d", res.StatusCode)
	}
	var hist model.PlayerHistory
	if err := json.NewDecoder(res.Body).Decode(&hist); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if hist.Name != "Ivan" {
		t.Errorf("name = %q, want Ivan", hist.Name)
	}
	if hist.Summary.Matches != 1 || hist.Summary.Wins != 1 || hist.Summary.ManOfTheMatch != 1 {
		t.Errorf("summary = %+v, want one win and one award", hist.Summary)
	}
	if hist.Summary.Rated != 1 || hist.Summary.AverageRating == 0 {
		t.Errorf("summary = %+v, want one rating", hist.Summary)
	}
	if len(hist.Appearances) != 1 || hist.Appearances[0].Team != "A" {
		t.Fatalf("appearances = %+v", hist.Appearances)
	}
	if got := *hist.Appearances[0].GoalsFor; got != 5 {
		t.Errorf("goalsFor = %d, want 5", got)
	}

	// 7. Delete the second fixture.
	if res := h.do(t, http.MethodDelete, urlMatch(split.ID), nil); res.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", res.StatusCode)
	}
	if got := listMatches(t, h); len(got) != 2 {
		t.Errorf("history holds %d matches after delete, want 2", len(got))
	}

	// 8. Persistence: a fresh server over the same file sees the history.
	reloaded := newServerOverDir(t, h.dir)
	if got := listMatches(t, reloaded); len(got) != 2 {
		t.Errorf("reopened store holds %d matches, want 2", len(got))
	}
	if got := listMatches(t, reloaded); len(got) > 0 && got[0].Date.Before(got[len(got)-1].Date) {
		t.Error("the list must come back newest first")
	}
}

func urlMatch(id int) string { return "/api/matches/" + strconv.Itoa(id) }

func playerURL(id int) string { return "/api/players/" + strconv.Itoa(id) }

// newServerOverDir starts a second server over the same data files, proving the
// history is on disk rather than only in memory.
func newServerOverDir(t *testing.T, dir string) *harness {
	t.Helper()
	store, err := storage.NewJSONStore(filepath.Join(dir, "players.json"))
	if err != nil {
		t.Fatalf("reopen roster: %v", err)
	}
	matchStore, err := storage.NewMatchJSONStore(filepath.Join(dir, "matches.json"))
	if err != nil {
		t.Fatalf("reopen history: %v", err)
	}
	ts := httptest.NewServer(New(store, matchStore, nil))
	t.Cleanup(ts.Close)
	return &harness{ts: ts, path: filepath.Join(dir, "players.json"),
		matchPath: filepath.Join(dir, "matches.json"), dir: dir}
}

func decodeMatch(t *testing.T, res *http.Response) model.Match {
	t.Helper()
	var m model.Match
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatalf("decode match: %v", err)
	}
	return m
}

func TestMatchRequestValidation(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)
	ids := func(ps []model.Player) []int {
		out := make([]int, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	good := ids(roster[:6])

	cases := []struct {
		name    string
		payload map[string]any
		mention string
	}{
		{"unknown player", map[string]any{"teamA": []int{1, 2, 999}, "teamB": ids(roster[6:9])}, "not in the roster"},
		{"unequal teams", map[string]any{"teamA": good, "teamB": ids(roster[6:9])}, "same size"},
		{"duplicate inside a team", map[string]any{"teamA": []int{1, 1, 2}, "teamB": ids(roster[6:9])}, "twice"},
		{"empty team", map[string]any{"teamA": []int{}, "teamB": ids(roster[6:9])}, "no players"},
		{"player in both teams", map[string]any{"teamA": good, "teamB": []int{1, 7, 8, 9, 10, 11}}, "listed twice"},
		{"negative weight", map[string]any{"teamA": good, "teamB": ids(roster[6:9]),
			"weights": map[string]float64{"attack": -5, "defense": 5}}, "negative"},
		{"all weights zero", map[string]any{"teamA": good, "teamB": ids(roster[6:9]),
			"weights": map[string]float64{"attack": 0, "defense": 0}}, "zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, "/api/matches", tc.payload)
			if res.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(res.Body)
				t.Fatalf("status = %d, want 400 (%s)", res.StatusCode, body)
			}
			if msg := errorText(t, res); !strings.Contains(msg, tc.mention) {
				t.Errorf("error %q should mention %q", msg, tc.mention)
			}
		})
	}
	if got := listMatches(t, h); len(got) != 0 {
		t.Errorf("rejected requests must not store anything, found %d matches", len(got))
	}
}

func TestMatchResultValidation(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)
	a := make([]int, 0, 3)
	b := make([]int, 0, 3)
	for _, p := range roster[:3] {
		a = append(a, p.ID)
	}
	for _, p := range roster[3:6] {
		b = append(b, p.ID)
	}
	res, created := createMatch(t, h, a, b, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	id := urlMatch(created.ID)

	cases := []struct {
		name    string
		payload map[string]any
		mention string
	}{
		{"half a score", map[string]any{"goalsA": 2}, "both teams"},
		{"negative goals", map[string]any{"goalsA": -1, "goalsB": 2}, "negative"},
		{"rating an outsider", map[string]any{"goalsA": 1, "goalsB": 1,
			"ratings": []map[string]any{{"playerId": 999, "rating": 8}}}, "not in this match"},
		{"rating out of range", map[string]any{"goalsA": 1, "goalsB": 1,
			"ratings": []map[string]any{{"playerId": a[0], "rating": 12}}}, "between"},
		{"claim rated too early", map[string]any{"goalsA": 1, "goalsB": 1, "status": "rated"}, "no rating"},
		{"unknown status", map[string]any{"status": "final"}, "unknown match status"},
		{"award before a result", map[string]any{"manOfTheMatch": a[0]}, "unplayed"},
		{"award to an outsider", map[string]any{"goalsA": 1, "goalsB": 0, "manOfTheMatch": 999}, "one of the"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPut, id, tc.payload)
			if res.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(res.Body)
				t.Fatalf("status = %d, want 400 (%s)", res.StatusCode, body)
			}
			if msg := errorText(t, res); !strings.Contains(msg, tc.mention) {
				t.Errorf("error %q should mention %q", msg, tc.mention)
			}
		})
	}

	// Every rejection must have left the stored match untouched.
	before := decodeMatch(t, h.do(t, http.MethodGet, id, nil))
	if before.Status != model.MatchUpcoming || before.TeamA.Goals != nil || len(before.Ratings) != 0 {
		t.Errorf("rejected updates changed the stored match: %+v", before)
	}

	// A valid result sticks, and a later full ratings set advances the status.
	if res := h.do(t, http.MethodPut, id, map[string]any{"goalsA": 2, "goalsB": 2}); res.StatusCode != http.StatusOK {
		t.Fatalf("valid result rejected")
	}
	if got := decodeMatch(t, h.do(t, http.MethodGet, id, nil)); got.Status != model.MatchPlayed {
		t.Errorf("status = %q, want played", got.Status)
	}

	if res := h.do(t, http.MethodPut, urlMatch(4242), map[string]any{"goalsA": 1, "goalsB": 1}); res.StatusCode != http.StatusNotFound {
		t.Errorf("PUT unknown match = %d, want 404", res.StatusCode)
	}
	if res := h.do(t, http.MethodDelete, urlMatch(4242), nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE unknown match = %d, want 404", res.StatusCode)
	}
	if res := h.do(t, http.MethodGet, "/api/players/4242/history", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("history of unknown player = %d, want 404", res.StatusCode)
	}
}

func TestMatchRoutingAndPlayerIndependence(t *testing.T) {
	h := newTestServer(t)

	// With no matches stored, the collection is an empty array, not null.
	res := h.do(t, http.MethodGet, "/api/matches", nil)
	body := readAll(t, res)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty history = %s, want []", body)
	}

	// Wrong verbs on the new endpoints still answer 405 + Allow + JSON.
	for _, tc := range []struct{ method, path, want string }{
		{http.MethodDelete, "/api/matches", "GET, POST"},
		{http.MethodPatch, "/api/matches/1", "GET, PUT, DELETE"},
		{http.MethodPost, "/api/players/1/history", "GET"},
	} {
		res := h.do(t, tc.method, tc.path, nil)
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.path, res.StatusCode)
		}
		if got := res.Header.Get("Allow"); got != tc.want {
			t.Errorf("%s %s Allow = %q, want %q", tc.method, tc.path, got, tc.want)
		}
		if msg := errorText(t, res); !strings.Contains(msg, "not allowed") {
			t.Errorf("body = %q", msg)
		}
	}

	// Deleting a player must not damage the match history or the roster files.
	roster := h.roster(t)
	a, b := []int{roster[0].ID, roster[1].ID}, []int{roster[2].ID, roster[3].ID}
	res, created := createMatch(t, h, a, b, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	if res := h.do(t, http.MethodDelete, playerURL(roster[0].ID), nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete player = %d", res.StatusCode)
	}
	snapshot := decodeMatch(t, h.do(t, http.MethodGet, urlMatch(created.ID), nil))
	if snapshot.TeamA.Players[0].Name != roster[0].Name || snapshot.TeamA.Players[0].ID != roster[0].ID {
		t.Errorf("the frozen line-up was rewritten by the roster edit: %+v", snapshot.TeamA.Players)
	}
	if len(listMatches(t, h)) != 1 {
		t.Error("deleting a player must not delete their matches")
	}
}

func TestMatchRedrawOverHTTP(t *testing.T) {
	h := newTestServer(t)
	roster := h.roster(t)
	a := []int{roster[0].ID, roster[1].ID, roster[2].ID}
	b := []int{roster[3].ID, roster[4].ID, roster[5].ID}

	res, created := createMatch(t, h, a, b, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	id := urlMatch(created.ID)

	// Hand-tweaking the pending teams updates the same fixture in place.
	tweakedA := []int{a[0], a[1], b[0]}
	tweakedB := []int{b[2], a[2], b[1]}
	res = h.do(t, http.MethodPut, id, map[string]any{"teamA": tweakedA, "teamB": tweakedB})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("re-draw = %d: %s", res.StatusCode, body)
	}
	redrawn := decodeMatch(t, res)
	if redrawn.ID != created.ID {
		t.Errorf("id = %d, want the pending match to be updated in place", redrawn.ID)
	}
	if !redrawn.SameLineup(tweakedA, tweakedB) {
		t.Errorf("line-up = %v / %v, want the tweaked draw",
			idsOfMatch(redrawn.TeamA), idsOfMatch(redrawn.TeamB))
	}
	if redrawn.Status != model.MatchUpcoming {
		t.Errorf("status = %q, want upcoming", redrawn.Status)
	}
	if len(listMatches(t, h)) != 1 {
		t.Error("a re-draw must not add a second match")
	}

	// The frozen profile is the one it was drawn with, not the default.
	res, weighted := createMatch(t, h, []int{roster[6].ID, roster[7].ID}, []int{roster[8].ID, roster[9].ID},
		map[string]float64{"attack": 100})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("second create = %d", res.StatusCode)
	}
	if want := (model.Weights{Attack: 100}); weighted.Weights != want {
		t.Errorf("weights = %+v, want %+v", weighted.Weights, want)
	}
	res = h.do(t, http.MethodPut, urlMatch(weighted.ID), map[string]any{
		"teamA": []int{roster[6].ID, roster[8].ID}, "teamB": []int{roster[9].ID, roster[7].ID},
	})
	kept := decodeMatch(t, res)
	if want := (model.Weights{Attack: 100}); kept.Weights != want {
		t.Errorf("re-draw changed the stored profile to %+v", kept.Weights)
	}

	cases := []struct {
		name    string
		payload map[string]any
		mention string
	}{
		{"only one side", map[string]any{"teamA": a}, "needs both"},
		{"unknown player", map[string]any{"teamA": []int{1, 2, 999}, "teamB": b}, "not in the roster"},
		{"unequal teams", map[string]any{"teamA": a, "teamB": []int{roster[3].ID}}, "team size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPut, id, tc.payload)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.StatusCode)
			}
			if msg := errorText(t, res); !strings.Contains(msg, tc.mention) {
				t.Errorf("error %q should mention %q", msg, tc.mention)
			}
			if after := decodeMatch(t, h.do(t, http.MethodGet, id, nil)); !after.SameLineup(tweakedA, tweakedB) {
				t.Error("a rejected re-draw changed the stored line-up")
			}
		})
	}

	// A finished match is history: its composition can no longer move.
	if res := h.do(t, http.MethodPut, id, map[string]any{"goalsA": 3, "goalsB": 1}); res.StatusCode != http.StatusOK {
		t.Fatalf("set result = %d", res.StatusCode)
	}
	res = h.do(t, http.MethodPut, id, map[string]any{"teamA": a, "teamB": b})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("re-draw of a played match = %d, want 400", res.StatusCode)
	}
	if msg := errorText(t, res); !strings.Contains(msg, "without a result") {
		t.Errorf("error = %q", msg)
	}

	// Clearing the result reopens it, and the line-up can move again.
	res = h.do(t, http.MethodPut, id, map[string]any{"clearResult": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear = %d", res.StatusCode)
	}
	cleared := decodeMatch(t, res)
	if cleared.Status != model.MatchUpcoming || cleared.TeamA.Goals != nil || cleared.TeamB.Goals != nil {
		t.Errorf("after clearing: %+v", cleared)
	}
	if res := h.do(t, http.MethodPut, id, map[string]any{"teamA": a, "teamB": b}); res.StatusCode != http.StatusOK {
		t.Errorf("re-draw after clearing = %d, want 200", res.StatusCode)
	}
}

func idsOfMatch(team model.MatchTeam) []int {
	out := make([]int, 0, len(team.Players))
	for _, p := range team.Players {
		out = append(out, p.ID)
	}
	return out
}

func errorText(t *testing.T, res *http.Response) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body["error"]
}
