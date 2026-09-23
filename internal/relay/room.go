package relay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"rwgin/internal/config"
	"rwgin/internal/protocol"
)

type RoomSummary struct {
	Code       string    `json:"code"`
	Title      string    `json:"title"`
	MapName    string    `json:"mapName"`
	MaxPlayers int       `json:"maxPlayers"`
	Players    int       `json:"players"`
	InGame     bool      `json:"inGame"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Manager struct {
	cfg   config.Config
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewManager(cfg config.Config) *Manager {
	return &Manager{
		cfg:   cfg,
		rooms: make(map[string]*Room),
	}
}

func (m *Manager) List() []RoomSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]RoomSummary, 0, len(m.rooms))
	for _, room := range m.rooms {
		items = append(items, room.Summary())
	}
	return items
}

func (m *Manager) Get(code string) (*Room, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	m.mu.RLock()
	defer m.mu.RUnlock()
	room, ok := m.rooms[normalized]
	return room, ok
}

func (m *Manager) Create(code, title string, maxPlayers int) (*Room, error) {
	if maxPlayers <= 0 {
		maxPlayers = m.cfg.DefaultMaxPlayers
	}

	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" {
		normalized = m.nextCode()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rooms[normalized]; exists {
		return nil, fmt.Errorf("room already exists: %s", normalized)
	}

	room := &Room{
		Code:       normalized,
		Title:      defaultIfBlank(title, m.cfg.DefaultRoomName+" "+normalized),
		MapName:    m.cfg.DefaultMapName,
		ServerUUID: randomHex(16),
		MaxPlayers: maxPlayers,
		MaxUnits:   m.cfg.DefaultMaxUnits,
		Income:     m.cfg.DefaultIncome,
		CreatedAt:  time.Now(),
		players:    make([]*Session, maxPlayers),
	}
	m.rooms[normalized] = room
	return room, nil
}

func (m *Manager) JoinOrCreate(code string, session *Session) (*Room, bool, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" {
		var err error
		room, err := m.Create("", "", m.cfg.DefaultMaxPlayers)
		if err != nil {
			return nil, false, err
		}
		if err := room.Join(session); err != nil {
			return nil, false, err
		}
		return room, true, nil
	}

	m.mu.RLock()
	room, ok := m.rooms[normalized]
	m.mu.RUnlock()
	if !ok {
		var err error
		room, err = m.Create(normalized, "", m.cfg.DefaultMaxPlayers)
		if err != nil {
			return nil, false, err
		}
		if err := room.Join(session); err != nil {
			return nil, false, err
		}
		return room, true, nil
	}

	if err := room.Join(session); err != nil {
		return nil, false, err
	}
	return room, false, nil
}

func (m *Manager) RemoveIfEmpty(room *Room) {
	if room == nil || room.PlayerCount() != 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.rooms[room.Code]; ok && current == room && room.PlayerCount() == 0 {
		delete(m.rooms, room.Code)
	}
}

func (m *Manager) nextCode() string {
	for {
		code := fmt.Sprintf("%s%s", strings.ToUpper(m.cfg.RoomCodePrefix), randomCode(4))
		if _, exists := m.rooms[code]; !exists {
			return code
		}
	}
}

type Room struct {
	Code       string
	Title      string
	MapName    string
	ServerUUID string
	MaxPlayers int
	MaxUnits   int
	Income     float32
	CreatedAt  time.Time

	mu      sync.RWMutex
	InGame  bool
	players []*Session
}

func (r *Room) Summary() RoomSummary {
	return RoomSummary{
		Code:       r.Code,
		Title:      r.Title,
		MapName:    r.MapName,
		MaxPlayers: r.MaxPlayers,
		Players:    r.PlayerCount(),
		InGame:     r.IsInGame(),
		CreatedAt:  r.CreatedAt,
	}
}

func (r *Room) Join(session *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for idx, existing := range r.players {
		if existing == nil {
			r.players[idx] = session
			session.setRoom(r, idx)
			return nil
		}
	}
	return fmt.Errorf("room is full")
}

func (r *Room) Leave(session *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for idx, existing := range r.players {
		if existing == session {
			r.players[idx] = nil
			if r.PlayerCountLocked() == 0 {
				r.InGame = false
			}
			return
		}
	}
}

func (r *Room) PlayerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.PlayerCountLocked()
}

func (r *Room) PlayerCountLocked() int {
	count := 0
	for _, player := range r.players {
		if player != nil {
			count++
		}
	}
	return count
}

func (r *Room) SnapshotPlayers() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]*Session, len(r.players))
	copy(items, r.players)
	return items
}

func (r *Room) SetInGame(value bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.InGame = value
}

func (r *Room) IsInGame() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.InGame
}

func (r *Room) broadcast(packet protocol.Packet) {
	for _, player := range r.SnapshotPlayers() {
		if player == nil {
			continue
		}
		_ = player.Send(packet)
	}
}

func (r *Room) broadcastSystem(message string) {
	packet := buildChatPacket("SERVER", message, 5)
	r.broadcast(packet)
}

func (r *Room) broadcastChat(sender, message string) {
	packet := buildChatPacket(sender, message, 0)
	r.broadcast(packet)
}

func (r *Room) broadcastTeamList() error {
	for _, player := range r.SnapshotPlayers() {
		if player == nil {
			continue
		}
		if err := r.sendTeamListTo(player); err != nil {
			return err
		}
	}
	return nil
}

func (r *Room) sendTeamListTo(player *Session) error {
	packet, err := r.buildTeamListPacket(player)
	if err != nil {
		return err
	}
	return player.Send(packet)
}

func buildChatPacket(sender, message string, team int32) protocol.Packet {
	w := protocol.NewWriter()
	protocol.RequireNoError(w.String(message))
	w.Byte(3)
	protocol.RequireNoError(w.MaybeString(sender))
	w.Int32(team)
	w.Int32(team)
	return w.Packet(protocol.TypeChatBroadcast)
}

func (r *Room) buildTeamListPacket(player *Session) (protocol.Packet, error) {
	w := protocol.NewWriter()
	w.Int32(int32(max(player.Slot(), 0)))
	w.Bool(false)
	w.Int32(int32(r.MaxPlayers))

	players := r.SnapshotPlayers()
	if err := w.GzipSection("teams", func(section *protocol.Writer) error {
		for _, member := range players {
			if member == nil {
				section.Bool(false)
				continue
			}

			section.Bool(true)
			section.Raw(make([]byte, 13))
			if err := section.MaybeString(member.name); err != nil {
				return err
			}
			section.Raw(make([]byte, 32))
			if member.Slot() == 0 {
				section.Int32(1)
			} else {
				section.Int32(0)
			}
			for i := 0; i < 4; i++ {
				section.Bool(false)
			}
			section.Int32(0)
		}
		for i := len(players); i < r.MaxPlayers; i++ {
			section.Bool(false)
		}
		return nil
	}); err != nil {
		return protocol.Packet{}, err
	}

	w.Int32(2)
	w.Int32(0)
	w.Bool(true)
	w.Int32(1)
	w.Byte(5)
	w.Int32(int32(r.MaxUnits))
	w.Int32(int32(r.MaxUnits))
	w.Int32(1)
	w.Float32(r.Income)
	w.Bool(true)
	w.Bool(false)
	w.Bool(false)
	w.Bool(false)
	w.Bool(false)
	return w.Packet(protocol.TypeTeamList), nil
}

func randomCode(length int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "ROOM"
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "rwgin-room"
	}
	return hex.EncodeToString(buf)
}

func defaultIfBlank(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
