package model

import (
	"strings"
	"testing"
	"time"
)

func squad(n int) []Player {
	out := make([]Player, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, Player{
			ID: i, Name: string(rune('A'+i-1)) + "son",
			Attack: float64(4 + i%5), Defense: float64(4 + (i*3)%5),
			Goalkeeping: float64(1 + (i*2)%3), Overall: float64(6 + i%3),
		})
	}
	return out
}

// pids extracts player IDs, matching what Match.SameLineup compares.
func pids(players []Player) []int {
	out := make([]int, 0, len(players))
	for _, p := range players {
		out = append(out, p.ID)
	}
	return out
}

func halves(n int) ([]Player, []Player) {
	all := squad(n)
	return all[:n/2], all[n/2:]
}

func TestNewMatchSnapshotsTheLineups(t *testing.T) {
	a, b := halves(6)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if m.ID != 0 {
		t.Errorf("ID = %d, want 0 so the store assigns it", m.ID)
	}
	if m.Status != MatchUpcoming {
		t.Errorf("status = %q, want %q", m.Status, MatchUpcoming)
	}
	if m.TeamSize != 3 {
		t.Errorf("teamSize = %d, want 3", m.TeamSize)
	}
	if len(m.TeamA.Players) != 3 || m.TeamA.Players[0].ID != a[0].ID {
		t.Errorf("team A snapshot = %+v", m.TeamA.Players)
	}
	if m.TeamA.Goals != nil || m.TeamB.Goals != nil {
		t.Error("a freshly drawn match must not have a score")
	}
	if len(m.Ratings) != 0 {
		t.Errorf("ratings = %v, want none", m.Ratings)
	}

	// Aggregates must reflect the frozen players, not some later roster state.
	nw := DefaultWeights().Normalized()
	want := NewTeam(a, nw)
	if m.TeamA.Attack != RoundTo(want.Attack, 1) || m.TeamA.Defense != RoundTo(want.Defense, 1) {
		t.Errorf("team A totals = %v/%v, want %v/%v",
			m.TeamA.Attack, m.TeamA.Defense, want.Attack, want.Defense)
	}
	if m.TeamA.WeightedScore != RoundTo(want.WeightedScore, 2) {
		t.Errorf("team A weighted = %v, want %v", m.TeamA.WeightedScore, want.WeightedScore)
	}
	if m.Balance < 0 || m.Balance > 100 {
		t.Errorf("balance = %v, outside 0..100", m.Balance)
	}
	if got := BalancePercent(m.TeamA.WeightedScore, m.TeamB.WeightedScore); RoundTo(got, 1) != m.Balance {
		t.Errorf("balance = %v, recomputed %v", m.Balance, got)
	}
	if m.Date.IsZero() {
		t.Error("a zero date should fall back to now")
	}
}

func TestNewMatchExplicitDateIsUTC(t *testing.T) {
	a, b := halves(4)
	date := time.Date(2026, 3, 5, 14, 30, 0, 0, time.FixedZone("EET", 2*3600))
	m, err := NewMatch(a, b, DefaultWeights(), date)
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if m.Date.Location() != time.UTC {
		t.Errorf("date location = %v, want UTC", m.Date.Location())
	}
	if want := date.UTC().Truncate(time.Second); !m.Date.Equal(want) {
		t.Errorf("date = %v, want %v", m.Date, want)
	}
}

func TestNewMatchRejectsBadLineups(t *testing.T) {
	a, b := halves(6)
	def := DefaultWeights()

	if _, err := NewMatch(nil, b, def, time.Time{}); err == nil {
		t.Error("an empty team should be rejected")
	}
	if _, err := NewMatch(a[:2], b, def, time.Time{}); err == nil {
		t.Error("unequal teams should be rejected")
	}
	if _, err := NewMatch(a, a, def, time.Time{}); err == nil {
		t.Error("the same players on both sides should be rejected")
	}
	merged := append(append([]Player{}, a...), b[0])
	if _, err := NewMatch(merged, b[1:], def, time.Time{}); err == nil {
		t.Error("a repeated player should be rejected")
	}
	noID := append([]Player{}, a...)
	noID[0].ID = 0
	if _, err := NewMatch(noID, b, def, time.Time{}); err == nil {
		t.Error("unsaved players (id 0) cannot appear in a match")
	}
}

func TestMatchSameLineup(t *testing.T) {
	a, b := halves(6)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if len(m.PlayerIDs()) != 6 {
		t.Fatalf("PlayerIDs = %v, want 6 entries", m.PlayerIDs())
	}
	teamA := idsOf(m.TeamA.Players)
	teamB := idsOf(m.TeamB.Players)

	if !m.SameLineup(teamA, teamB) {
		t.Error("a match must recognise its own line-up")
	}
	if !m.SameLineup(teamB, teamA) {
		t.Error("swapping the two sides is the same fixture: nobody has home advantage")
	}
	shuffled := append([]int{}, teamA...)
	shuffled[0], shuffled[len(shuffled)-1] = shuffled[len(shuffled)-1], shuffled[0]
	if !m.SameLineup(shuffled, teamB) {
		t.Error("order inside a team must not matter")
	}

	// One player across: same twelve people, genuinely different match.
	mixedA := append([]int{}, teamA...)
	mixedB := append([]int{}, teamB...)
	mixedA[0], mixedB[0] = mixedB[0], mixedA[0]
	if m.SameLineup(mixedA, mixedB) {
		t.Error("a re-split must not be treated as the same match, or manual tweaks would never be saved")
	}
	if m.SameLineup(teamA, teamA) {
		t.Error("two identical teams are not this match")
	}
	if m.SameLineup(teamA[:1], teamB) {
		t.Error("different team sizes cannot describe the same fixture")
	}
}

func TestMatchStatusFollowsTheStoredContent(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}

	if got := m.DerivedStatus(); got != MatchUpcoming {
		t.Fatalf("derived = %q, want %q", got, MatchUpcoming)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("an upcoming match should be valid: %v", err)
	}

	// Result, no ratings → played.
	if err := m.SetResult(5, 3); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if m.Status != MatchPlayed {
		t.Errorf("status = %q, want %q", m.Status, MatchPlayed)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("played match invalid: %v", err)
	}

	// Every player rated → rated.
	for _, p := range m.PlayerIDs() {
		if err := m.Rate(p, float64(5+p%4)); err != nil {
			t.Fatalf("Rate(%d): %v", p, err)
		}
	}
	if m.Status != MatchRated {
		t.Errorf("status = %q, want %q after rating everyone", m.Status, MatchRated)
	}
	if got := m.ratedCount(); got != 4 {
		t.Errorf("ratedCount = %d, want 4", got)
	}
}

func TestMatchValidateRejectsInconsistentStatus(t *testing.T) {
	a, b := halves(4)
	fresh, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}

	// Claiming "rated" without the data behind it must fail, and the message has
	// to name what is missing first. A rejected change must not stick.
	tooGood := fresh
	status := MatchRated
	if err := tooGood.Apply(MatchResult{Status: &status}); err == nil {
		t.Fatal("status rated without a score or ratings must be rejected")
	} else if !strings.Contains(err.Error(), "score") {
		t.Errorf("error = %q, want it to name the missing score", err)
	}
	if tooGood.Status != MatchUpcoming {
		t.Errorf("a rejected update left status = %q; Apply must be all-or-nothing", tooGood.Status)
	}

	partial := fresh
	if err := partial.SetResult(2, 2); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if err := partial.Rate(a[0].ID, 7); err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got := partial.DerivedStatus(); got != MatchPlayed {
		t.Errorf("partial ratings should stay %q, got %q", MatchPlayed, got)
	}
	status = MatchRated
	if err := partial.Apply(MatchResult{Status: &status}); err == nil {
		t.Error("one rating out of four is not 'rated'")
	}

	upcoming := fresh
	if err := upcoming.Apply(MatchResult{GoalsA: ptr(1)}); err == nil {
		t.Error("only half a score should be rejected")
	}
	if err := upcoming.Apply(MatchResult{GoalsA: ptr(1), GoalsB: ptr(0), Status: ptrMatch(MatchUpcoming)}); err == nil {
		t.Error("a match with a score cannot claim to be upcoming")
	}
}

func ptrMatch(s string) *string { return &s }

func TestMatchRatingsAreValidated(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if err := m.SetResult(1, 0); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	if err := m.Rate(a[0].ID, 11); err == nil {
		t.Error("a rating above 10 must be rejected")
	}
	if err := m.Rate(a[0].ID, 0.5); err == nil {
		t.Error("a rating below 1 must be rejected")
	}
	if err := m.Rate(999, 7); err == nil {
		t.Error("rating an outsider must be rejected")
	}
	if err := m.Rate(a[0].ID, 7.5); err != nil {
		t.Fatalf("valid rating rejected: %v", err)
	}
	if got, ok := m.Rating(a[0].ID); !ok || got != 7.5 {
		t.Errorf("Rating = %v (%v), want 7.5", got, ok)
	}
	// Re-rating replaces rather than duplicates.
	if err := m.Rate(a[0].ID, 6); err != nil {
		t.Fatalf("re-Rate: %v", err)
	}
	if len(m.Ratings) != 1 {
		t.Errorf("ratings = %+v, want a single entry", m.Ratings)
	}
	if _, err := NewMatch(a, b, DefaultWeights(), time.Time{}); err != nil {
		t.Fatalf("control NewMatch: %v", err)
	}
}

func TestMatchManOfTheMatchMustBelong(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if err := m.Apply(MatchResult{ManOfTheMatch: ptr(b[0].ID)}); err == nil {
		t.Error("an unplayed match cannot crown anyone")
	}
	if err := m.SetResult(3, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if err := m.Apply(MatchResult{ManOfTheMatch: ptr(999)}); err == nil {
		t.Error("a man of the match must be one of the players")
	}
	if err := m.Apply(MatchResult{ManOfTheMatch: ptr(b[0].ID)}); err != nil {
		t.Fatalf("valid man of the match rejected: %v", err)
	}
	if m.ManOfTheMatch == nil || *m.ManOfTheMatch != b[0].ID {
		t.Fatalf("manOfTheMatch = %v", m.ManOfTheMatch)
	}
	if err := m.Apply(MatchResult{ClearMotm: true}); err != nil {
		t.Fatalf("clearing the award failed: %v", err)
	}
	if m.ManOfTheMatch != nil {
		t.Errorf("manOfTheMatch = %d, want cleared", *m.ManOfTheMatch)
	}
}

func TestMatchLineupsAreImmutable(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	before := m.PlayerIDs()

	// A result update must never change who played, whatever it contains.
	ratings := []PlayerRating{{PlayerID: 999, Rating: 9}}
	status := MatchPlayed
	if err := m.Apply(MatchResult{Ratings: &ratings, GoalsA: ptr(1), GoalsB: ptr(1), Status: &status}); err == nil {
		t.Fatal("a rating for an outsider must be rejected")
	}
	if got := m.PlayerIDs(); len(got) != len(before) {
		t.Errorf("line-up changed: %v → %v", before, got)
	}
}

func TestMatchRedrawOnlyWhileUnplayed(t *testing.T) {
	all := squad(8)
	a, b := all[:4], all[4:8]
	m, err := NewMatch(a, b, DefaultWeights(), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	m.ID = 7

	// Swap one player across: the pending fixture follows the new draw.
	mixedA := append([]Player{}, a...)
	mixedB := append([]Player{}, b...)
	mixedA[0], mixedB[0] = mixedB[0], mixedA[0]
	before := m.Balance
	if err := m.Redraw(mixedA, mixedB, DefaultWeights(), nil); err != nil {
		t.Fatalf("Redraw: %v", err)
	}
	if m.ID != 7 {
		t.Errorf("id = %d, want the redraw to keep it", m.ID)
	}
	if !m.Date.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("date = %v, want the redraw to keep it", m.Date)
	}
	if m.Status != MatchUpcoming {
		t.Errorf("status = %q, want upcoming", m.Status)
	}
	if m.SameLineup(pids(a), pids(b)) {
		t.Error("the line-up was not replaced")
	}
	if !m.SameLineup(pids(mixedA), pids(mixedB)) {
		t.Error("the new line-up was not stored")
	}
	if m.Balance != before && m.Balance == 0 {
		t.Errorf("balance = %v, want it recomputed for the new teams", m.Balance)
	}

	// Once a result exists the composition is history and must not move.
	if err := m.SetResult(2, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if err := m.Redraw(a, b, DefaultWeights(), nil); err == nil {
		t.Fatal("a played match must not be re-drawable")
	} else if !strings.Contains(err.Error(), "without a result") {
		t.Errorf("error = %q, want it to explain why", err)
	}
	if !m.SameLineup(pids(mixedA), pids(mixedB)) {
		t.Error("a rejected redraw must leave the match alone")
	}

	// Clearing the result unlocks re-drawing again.
	if err := m.ClearResult(); err != nil {
		t.Fatalf("ClearResult: %v", err)
	}
	if err := m.Redraw(a, b, DefaultWeights(), nil); err != nil {
		t.Fatalf("Redraw after clearing: %v", err)
	}
	if !m.SameLineup(pids(a), pids(b)) || m.ID != 7 {
		t.Errorf("after redraw: %+v", m)
	}
}

func TestMatchRedrawValidatesTheNewTeams(t *testing.T) {
	all := squad(8)
	m, err := NewMatch(all[:4], all[4:], DefaultWeights(), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if err := m.Redraw(all[:3], all[3:], DefaultWeights(), nil); err == nil {
		t.Error("a re-draw with unequal teams must be rejected")
	}
	if err := m.Redraw(all[:4], all[:4], DefaultWeights(), nil); err == nil {
		t.Error("the same players on both sides must be rejected")
	}
	when := time.Unix(1800000000, 0)
	if err := m.Redraw(all[:2], all[2:4], DefaultWeights(), &when); err == nil {
		t.Error("a re-draw with the wrong size for this match must be rejected")
	}
}

func TestMatchClearResult(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if err := m.SetResult(4, 2); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if err := m.Apply(MatchResult{ManOfTheMatch: ptr(a[0].ID)}); err != nil {
		t.Fatalf("motm: %v", err)
	}
	if err := m.ClearResult(); err != nil {
		t.Fatalf("ClearResult: %v", err)
	}
	if m.Status != MatchUpcoming || m.TeamA.Goals != nil || len(m.Ratings) != 0 || m.ManOfTheMatch != nil {
		t.Errorf("after clearing: %+v", m)
	}
}

func TestOutcomeOf(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	if got := m.OutcomeOf("A"); got != "" {
		t.Errorf("outcome before a result = %q, want empty", got)
	}
	for _, tc := range []struct {
		goalsA, goalsB int
		wantA, wantB   string
	}{
		{3, 1, OutcomeWin, OutcomeLoss},
		{1, 3, OutcomeLoss, OutcomeWin},
		{2, 2, OutcomeDraw, OutcomeDraw},
	} {
		m, err := NewMatch(a, b, DefaultWeights(), time.Time{})
		if err != nil {
			t.Fatalf("NewMatch: %v", err)
		}
		if err := m.SetResult(tc.goalsA, tc.goalsB); err != nil {
			t.Fatalf("SetResult: %v", err)
		}
		if got := m.OutcomeOf("A"); got != tc.wantA {
			t.Errorf("%d-%d: team A outcome = %q, want %q", tc.goalsA, tc.goalsB, got, tc.wantA)
		}
		if got := m.OutcomeOf("B"); got != tc.wantB {
			t.Errorf("%d-%d: team B outcome = %q, want %q", tc.goalsA, tc.goalsB, got, tc.wantB)
		}
	}
}

func TestHistoryOfCountsOnlyPlayedMatches(t *testing.T) {
	a, b := halves(6) // players 1..3 vs 4..6
	drawn, err := NewMatch(a, b, DefaultWeights(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	drawn.ID = 1

	played, err := NewMatch(a, b, DefaultWeights(), time.Now())
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	played.ID = 2
	if err := played.SetResult(4, 2); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	for _, p := range a {
		if err := played.Rate(p.ID, 8); err != nil {
			t.Fatalf("Rate: %v", err)
		}
	}
	if err := played.Apply(MatchResult{ManOfTheMatch: ptr(a[0].ID)}); err != nil {
		t.Fatalf("motm: %v", err)
	}

	all := []Match{drawn, played}

	// A player on the winning side, rated.
	h := HistoryOf(all, a[0])
	if h.Summary.Matches != 1 {
		t.Errorf("matches = %d, want 1 (the drawn match was never played)", h.Summary.Matches)
	}
	if h.Summary.Wins != 1 || h.Summary.Draws != 0 || h.Summary.Losses != 0 {
		t.Errorf("record = %+v, want one win", h.Summary)
	}
	if h.Summary.AverageRating != 8 {
		t.Errorf("average = %v, want 8", h.Summary.AverageRating)
	}
	if h.Summary.ManOfTheMatch != 1 {
		t.Errorf("manOfTheMatch = %d, want 1", h.Summary.ManOfTheMatch)
	}
	if len(h.Appearances) != 1 || h.Appearances[0].MatchID != 2 || h.Appearances[0].Team != "A" {
		t.Errorf("appearances = %+v", h.Appearances)
	}
	if app := h.Appearances[0]; app.GoalsFor == nil || *app.GoalsFor != 4 || *app.GoalsAgainst != 2 {
		t.Errorf("goals = %+v, want 4 for 2 against", app)
	}

	// A player on the losing side sees the same score the other way round.
	lost := HistoryOf(all, b[0])
	if lost.Summary.Losses != 1 || *lost.Appearances[0].GoalsFor != 2 {
		t.Errorf("losing side record = %+v", lost.Summary)
	}
	if lost.Summary.Rated != 0 || lost.Summary.AverageRating != 0 {
		t.Errorf("unrated player summary = %+v, want no rating", lost.Summary)
	}
	if lost.Summary.Matches != 1 {
		t.Errorf("an unrated participant should still count as an appearance, got %d", lost.Summary.Matches)
	}
}

func TestHistoryOfAveragesAndFormWindow(t *testing.T) {
	players := squad(8)
	var matches []Match
	ratings := []float64{9, 7, 8, 6, 5, 7}
	for i, r := range ratings {
		a := players[:4]
		b := players[4:]
		m, err := NewMatch(a, b, DefaultWeights(), time.Now().Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatalf("NewMatch: %v", err)
		}
		m.ID = i + 1
		if err := m.SetResult(i%3, (i+1)%3); err != nil {
			t.Fatalf("SetResult: %v", err)
		}
		if err := m.Rate(a[0].ID, r); err != nil {
			t.Fatalf("Rate: %v", err)
		}
		matches = append(matches, m)
	}

	h := HistoryOf(matches, players[0])
	if h.Summary.Matches != 6 {
		t.Fatalf("matches = %d, want 6", h.Summary.Matches)
	}
	// newest first: 7,5,6,8,7,9 → average 7.0
	if h.Summary.AverageRating != 7 {
		t.Errorf("average = %v, want 7", h.Summary.AverageRating)
	}
	want := []float64{7, 5, 6, 8, 7}
	if len(h.Summary.LastRatings) != len(want) {
		t.Fatalf("lastRatings = %v, want %v", h.Summary.LastRatings, want)
	}
	for i, v := range want {
		if h.Summary.LastRatings[i] != v {
			t.Errorf("lastRatings[%d] = %v, want %v", i, h.Summary.LastRatings[i], v)
		}
	}
	if h.Appearances[0].Date.Before(h.Appearances[5].Date) {
		t.Error("appearances must be newest first")
	}
	if h.Summary.LastPlayed == nil || !h.Summary.LastPlayed.Equal(h.Appearances[0].Date) {
		t.Errorf("lastPlayed = %v, want %v", h.Summary.LastPlayed, h.Appearances[0].Date)
	}
}

func TestHistoryOfIgnoresStrayRatings(t *testing.T) {
	a, b := halves(4)
	m, err := NewMatch(a, b, DefaultWeights(), time.Now())
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	m.ID = 1
	if err := m.SetResult(2, 0); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	// Hand-editable JSON could contain a rating for someone who is gone; the
	// aggregate must not divide by it.
	m.Ratings = append(m.Ratings, PlayerRating{PlayerID: a[0].ID, Rating: 6}, PlayerRating{PlayerID: 4242, Rating: 10})

	h := HistoryOf([]Match{m}, a[0])
	if h.Summary.Rated != 1 || h.Summary.AverageRating != 6 {
		t.Errorf("summary = %+v, want one rating of 6", h.Summary)
	}
	if outsider := HistoryOf([]Match{m}, Player{ID: 4242, Name: "Ghost"}); outsider.Summary.Matches != 0 {
		t.Errorf("a non-participant should have no history, got %+v", outsider.Summary)
	}
}

func TestSortMatchesNewestFirst(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mk := func(id int, when time.Time) Match {
		return Match{ID: id, Date: when, Status: MatchUpcoming, TeamSize: 1,
			TeamA: MatchTeam{Players: []MatchPlayer{{ID: 100 + id, Name: "A"}}},
			TeamB: MatchTeam{Players: []MatchPlayer{{ID: 200 + id, Name: "B"}}}}
	}
	list := []Match{mk(1, base), mk(2, base.Add(time.Hour)), mk(3, base)}
	SortMatchesNewestFirst(list)
	if list[0].ID != 2 || list[1].ID != 3 || list[2].ID != 1 {
		t.Errorf("order = %d,%d,%d, want 2,3,1 (newest first, then higher id)", list[0].ID, list[1].ID, list[2].ID)
	}
}

func TestMatchValidateChecksShape(t *testing.T) {
	base := func() Match {
		a, b := halves(4)
		m, err := NewMatch(a, b, DefaultWeights(), time.Unix(1700000000, 0))
		if err != nil {
			t.Fatalf("NewMatch: %v", err)
		}
		return m
	}

	short := base()
	short.TeamA.Players = short.TeamA.Players[:1]
	short.TeamSize = 1
	if err := short.Validate(); err == nil {
		t.Error("team size must agree with both line-ups")
	}

	ghost := base()
	ghost.Ratings = []PlayerRating{{PlayerID: 777, Rating: 8}}
	if err := ghost.Validate(); err == nil {
		t.Error("a rating for a non-participant must be rejected")
	}

	both := base()
	both.TeamB.Players[0].ID = both.TeamA.Players[0].ID
	if err := both.Validate(); err == nil {
		t.Error("one player in both teams must be rejected")
	}

	nameless := base()
	nameless.TeamA.Players[0].Name = ""
	if err := nameless.Validate(); err == nil {
		t.Error("a snapshotted player without a name must be rejected")
	}

	unknownStatus := base()
	unknownStatus.Status = "finished"
	if err := unknownStatus.Validate(); err == nil {
		t.Error("an unknown status must be rejected")
	}

	// Negative goals are clamped rather than stored: the API rejects them, but a
	// hand-edited data file must still load.
	negative := base()
	if err := negative.SetResult(-1, 2); err != nil {
		t.Fatalf("SetResult with negative goals: %v", err)
	}
	if negative.TeamA.Goals == nil || *negative.TeamA.Goals != 0 {
		t.Errorf("goals = %v, want clamped to 0", negative.TeamA.Goals)
	}
	if err := negative.Validate(); err != nil {
		t.Errorf("clamped match should be valid: %v", err)
	}
}
