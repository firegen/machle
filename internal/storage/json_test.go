package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"football-balancer/internal/model"
)

func open(t *testing.T) (*JSONStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "players.json")
	store, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	return store, path
}

func TestNewStoreSeedsExampleRoster(t *testing.T) {
	store, path := open(t)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("roster file was not created: %v", err)
	}
	var onDisk []model.Player
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("seeded roster is not valid JSON: %v", err)
	}
	if len(onDisk) != 20 {
		t.Errorf("seeded %d players, want 20", len(onDisk))
	}

	inMemory, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inMemory) != len(onDisk) {
		t.Fatalf("memory holds %d players, disk holds %d", len(inMemory), len(onDisk))
	}
	for i := range onDisk {
		if inMemory[i] != onDisk[i] {
			t.Errorf("player %d differs: memory %+v disk %+v", i, inMemory[i], onDisk[i])
		}
	}
}

func TestShippedDataFileIsValidRoster(t *testing.T) {
	// data/players.json is a deliverable; make sure it still loads and matches
	// the built-in seed so the two cannot drift apart silently.
	const repoFile = "../../data/players.json"
	raw, err := os.ReadFile(repoFile)
	if err != nil {
		t.Fatalf("read %s: %v", repoFile, err)
	}
	var shipped []model.Player
	if err := json.Unmarshal(raw, &shipped); err != nil {
		t.Fatalf("parse %s: %v", repoFile, err)
	}
	seed := DefaultPlayers()
	if len(shipped) != len(seed) {
		t.Fatalf("%s holds %d players, the seed holds %d", repoFile, len(shipped), len(seed))
	}
	for i := range seed {
		if shipped[i] != seed[i] {
			t.Errorf("%s player %d drifted from the seed: %+v vs %+v", repoFile, i+1, shipped[i], seed[i])
		}
	}
}

func TestStoreReloadsFromDisk(t *testing.T) {
	store, path := open(t)
	if _, err := store.Add(model.Player{Name: "Persisted", Attack: 8, Defense: 7, Goalkeeping: 1, Overall: 7.5}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	reopened, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	list, err := reopened.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 21 {
		t.Fatalf("reopened roster has %d players, want 21", len(list))
	}
	if list[20].Name != "Persisted" {
		t.Errorf("last player is %q, want Persisted", list[20].Name)
	}
	if list[20].ID != 21 {
		t.Errorf("ID = %d, want 21", list[20].ID)
	}
}

func TestIDsAreNeverReused(t *testing.T) {
	store, _ := open(t)
	first, err := store.Add(model.Player{Name: "Temp", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	second, err := store.Add(model.Player{Name: "Next", Attack: 6, Defense: 6, Goalkeeping: 1, Overall: 6})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("deleted id %d was handed out again", second.ID)
	}
}

func TestListReturnsACopy(t *testing.T) {
	store, _ := open(t)
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list[0].Name = "Mutated"
	again, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if again[0].Name == "Mutated" {
		t.Error("List exposed internal state to mutation")
	}
}

func TestGetUpdateDelete(t *testing.T) {
	store, _ := open(t)

	got, err := store.Get(4)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Dimitar" {
		t.Fatalf("Get(4) = %q, want Dimitar", got.Name)
	}

	got.Overall = 9.9
	saved, err := store.Update(got)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if saved.Overall != 9.9 {
		t.Errorf("Overall = %v, want 9.9", saved.Overall)
	}

	if err := store.Delete(4); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(4); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(4); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestRejectsInvalidPlayers(t *testing.T) {
	store, _ := open(t)

	if _, err := store.Add(model.Player{Name: "Bad", Attack: 0, Defense: 5, Goalkeeping: 1, Overall: 5}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Add with rating 0 = %v, want ErrInvalid", err)
	}
	if _, err := store.Add(model.Player{Name: "", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Add with blank name = %v, want ErrInvalid", err)
	}
	if _, err := store.Update(model.Player{ID: 1, Name: "Bad", Attack: 5, Defense: 5, Goalkeeping: 99, Overall: 5}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Update with rating 99 = %v, want ErrInvalid", err)
	}

	// A rejected write must leave the roster untouched.
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 20 {
		t.Errorf("roster holds %d players after rejected writes, want 20", len(list))
	}
	if list[3].Goalkeeping != 8 {
		t.Errorf("rejected update was applied: %+v", list[3])
	}
}

func TestNamesAreTrimmed(t *testing.T) {
	store, _ := open(t)
	created, err := store.Add(model.Player{Name: "  Spacey  ", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.Name != "Spacey" {
		t.Errorf("name = %q, want %q", created.Name, "Spacey")
	}
}

func TestBadIDsOnDiskAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "players.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	write(`[{"id":1,"name":"A","attack":5,"defense":5,"goalkeeping":1,"overall":5},
	       {"id":1,"name":"B","attack":6,"defense":6,"goalkeeping":1,"overall":6}]`)
	if _, err := NewJSONStore(path); err == nil {
		t.Error("duplicate ids on disk must be rejected")
	}

	write(`[{"id":0,"name":"A","attack":5,"defense":5,"goalkeeping":1,"overall":5}]`)
	if _, err := NewJSONStore(path); err == nil {
		t.Error("non-positive ids on disk must be rejected")
	}
}

func TestCorruptFileIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "players.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewJSONStore(path); err == nil {
		t.Fatal("a corrupt roster must refuse to start the store")
	}

	if err := os.WriteFile(path, []byte(`[{"id":1,"name":"TooGood","attack":42,"defense":5,"goalkeeping":1,"overall":5}]`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewJSONStore(path); err == nil {
		t.Fatal("out-of-range ratings on disk must be rejected")
	}
}

func TestEmptyFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "players.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("roster has %d players, want an empty one preserved", len(list))
	}
	created, err := store.Add(model.Player{Name: "First", Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.ID != 1 {
		t.Errorf("ID = %d, want 1", created.ID)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store, _ := open(t)

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.Add(model.Player{
				Name: fmt.Sprintf("Player%d", i), Attack: 5, Defense: 5, Goalkeeping: 1, Overall: 5,
			}); err != nil {
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
	if len(list) != 44 {
		t.Errorf("roster holds %d players, want 20 + 24", len(list))
	}
	seen := map[int]bool{}
	for _, p := range list {
		if seen[p.ID] {
			t.Errorf("two players share id %d", p.ID)
		}
		seen[p.ID] = true
	}

	// The file on disk must agree with memory after the dust settles.
	reopened, err := NewJSONStore(store.path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if again, _ := reopened.List(); len(again) != len(list) {
		t.Errorf("disk holds %d players, memory holds %d", len(again), len(list))
	}
}
