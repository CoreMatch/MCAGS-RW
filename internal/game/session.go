package game

import "time"

// PlayerID is the unique identifier for a player.
type PlayerID string

// Player represents a player in the game session.
type Player struct {
	ID   PlayerID
	Name string
	// Add other player-specific data here, like team, resources, etc.
}

// GameState represents the state of the game world.
type GameState struct {
	// For now, this is a placeholder.
	// We will add map data, units, etc. here later.
	WorldTime int64 // Example: game time in ticks
}

// Session represents a single game session or room.
type Session struct {
	ID      string
	players map[PlayerID]*Player
	state   *GameState
	ticker  *time.Ticker
	quit    chan struct{}
}

// NewSession creates and initializes a new game session.
func NewSession(id string) *Session {
	return &Session{
		ID:      id,
		players: make(map[PlayerID]*Player),
		state:   &GameState{},
		quit:    make(chan struct{}),
	}
}

// Start begins the game loop for the session.
func (s *Session) Start(tickRate time.Duration) {
	s.ticker = time.NewTicker(tickRate)
	go func() {
		for {
			select {
			case <-s.ticker.C:
				s.update()
			case <-s.quit:
				s.ticker.Stop()
				return
			}
		}
	}()
}

// Stop ends the game loop for the session.
func (s *Session) Stop() {
	close(s.quit)
}

// update is the main game logic update function, called on each tick.
func (s *Session) update() {
	// 1. Process player inputs (we'll add this later)
	// 2. Update game state
	s.state.WorldTime++

	// 3. Broadcast state to players (we'll add this later)
}

// AddPlayer adds a player to the session.
func (s *Session) AddPlayer(player *Player) {
	s.players[player.ID] = player
}

// RemovePlayer removes a player from the session.
func (s *Session) RemovePlayer(playerID PlayerID) {
	delete(s.players, playerID)
}
