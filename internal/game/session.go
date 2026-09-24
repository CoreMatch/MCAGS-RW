package game

import (
	"fmt"
	"rwgin/internal/protocol"
	"time"
)

// PlayerID is the unique identifier for a player.
type PlayerID string

// PlayerConn represents the network connection for a player.
// It is an interface to decouple the game logic from the network layer.
type PlayerConn interface {
	Send(packet protocol.Packet) error
}

// Player represents a player in the game session.
type Player struct {
	ID   PlayerID
	Name string
	Conn PlayerConn
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

// GetPlayer retrieves a player from the session by their ID.
func (s *Session) GetPlayer(id PlayerID) *Player {
	// Note: This is a simple implementation. For sessions with many players,
	// we might want to use a more efficient data structure.
	player, _ := s.players[id]
	return player
}

// AddPlayer adds a player to the session and broadcasts their arrival.
func (s *Session) AddPlayer(player *Player) {
	s.players[player.ID] = player

	// Broadcast the new player's arrival to all players in the session.
	joinMsg := fmt.Sprintf("%s has joined the game.", player.Name)
	packet, err := protocol.EncodeSystemMessage(joinMsg)
	if err == nil {
		s.broadcast(packet, nil) // Broadcast to all, excluding no one
	}
}

func (s *Session) HandleChatMessage(from *Player, message string) {
	packet, err := protocol.EncodeChat(from.Name, message)
	if err == nil {
		s.broadcast(packet, from.ID) // Broadcast to all, excluding the sender
	}
}

// broadcast sends a packet to all players in the session, optionally excluding one.
func (s *Session) broadcast(packet protocol.Packet, exclude PlayerID) {
	for id, player := range s.players {
		if id != exclude && player.Conn != nil {
			player.Conn.Send(packet)
		}
	}
}

// RemovePlayer removes a player from the session.
func (s *Session) RemovePlayer(playerID PlayerID) {
	delete(s.players, playerID)
}
