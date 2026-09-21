package storage

import "football-balancer/internal/model"

// DefaultPlayers is the example roster written to data/players.json on the very
// first run, so the application is usable without any manual setup.
func DefaultPlayers() []model.Player {
	return []model.Player{
		{ID: 1, Name: "Ivan", Attack: 9, Defense: 6, Goalkeeping: 1, Overall: 8.0},
		{ID: 2, Name: "Peter", Attack: 7, Defense: 8, Goalkeeping: 1, Overall: 7.5},
		{ID: 3, Name: "Georgi", Attack: 5, Defense: 9, Goalkeeping: 1, Overall: 7.0},
		{ID: 4, Name: "Dimitar", Attack: 8, Defense: 5, Goalkeeping: 8, Overall: 7.5},
		{ID: 5, Name: "Nikolay", Attack: 6, Defense: 7, Goalkeeping: 1, Overall: 6.5},
		{ID: 6, Name: "Stefan", Attack: 9, Defense: 7, Goalkeeping: 1, Overall: 8.2},
		{ID: 7, Name: "Martin", Attack: 8, Defense: 6, Goalkeeping: 1, Overall: 7.6},
		{ID: 8, Name: "Alex", Attack: 6, Defense: 9, Goalkeeping: 1, Overall: 7.4},
		{ID: 9, Name: "Viktor", Attack: 10, Defense: 5, Goalkeeping: 1, Overall: 8.0},
		{ID: 10, Name: "Kaloyan", Attack: 5, Defense: 8, Goalkeeping: 1, Overall: 6.8},
		{ID: 11, Name: "Teodor", Attack: 7, Defense: 7, Goalkeeping: 1, Overall: 7.5},
		{ID: 12, Name: "Boris", Attack: 4, Defense: 9, Goalkeeping: 7, Overall: 7.0},
		{ID: 13, Name: "Anton", Attack: 8, Defense: 7, Goalkeeping: 1, Overall: 7.8},
		{ID: 14, Name: "Daniel", Attack: 6, Defense: 6, Goalkeeping: 1, Overall: 6.6},
		{ID: 15, Name: "Mihail", Attack: 7, Defense: 5, Goalkeeping: 1, Overall: 6.9},
		{ID: 16, Name: "Radoslav", Attack: 5, Defense: 8, Goalkeeping: 8, Overall: 7.2},
		{ID: 17, Name: "Hristo", Attack: 9, Defense: 4, Goalkeeping: 1, Overall: 7.3},
		{ID: 18, Name: "Vasil", Attack: 6, Defense: 8, Goalkeeping: 1, Overall: 7.3},
		{ID: 19, Name: "Kristian", Attack: 8, Defense: 8, Goalkeeping: 1, Overall: 8.0},
		{ID: 20, Name: "Todor", Attack: 3, Defense: 9, Goalkeeping: 9, Overall: 7.0},
	}
}
