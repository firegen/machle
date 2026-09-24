package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"football-balancer/internal/model"
)

func openMatches(t *testing.T) (*MatchJSONStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "matches.json")
	store, err := NewMatchJSONStore(path)
	if err != nil {
		t.Fatalf("NewMatchJSONStore: %v", err)
	}
	return store, path
}

// drawnMatch builds a valid upcoming fixture from a roster of n players.
func drawnMatch(t *testing.T, n int, when time.Time) model.Match {
	t.Helper()
	roster := DefaultPlayers()[:n]
	m, err := model.NewMatch(roster[:n/2], roster[n/2:], model.DefaultWeights(), when)
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	return m
}

func TestNewMatchStoreStartsEmpty(t *testing.T) {
	store, path := openMatches(t)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the matches file should be created on first start: %v", err)
	}
	var onDisk []model.Match
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("created file is not valid JSON: %v", err)
	}
	if len(onDisk) != 0 {
		t.Errorf("new store holds %d matches, want none", len(onDisk))
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List = %d, want 0", len(list))
	}
}

func TestMatchStoreRoundTrip(t *testing.T) {
	store, path := openMatches(t)

	m := drawnMatch(t, 6, time.Now())
	created, err := store.Add(m)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.ID != 1 {
		t.Errorf("ID = %d, want 1", created.ID)
	}

	// Reopen from disk: what was saved must be exactly what is loaded.
	reopened, err := NewMatchJSONStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	list, err := reopened.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("reopened store holds %d matches, want 1", len(list))
	}
	// Match holds slices, so compare encodings rather than structs.
	want, _ := json.Marshal(created)
	gotRaw, _ := json.Marshal(list[0])
	if string(want) != string(gotRaw) {
		t.Errorf("round trip changed the match:\n got %s\nwant %s", gotRaw, want)
	}
	got, err := reopened.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TeamSize != 3 || len(got.TeamA.Players) != 3 || len(got.TeamB.Players) != 3 {
		t.Errorf("line-up did not survive the round trip: %+v", got)
	}
}

func TestMatchIDsAreNeverReused(t *testing.T) {
	store, _ := openMatches(t)

	first, err := store.Add(drawnMatch(t, 4, time.Now()))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	second, err := store.Add(drawnMatch(t, 4, time.Now()))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("deleted match id %d was handed out again", second.ID)
	}
}

func TestMatchStoreUpdateAndErrors(t *testing.T) {
	store, _ := openMatches(t)

	m, err := store.Add(drawnMatch(t, 4, time.Now()))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.SetResult(3, 2); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	updated, err := store.Update(m)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != model.MatchPlayed || *updated.TeamA.Goals != 3 {
		t.Errorf("update returned %+v", updated)
	}

	if _, err := store.Get(4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}
	missing := drawnMatch(t, 4, time.Now())
	missing.ID = 4242
	if _, err := store.Update(missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update unknown = %v, want ErrNotFound", err)
	}
	if err := store.Delete(4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete unknown = %v, want ErrNotFound", err)
	}

	// A structurally impossible match must be refused, and refuse cleanly.
	if _, err := store.Add(model.Match{Status: model.MatchRated, TeamSize: 2}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Add invalid match = %v, want ErrInvalid", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("store holds %d matches after a rejected add, want 1", len(list))
	}
}

func TestMatchStoreRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matches.json")

	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewMatchJSONStore(path); err == nil {
		t.Fatal("a corrupt history must refuse to load")
	}

	// Valid JSON, but the record claims "rated" with nothing behind it.
	bad := `[{"id":1,"date":"2026-05-01T10:00:00Z","status":"rated","teamSize":1,
	          "teamA":{"players":[{"id":1,"name":"A"}]},"teamB":{"players":[{"id":2,"name":"B"}]},
	          "ratings":[]}]`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewMatchJSONStore(path); err == nil {
		t.Fatal("an inconsistent stored match must be rejected")
	}
}

func TestMatchStoreAtomicWriteLeavesNoTempFiles(t *testing.T) {
	store, path := openMatches(t)
	dir := filepath.Dir(path)

	for i := 0; i < 5; i++ {
		if _, err := store.Add(drawnMatch(t, 4, time.Now().Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "matches.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp files left behind: %v", names)
	}
}

func TestMatchStoreConcurrentUse(t *testing.T) {
	store, path := openMatches(t)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.Add(drawnMatch(t, 4, time.Unix(1700000000+int64(i)*3600, 0))); err != nil {
				t.Errorf("Add: %v", err)
			}
			if _, err := store.List(); err != nil {
				t.Errorf("List: %v", err)
			}
		}(i)
	}
	wg.Wait()

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 12 {
		t.Errorf("store holds %d matches, want 12", len(list))
	}
	seen := map[int]bool{}
	for _, m := range list {
		if seen[m.ID] {
			t.Errorf("two matches share id %d", m.ID)
		}
		seen[m.ID] = true
	}

	reopened, err := NewMatchJSONStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if again, _ := reopened.List(); len(again) != len(list) {
		t.Errorf("disk holds %d matches, memory holds %d", len(again), len(list))
	}
}

// The match store must survive a roster that loses a participant: history is
// frozen at creation, so deleting a player cannot corrupt the file.
func TestMatchSurvivesRosterDeletion(t *testing.T) {
	players := DefaultPlayers()
	m, err := model.NewMatch(players[:3], players[3:6], model.DefaultWeights(), time.Now())
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if err := m.SetResult(2, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	dir := t.TempDir()
	matchStore, err := NewMatchJSONStore(filepath.Join(dir, "matches.json"))
	if err != nil {
		t.Fatalf("NewMatchJSONStore: %v", err)
	}
	saved, err := matchStore.Add(m)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	roster, err := NewJSONStore(filepath.Join(dir, "players.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	for _, p := range players[:6] {
		if err := roster.Delete(p.ID); err != nil {
			t.Fatalf("Delete %d: %v", p.ID, err)
		}
	}

	after, err := matchStore.Get(saved.ID)
	if err != nil {
		t.Fatalf("Get after roster deletions: %v", err)
	}
	if len(after.TeamA.Players) != 3 || after.TeamA.Players[0].Name != players[0].Name {
		t.Errorf("the frozen line-up was disturbed: %+v", after.TeamA.Players)
	}
	hist := model.HistoryOf([]model.Match{after}, players[0])
	if fmt.Sprint(hist.Summary.Matches, hist.Summary.Wins) != "1 1" {
		t.Errorf("history after deletions = %+v, want one win", hist.Summary)
	}
}
