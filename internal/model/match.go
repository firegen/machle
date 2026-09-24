package model

import (
	"fmt"
	"sort"
	"time"
)

// Match lifecycle. The status is always consistent with what the match actually
// holds (a score, ratings), which the server enforces rather than trusts.
const (
	MatchUpcoming = "upcoming" // line-ups drawn, nothing played yet
	MatchPlayed   = "played"   // result recorded
	MatchRated    = "rated"    // result recorded and every player rated
)

// ValidStatus reports whether s is one of the three lifecycle states.
func ValidStatus(s string) bool {
	switch s {
	case MatchUpcoming, MatchPlayed, MatchRated:
		return true
	}
	return false
}

// MatchOutcome describes a match from one player's point of view.
const (
	OutcomeWin  = "win"
	OutcomeDraw = "draw"
	OutcomeLoss = "loss"
)

// MatchPlayer is a frozen copy of a roster entry: renaming or deleting a player
// later must not rewrite what actually happened on the pitch.
type MatchPlayer struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MatchTeam is one side of a saved match, with the strength it had when the
// match was drawn.
type MatchTeam struct {
	Players       []MatchPlayer `json:"players"`
	Goals         *int          `json:"goals"`
	Attack        float64       `json:"attack"`
	Defense       float64       `json:"defense"`
	Goalkeeping   float64       `json:"goalkeeping"`
	Overall       float64       `json:"overall"`
	WeightedScore float64       `json:"weightedScore"`
	Goalkeepers   int           `json:"goalkeepers"`
}

// PlayerRating is one player's end-of-match mark, on the same 1..10 scale as
// any other rating. It is a record of performance, never an input to balancing.
type PlayerRating struct {
	PlayerID int     `json:"playerId"`
	Rating   float64 `json:"rating"`
}

// Match is a saved fixture: drawn line-ups, optionally a result and per-player
// ratings.
type Match struct {
	ID            int            `json:"id"`
	Date          time.Time      `json:"date"`
	Status        string         `json:"status"`
	TeamSize      int            `json:"teamSize"`
	TeamA         MatchTeam      `json:"teamA"`
	TeamB         MatchTeam      `json:"teamB"`
	Weights       Weights        `json:"weights"`
	Balance       float64        `json:"balance"`
	Difference    float64        `json:"difference"`
	Ratings       []PlayerRating `json:"ratings"`
	ManOfTheMatch *int           `json:"manOfTheMatch,omitempty"`
}

// SnapshotTeam freezes a line-up and its aggregate strength. Weights are
// normalised here, so callers may pass them on any scale.
func SnapshotTeam(players []Player, weights Weights) MatchTeam {
	nw := weights.Normalized()
	t := NewTeam(players, nw)
	out := MatchTeam{
		Players:       make([]MatchPlayer, 0, len(players)),
		Attack:        RoundTo(t.Attack, 1),
		Defense:       RoundTo(t.Defense, 1),
		Goalkeeping:   RoundTo(t.Goalkeeping, 1),
		Overall:       RoundTo(t.Overall, 1),
		WeightedScore: RoundTo(t.WeightedScore, 2),
		Goalkeepers:   t.Goalkeepers,
	}
	for _, p := range players {
		out.Players = append(out.Players, MatchPlayer{ID: p.ID, Name: p.Name})
	}
	return out
}

// NewMatch records a drawn fixture. Both sides must be non-empty, equal in size
// and free of repeated players. A zero date becomes "now".
func NewMatch(teamA, teamB []Player, weights Weights, date time.Time) (Match, error) {
	if len(teamA) == 0 || len(teamB) == 0 {
		return Match{}, fmt.Errorf("both teams need at least one player")
	}
	if len(teamA) != len(teamB) {
		return Match{}, fmt.Errorf("teams must have the same size, got %d and %d", len(teamA), len(teamB))
	}
	seen := make(map[int]bool, len(teamA)+len(teamB))
	for _, p := range append(append([]Player{}, teamA...), teamB...) {
		if p.ID <= 0 {
			return Match{}, fmt.Errorf("player %q has no id; only saved players can be matched", p.Name)
		}
		if seen[p.ID] {
			return Match{}, fmt.Errorf("player %q (id %d) is listed twice", p.Name, p.ID)
		}
		seen[p.ID] = true
	}

	a := SnapshotTeam(teamA, weights)
	b := SnapshotTeam(teamB, weights)
	if date.IsZero() {
		date = time.Now()
	}
	return Match{
		Date:       date.UTC().Truncate(time.Second),
		Status:     MatchUpcoming,
		TeamSize:   len(teamA),
		TeamA:      a,
		TeamB:      b,
		Weights:    weights.AsPercent(),
		Balance:    RoundTo(BalancePercent(a.WeightedScore, b.WeightedScore), 1),
		Difference: RoundTo(abs(a.WeightedScore-b.WeightedScore), 2),
		Ratings:    []PlayerRating{},
	}, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// PlayerIDs lists every participant, team A first.
func (m Match) PlayerIDs() []int {
	ids := make([]int, 0, len(m.TeamA.Players)+len(m.TeamB.Players))
	for _, p := range m.TeamA.Players {
		ids = append(ids, p.ID)
	}
	for _, p := range m.TeamB.Players {
		ids = append(ids, p.ID)
	}
	return ids
}

// SameLineup reports whether the given teams are precisely this match's, either
// as drawn or with the two sides swapped (a fixture has no home team). Comparing
// per side rather than the combined pool matters: moving two players across is a
// different match, even though the same twelve people are involved.
func (m Match) SameLineup(teamA, teamB []int) bool {
	a := idsOf(m.TeamA.Players)
	b := idsOf(m.TeamB.Players)
	return (sameIDs(a, teamA) && sameIDs(b, teamB)) || (sameIDs(a, teamB) && sameIDs(b, teamA))
}

func idsOf(players []MatchPlayer) []int {
	ids := make([]int, 0, len(players))
	for _, p := range players {
		ids = append(ids, p.ID)
	}
	return ids
}

// sameIDs compares two ID groups ignoring order and duplicates.
func sameIDs(want, got []int) bool {
	if len(want) != len(got) {
		return false
	}
	left := append([]int{}, want...)
	right := append([]int{}, got...)
	sort.Ints(left)
	sort.Ints(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// PlayingTime says which side a player was on, and whether they played at all.
func (m Match) PlayingTime(playerID int) (team string, ok bool) {
	for _, p := range m.TeamA.Players {
		if p.ID == playerID {
			return "A", true
		}
	}
	for _, p := range m.TeamB.Players {
		if p.ID == playerID {
			return "B", true
		}
	}
	return "", false
}

// Rating returns a player's end-of-match mark.
func (m Match) Rating(playerID int) (float64, bool) {
	for _, r := range m.Ratings {
		if r.PlayerID == playerID {
			return r.Rating, true
		}
	}
	return 0, false
}

func (m Match) goalsSet() bool { return m.TeamA.Goals != nil && m.TeamB.Goals != nil }

// ratedCount counts ratings belonging to a participant; stray ratings are not
// counted so history stays readable after roster surgery.
func (m Match) ratedCount() int {
	in := make(map[int]bool, len(m.Ratings)*2)
	for _, id := range m.PlayerIDs() {
		in[id] = true
	}
	n := 0
	for _, r := range m.Ratings {
		if in[r.PlayerID] {
			n++
		}
	}
	return n
}

// DerivedStatus is the most advanced status the stored content supports.
func (m Match) DerivedStatus() string {
	switch {
	case m.goalsSet() && m.ratedCount() == len(m.PlayerIDs()):
		return MatchRated
	case m.goalsSet():
		return MatchPlayed
	default:
		return MatchUpcoming
	}
}

// Validate enforces the invariants the UI and the aggregates rely on: a line-up
// of the right size, ratings only for participants, and a status that matches
// the stored content.
func (m Match) Validate() error {
	if m.TeamSize <= 0 {
		return fmt.Errorf("team size must be positive")
	}
	if len(m.TeamA.Players) != m.TeamSize || len(m.TeamB.Players) != m.TeamSize {
		return fmt.Errorf("both teams must hold %d players, got %d and %d",
			m.TeamSize, len(m.TeamA.Players), len(m.TeamB.Players))
	}
	if !ValidStatus(m.Status) {
		return fmt.Errorf("unknown match status %q", m.Status)
	}

	seen := map[int]bool{}
	for _, id := range m.PlayerIDs() {
		if id <= 0 {
			return fmt.Errorf("match players must have positive ids")
		}
		if seen[id] {
			return fmt.Errorf("player %d appears in both teams", id)
		}
		seen[id] = true
	}

	for _, team := range []MatchTeam{m.TeamA, m.TeamB} {
		for _, p := range team.Players {
			if p.Name == "" {
				return fmt.Errorf("player %d was saved without a name", p.ID)
			}
		}
		for _, g := range []*int{team.Goals} {
			if g != nil && *g < 0 {
				return fmt.Errorf("goals must not be negative")
			}
		}
	}

	for _, r := range m.Ratings {
		if _, ok := m.PlayingTime(r.PlayerID); !ok {
			return fmt.Errorf("rating refers to player %d who is not in this match", r.PlayerID)
		}
		if r.Rating < MinRating || r.Rating > MaxRating {
			return fmt.Errorf("rating for player %d must be between %g and %g, got %g",
				r.PlayerID, MinRating, MaxRating, r.Rating)
		}
	}

	if m.ManOfTheMatch != nil {
		if _, ok := m.PlayingTime(*m.ManOfTheMatch); !ok {
			return fmt.Errorf("man of the match must be one of the %d players", 2*m.TeamSize)
		}
	}

	// Status and content must agree, otherwise "rated" would mean nothing.
	switch m.Status {
	case MatchUpcoming:
		// Half a score is not a result: requiring both sides keeps "upcoming"
		// unambiguous, so a saved match is either played or clearly not.
		if (m.TeamA.Goals == nil) != (m.TeamB.Goals == nil) {
			return fmt.Errorf("a result needs the score for both teams")
		}
		if m.goalsSet() || len(m.Ratings) > 0 {
			return fmt.Errorf("this match already holds a result or ratings, so it cannot stay %q", MatchUpcoming)
		}
	case MatchPlayed:
		if !m.goalsSet() {
			return fmt.Errorf("a played match needs the score for both teams")
		}
	case MatchRated:
		if !m.goalsSet() {
			return fmt.Errorf("a rated match needs the score for both teams")
		}
		if missing := len(m.PlayerIDs()) - m.ratedCount(); missing > 0 {
			return fmt.Errorf("cannot mark as rated: %d of %d players still have no rating",
				missing, len(m.PlayerIDs()))
		}
	}
	if m.ManOfTheMatch != nil && m.Status == MatchUpcoming {
		return fmt.Errorf("an unplayed match cannot have a man of the match")
	}
	return nil
}

// MatchResult is the mutable part of a saved match. Line-ups are frozen at
// creation, so no request can rewrite who played.
type MatchResult struct {
	Date          *time.Time
	Status        *string
	GoalsA        *int
	GoalsB        *int
	Ratings       *[]PlayerRating
	ManOfTheMatch *int
	ClearMotm     bool
}

// Apply folds a result update into a stored match and revalidates the outcome.
// An omitted status advances to whatever the content now supports. The update is
// validated on a copy first, so a rejected change leaves the match exactly as it
// was rather than half-applying a bad rating or status.
func (m *Match) Apply(r MatchResult) error {
	next := *m // the line-ups are never touched here, so sharing them is safe
	if r.Date != nil {
		next.Date = r.Date.UTC().Truncate(time.Second)
	}
	if r.GoalsA != nil {
		next.TeamA.Goals = ptr(max(0, *r.GoalsA))
	}
	if r.GoalsB != nil {
		next.TeamB.Goals = ptr(max(0, *r.GoalsB))
	}
	if r.Ratings != nil {
		next.Ratings = compactRatings(*r.Ratings)
	}
	switch {
	case r.ClearMotm:
		next.ManOfTheMatch = nil
	case r.ManOfTheMatch != nil:
		next.ManOfTheMatch = r.ManOfTheMatch
	}
	if r.Status != nil {
		next.Status = *r.Status
	} else {
		next.Status = next.DerivedStatus()
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*m = next
	return nil
}

// SetResult records the final score and advances the status.
func (m *Match) SetResult(goalsA, goalsB int) error {
	return m.Apply(MatchResult{GoalsA: ptr(goalsA), GoalsB: ptr(goalsB)})
}

// Rate stores one player's mark, replacing an earlier one.
func (m *Match) Rate(playerID int, rating float64) error {
	list := append([]PlayerRating{}, m.Ratings...)
	replaced := false
	for i := range list {
		if list[i].PlayerID == playerID {
			list[i].Rating = rating
			replaced = true
		}
	}
	if !replaced {
		list = append(list, PlayerRating{PlayerID: playerID, Rating: rating})
	}
	return m.Apply(MatchResult{Ratings: &list})
}

// Redraw replaces the line-ups of a match that has not been played yet. It is the
// one part of a saved match that may change after creation: hand-tweaking the
// teams and then playing is normal, while a result or ratings freeze the
// composition forever.
func (m *Match) Redraw(teamA, teamB []Player, weights Weights, date *time.Time) error {
	if m.Status != MatchUpcoming || m.TeamA.Goals != nil || m.TeamB.Goals != nil || len(m.Ratings) > 0 {
		return fmt.Errorf("only a match without a result can be re-drawn (this one is %q)", m.Status)
	}
	// The fixture keeps its shape: re-distributing people is not the same as
	// turning a 5 v 5 into a 3 v 3 while keeping the saved identity.
	if len(teamA) != m.TeamSize || len(teamB) != m.TeamSize {
		return fmt.Errorf("a re-draw must keep the team size at %d, got %d and %d", m.TeamSize, len(teamA), len(teamB))
	}
	when := m.Date
	if date != nil {
		when = *date
	}
	fresh, err := NewMatch(teamA, teamB, weights, when)
	if err != nil {
		return err
	}
	fresh.ID = m.ID
	*m = fresh
	return m.Validate()
}

// ClearResult wipes the score, the ratings and the award, sending the match back
// to the drawing board. Used when a result was entered by mistake.
func (m *Match) ClearResult() error {
	empty := []PlayerRating{}
	status := MatchUpcoming
	m.TeamA.Goals = nil
	m.TeamB.Goals = nil
	m.Ratings = empty
	m.ManOfTheMatch = nil
	m.Status = status
	return m.Validate()
}

func compactRatings(in []PlayerRating) []PlayerRating {
	byPlayer := make(map[int]float64, len(in))
	for _, r := range in {
		byPlayer[r.PlayerID] = RoundTo(r.Rating, 1)
	}
	out := make([]PlayerRating, 0, len(byPlayer))
	for id, v := range byPlayer {
		out = append(out, PlayerRating{PlayerID: id, Rating: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlayerID < out[j].PlayerID })
	return out
}

func ptr(v int) *int { return &v }

// OutcomeOf reports how a given side fared, or "" while there is no score.
func (m Match) OutcomeOf(team string) string {
	if !m.goalsSet() {
		return ""
	}
	mine, theirs := *m.TeamA.Goals, *m.TeamB.Goals
	if team == "B" {
		mine, theirs = theirs, mine
	}
	switch {
	case mine > theirs:
		return OutcomeWin
	case mine < theirs:
		return OutcomeLoss
	default:
		return OutcomeDraw
	}
}

// Appearance is one player's record of a single match.
type Appearance struct {
	MatchID       int       `json:"matchId"`
	Date          time.Time `json:"date"`
	Status        string    `json:"status"`
	Team          string    `json:"team"`
	GoalsFor      *int      `json:"goalsFor,omitempty"`
	GoalsAgainst  *int      `json:"goalsAgainst,omitempty"`
	Outcome       string    `json:"outcome,omitempty"`
	Rating        float64   `json:"rating,omitempty"`
	ManOfTheMatch bool      `json:"manOfTheMatch,omitempty"`
	Balance       float64   `json:"balance"`
}

// PlayerSummary aggregates a player's matches.
type PlayerSummary struct {
	PlayerID      int        `json:"playerId"`
	Matches       int        `json:"matches"`
	Rated         int        `json:"rated"`
	AverageRating float64    `json:"averageRating"`
	Wins          int        `json:"wins"`
	Draws         int        `json:"draws"`
	Losses        int        `json:"losses"`
	ManOfTheMatch int        `json:"manOfTheMatch"`
	LastRatings   []float64  `json:"lastRatings,omitempty"`
	LastPlayed    *time.Time `json:"lastPlayed,omitempty"`
}

// PlayerHistory is everything one player has played, newest first.
type PlayerHistory struct {
	PlayerID    int           `json:"playerId"`
	Name        string        `json:"name"`
	Summary     PlayerSummary `json:"summary"`
	Appearances []Appearance  `json:"appearances"`
}

// SortMatchesNewestFirst orders by date, falling back to the ID for ties so the
// list is stable when several matches share a timestamp.
func SortMatchesNewestFirst(matches []Match) {
	sort.SliceStable(matches, func(i, j int) bool {
		if !matches[i].Date.Equal(matches[j].Date) {
			return matches[i].Date.After(matches[j].Date)
		}
		return matches[i].ID > matches[j].ID
	})
}

// HistoryOf builds one player's record. Only matches that actually happened
// count: a drawn line-up nobody turned up for is not an appearance.
func HistoryOf(matches []Match, player Player) PlayerHistory {
	h := PlayerHistory{PlayerID: player.ID, Name: player.Name, Appearances: []Appearance{}}
	var ratingSum float64

	for _, m := range matches {
		team, played := m.PlayingTime(player.ID)
		if !played || m.Status == MatchUpcoming {
			continue
		}
		app := Appearance{
			MatchID: m.ID,
			Date:    m.Date,
			Status:  m.Status,
			Team:    team,
			Outcome: m.OutcomeOf(team),
			Balance: m.Balance,
		}
		if m.goalsSet() {
			mine, theirs := *m.TeamA.Goals, *m.TeamB.Goals
			if team == "B" {
				mine, theirs = theirs, mine
			}
			app.GoalsFor, app.GoalsAgainst = ptr(mine), ptr(theirs)
		}
		if rating, has := m.Rating(player.ID); has {
			app.Rating = rating
			h.Summary.Rated++
			ratingSum += rating
		}
		if m.ManOfTheMatch != nil && *m.ManOfTheMatch == player.ID {
			app.ManOfTheMatch = true
			h.Summary.ManOfTheMatch++
		}
		switch app.Outcome {
		case OutcomeWin:
			h.Summary.Wins++
		case OutcomeDraw:
			h.Summary.Draws++
		case OutcomeLoss:
			h.Summary.Losses++
		}
		h.Appearances = append(h.Appearances, app)
	}

	sort.SliceStable(h.Appearances, func(i, j int) bool { return h.Appearances[i].Date.After(h.Appearances[j].Date) })

	h.Summary.PlayerID = player.ID
	h.Summary.Matches = len(h.Appearances)
	if h.Summary.Rated > 0 {
		h.Summary.AverageRating = RoundTo(ratingSum/float64(h.Summary.Rated), 2)
	}
	const formWindow = 5
	for i, app := range h.Appearances {
		if i >= formWindow {
			break
		}
		if app.Rating > 0 {
			h.Summary.LastRatings = append(h.Summary.LastRatings, app.Rating)
		}
	}
	if len(h.Appearances) > 0 {
		last := h.Appearances[0].Date
		h.Summary.LastPlayed = &last
	}
	return h
}
