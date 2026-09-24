package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rwgin/internal/config"
	"rwgin/internal/game"
	"rwgin/internal/protocol"
)

type Server struct {
	cfg          config.Config
	logger       *log.Logger
	gameSessions *game.SessionManager
	listener     net.Listener
}

func NewServer(cfg config.Config, logger *log.Logger) *Server {
	return &Server{
		cfg:          cfg,
		logger:       logger,
		gameSessions: game.NewSessionManager(),
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.GameListenAddr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.logger.Printf("game listener started on %s", s.cfg.GameListenAddr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		session := newSession(s, conn)
		go session.run(ctx)
	}
}

type sessionState int32

const (
	stateInit sessionState = iota
	stateAwaitRegister
	stateAwaitRoomCode
	stateActive
	stateClosed
)

type Session struct {
	server *Server
	conn   net.Conn
	id     string

	send    chan protocol.Packet
	closeCh chan struct{}
	once    sync.Once

	state         atomic.Int32
	name          string
	playerID      string
	clientVersion int
	preregister   protocol.PreregisterInfo

	roomMu      sync.RWMutex
	gameSession *game.Session
	slot        int
}

var sessionCounter atomic.Uint64

// #region debug-point common:runtime-report
func debugReport(runID, hypothesisID, location, message string, data map[string]any) {
	envBytes, err := os.ReadFile(".dbg/rw-client-interop.env")
	if err != nil {
		return
	}
	url := "http://127.0.0.1:7777/event"
	sessionID := "rw-client-interop"
	for _, line := range strings.Split(string(envBytes), "\n") {
		if strings.HasPrefix(line, "DEBUG_SERVER_URL=") {
			url = strings.TrimPrefix(line, "DEBUG_SERVER_URL=")
		}
		if strings.HasPrefix(line, "DEBUG_SESSION_ID=") {
			sessionID = strings.TrimPrefix(line, "DEBUG_SESSION_ID=")
		}
	}
	body, err := json.Marshal(map[string]any{
		"sessionId":    sessionID,
		"runId":        runID,
		"hypothesisId": hypothesisID,
		"location":     location,
		"msg":          message,
		"data":         data,
		"ts":           time.Now().UnixMilli(),
	})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func shouldDebugPacket(packetType int32) bool {
	switch packetType {
	case protocol.TypePreregisterReceive,
		protocol.TypePreregister,
		protocol.TypeRegisterPlayer,
		protocol.TypeHeartbeat,
		protocol.TypeHeartbeatResponse,
		protocol.TypeDisconnect,
		protocol.TypeRelayVersionInfo,
		protocol.TypeRelayPrompt,
		protocol.TypeRelayPromptReply,
		protocol.TypeRelayBecomeServer,
		protocol.TypeServerInfo,
		protocol.TypeTeamList,
		protocol.TypeStartGame:
		return true
	default:
		return false
	}
}

func debugRunID() string {
	return "post-fix"
}

// #endregion

func newSession(server *Server, conn net.Conn) *Session {
	id := fmt.Sprintf("s-%d", sessionCounter.Add(1))
	session := &Session{
		server:  server,
		conn:    conn,
		id:      id,
		send:    make(chan protocol.Packet, 32),
		closeCh: make(chan struct{}),
		slot:    -1,
	}
	session.state.Store(int32(stateInit))
	return session
}

func (s *Session) run(ctx context.Context) {
	s.server.logger.Printf("client connected: %s", s.conn.RemoteAddr())
	// #region debug-point A:session-connect
	debugReport(debugRunID(), "A", "internal/relay/server.go:run", "[DEBUG] session connected", map[string]any{
		"sessionId": s.id,
		"remote":    s.conn.RemoteAddr().String(),
	})
	// #endregion

	go s.writeLoop()
	defer s.close()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closeCh:
			return
		default:
		}

		_ = s.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		packet, err := protocol.ReadPacket(s.conn)
		if err != nil {
			// #region debug-point E:read-error-close
			debugReport(debugRunID(), "E", "internal/relay/server.go:run", "[DEBUG] read loop ended", map[string]any{
				"sessionId": s.id,
				"state":     s.state.Load(),
				"error":     err.Error(),
			})
			// #endregion
			return
		}
		// #region debug-point A:packet-in
		if shouldDebugPacket(packet.Type) {
			debugReport(debugRunID(), "A", "internal/relay/server.go:run", "[DEBUG] inbound packet", map[string]any{
				"sessionId": s.id,
				"type":      packet.Type,
				"size":      len(packet.Body),
			})
		}
		// #endregion
		// #region debug-point E:active-packet-in
		if sessionState(s.state.Load()) == stateActive {
			debugReport(debugRunID(), "E", "internal/relay/server.go:run", "[DEBUG] active session inbound packet", map[string]any{
				"sessionId": s.id,
				"type":      packet.Type,
				"size":      len(packet.Body),
			})
		}
		// #endregion

		if err := s.handlePacket(packet); err != nil {
			s.server.logger.Printf("session %s handle packet %d failed: %v", s.id, packet.Type, err)
			// #region debug-point E:handle-error-close
			debugReport(debugRunID(), "E", "internal/relay/server.go:run", "[DEBUG] handle packet failed", map[string]any{
				"sessionId": s.id,
				"type":      packet.Type,
				"error":     err.Error(),
			})
			// #endregion
			return
		}
	}
}

func (s *Session) writeLoop() {
	for {
		select {
		case <-s.closeCh:
			return
		case packet := <-s.send:
			// #region debug-point D:packet-out
			if shouldDebugPacket(packet.Type) {
				debugReport(debugRunID(), "D", "internal/relay/server.go:writeLoop", "[DEBUG] outbound packet", map[string]any{
					"sessionId": s.id,
					"type":      packet.Type,
					"size":      len(packet.Body),
				})
			}
			// #endregion
			_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := protocol.WritePacket(s.conn, packet); err != nil {
				s.close()
				return
			}
		}
	}
}

func (s *Session) ID() string {
	return s.playerID
}

func (s *Session) Name() string {
	return s.name
}

func (s *Session) handlePacket(packet protocol.Packet) error {
	switch packet.Type {
	case protocol.TypePreregisterReceive:
		return s.handlePreregister(packet.Body)
	case protocol.TypeRegisterPlayer:
		return s.handleRegister(packet.Body)
	case protocol.TypeRelayPromptReply:
		return s.handlePromptReply(packet.Body)
	case protocol.TypeHeartbeat:
		return s.Send(protocol.Packet{Type: protocol.TypeHeartbeatResponse})
	case protocol.TypeChatReceive:
		return s.handleChat(packet.Body)
	case protocol.TypeStartGame:
		return s.handleStartGame(packet)
	case protocol.TypeReturnToBattleRoom:
		return s.handleReturnToBattleRoom(packet)
	case protocol.TypeGameCommand:
		return s.handleGameCommand(packet)
	case protocol.TypeDisconnect:
		return errors.New("client requested disconnect")
	default:
		return nil
	}
}

func (s *Session) handlePreregister(body []byte) error {
	if sessionState(s.state.Load()) != stateInit {
		return nil
	}

	info, err := protocol.DecodePreregister(body)
	if err != nil {
		return err
	}
	s.preregister = info
	s.clientVersion = int(info.ClientVersion)
	if s.clientVersion == 0 {
		s.clientVersion = s.server.cfg.DefaultClientVersion
	}
	// #region debug-point A:decode-preregister
	debugReport(debugRunID(), "A", "internal/relay/server.go:handlePreregister", "[DEBUG] preregister decoded", map[string]any{
		"sessionId":     s.id,
		"packetVersion": info.PacketVersion,
		"clientVersion": info.ClientVersion,
		"query":         info.Query,
		"playerName":    info.PlayerName,
	})
	// #endregion

	packet, err := s.buildPreregisterPacket(s.server.cfg.RelayHandshakeID)
	if err != nil {
		return err
	}
	if err := s.Send(packet); err != nil {
		return err
	}

	s.state.Store(int32(stateAwaitRegister))
	return nil
}

func (s *Session) handleRegister(body []byte) error {
	if sessionState(s.state.Load()) != stateAwaitRegister {
		decodedName, decodedPlayerID, decodeErr := protocol.DecodeRegister(body)
		// #region debug-point E:register-while-active
		debugReport(debugRunID(), "E", "internal/relay/server.go:handleRegister", "[DEBUG] register packet received outside await-register", map[string]any{
			"sessionId":       s.id,
			"state":           s.state.Load(),
			"decodedName":     decodedName,
			"decodedPlayerId": decodedPlayerID,
			"decodeError": func() string {
				if decodeErr != nil {
					return decodeErr.Error()
				}
				return ""
			}(),
		})
		// #endregion
		return nil
	}

	name, playerID, err := protocol.DecodeRegister(body)
	if err != nil {
		return err
	}
	s.name = firstNonBlank(name, s.preregister.PlayerName, "Player")
	s.playerID = firstNonBlank(playerID, s.id)
	// #region debug-point B:decode-register
	debugReport(debugRunID(), "B", "internal/relay/server.go:handleRegister", "[DEBUG] register decoded", map[string]any{
		"sessionId":     s.id,
		"name":          s.name,
		"playerId":      s.playerID,
		"query":         s.preregister.Query,
		"clientVersion": s.clientVersion,
	})
	// #endregion

	if err := s.Send(protocol.EncodeRelayVersionInfo(int32(s.clientVersion))); err != nil {
		return err
	}

	query := strings.TrimSpace(strings.ToUpper(s.preregister.Query))
	if query != "" && query != "RELAYCN" {
		return s.joinRoom(query)
	}

	s.state.Store(int32(stateAwaitRoomCode))
	promptPacket, promptErr := protocol.EncodePrompt("请输入房间号，留空会自动创建新房间。")
	if promptErr != nil {
		return promptErr
	}
	return s.Send(promptPacket)
}

func (s *Session) handlePromptReply(body []byte) error {
	if sessionState(s.state.Load()) != stateAwaitRoomCode {
		return nil
	}

	roomCode, err := protocol.DecodePromptReply(body)
	if err != nil {
		return err
	}
	// #region debug-point C:prompt-room-code
	debugReport(debugRunID(), "C", "internal/relay/server.go:handlePromptReply", "[DEBUG] prompt reply decoded", map[string]any{
		"sessionId": s.id,
		"roomCode":  roomCode,
	})
	// #endregion
	return s.joinRoom(roomCode)
}

func (s *Session) handleChat(body []byte) error {
	gameSession := s.GameSession()
	if gameSession == nil {
		return nil
	}

	message, err := protocol.DecodeChatReceive(body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return nil
	}

	player := gameSession.GetPlayer(game.PlayerID(s.playerID))
	if player == nil {
		return nil // Player not found in session
	}

	gameSession.HandleChatMessage(player, message)
	return nil
}

func (s *Session) handleStartGame(packet protocol.Packet) error {
	gameSession := s.GameSession()
	if gameSession == nil {
		return nil
	}

	player := gameSession.GetPlayer(game.PlayerID(s.playerID))
	if player == nil {
		return nil // Player not found in session
	}

	gameSession.HandleStartGame(player)
	return nil
}

func (s *Session) handleReturnToBattleRoom(packet protocol.Packet) error {
	gameSession := s.GameSession()
	if gameSession == nil {
		return nil
	}

	// TODO: Implement return to battle room logic in game.Session
	s.server.logger.Printf("player %s is returning to battle room in session %s", s.name, gameSession.ID)

	return nil
}

func (s *Session) handleGameCommand(packet protocol.Packet) error {
	gameSession := s.GameSession()
	if gameSession == nil {
		return nil
	}

	player := gameSession.GetPlayer(game.PlayerID(s.playerID))
	if player == nil {
		return nil // Player not found in session
	}

	gameSession.SubmitCommand(player, packet)
	return nil
}

func (s *Session) joinRoom(code string) error {
	gameSession := s.server.gameSessions.CreateSession(code)
	gameSession.AddPlayer(&game.Player{
		ID:   game.PlayerID(s.playerID),
		Name: s.name,
		Conn: s, // s (the relay.Session) implements game.PlayerConn
	})
	s.gameSession = gameSession

	s.server.logger.Printf("player %s joined game session %s", s.name, code)

	s.state.Store(int32(stateActive))
	return nil
}

func (s *Session) Send(packet protocol.Packet) error {
	select {
	case <-s.closeCh:
		return net.ErrClosed
	case s.send <- packet:
		return nil
	}
}

func (s *Session) close() {
	s.once.Do(func() {
		gameSession := s.GameSession()
		// #region debug-point E:session-close
		debugReport(debugRunID(), "E", "internal/relay/server.go:close", "[DEBUG] session closing", map[string]any{
			"sessionId": s.id,
			"playerId":  s.playerID,
			"name":      s.name,
			"slot":      s.Slot(),
			"state":     s.state.Load(),
			"roomCode": func() string {
				if gameSession != nil {
					return gameSession.ID
				}
				return ""
			}(),
		})
		// #endregion
		s.state.Store(int32(stateClosed))
		close(s.closeCh)
		_ = s.conn.Close()

		if gameSession != nil {
			gameSession.RemovePlayer(game.PlayerID(s.playerID))
			s.server.logger.Printf("player %s removed from game session %s", s.name, gameSession.ID)
		}

		s.server.logger.Printf("client disconnected: %s", s.conn.RemoteAddr())
	})
}

func (s *Session) setGameSession(session *game.Session, slot int) {
	s.roomMu.Lock()
	defer s.roomMu.Unlock()
	s.gameSession = session
	s.slot = slot
}

func (s *Session) GameSession() *game.Session {
	s.roomMu.RLock()
	defer s.roomMu.RUnlock()
	return s.gameSession
}

func (s *Session) Slot() int {
	s.roomMu.RLock()
	defer s.roomMu.RUnlock()
	return s.slot
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func javaHash(s string) int32 {
	var h int32
	for _, r := range s {
		h = 31*h + int32(r)
	}
	return h
}
