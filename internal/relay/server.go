package relay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rwgin/internal/config"
	"rwgin/internal/protocol"
)

type Server struct {
	cfg      config.Config
	logger   *log.Logger
	rooms    *Manager
	listener net.Listener
}

func NewServer(cfg config.Config, logger *log.Logger) *Server {
	return &Server{
		cfg:    cfg,
		logger: logger,
		rooms:  NewManager(cfg),
	}
}

func (s *Server) Rooms() *Manager {
	return s.rooms
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

	roomMu sync.RWMutex
	room   *Room
	slot   int
}

var sessionCounter atomic.Uint64

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
			return
		}

		if err := s.handlePacket(packet); err != nil {
			s.server.logger.Printf("session %s handle packet %d failed: %v", s.id, packet.Type, err)
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
			_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := protocol.WritePacket(s.conn, packet); err != nil {
				s.close()
				return
			}
		}
	}
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
		return nil
	}

	name, playerID, err := protocol.DecodeRegister(body)
	if err != nil {
		return err
	}
	s.name = firstNonBlank(name, s.preregister.PlayerName, "Player")
	s.playerID = firstNonBlank(playerID, s.id)

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
	return s.joinRoom(roomCode)
}

func (s *Session) handleChat(body []byte) error {
	room := s.Room()
	if room == nil {
		return nil
	}

	message, err := protocol.DecodeChatReceive(body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return nil
	}

	if strings.HasPrefix(strings.TrimSpace(message), "/") {
		return s.handleRoomCommand(room, strings.TrimSpace(message))
	}

	room.broadcastChat(s.name, message)
	return nil
}

func (s *Session) handleStartGame(packet protocol.Packet) error {
	room := s.Room()
	if room == nil {
		return nil
	}
	if !room.CanModerate(s) {
		s.systemMessage("只有房主或管理员可以开始游戏。")
		return nil
	}
	room.SetInGame(true)
	room.broadcastSystem(fmt.Sprintf("%s 开始了游戏", s.name))
	room.broadcast(packet)
	return nil
}

func (s *Session) handleReturnToBattleRoom(packet protocol.Packet) error {
	room := s.Room()
	if room == nil {
		return nil
	}
	room.SetInGame(false)
	room.broadcast(packet)
	_ = room.broadcastTeamList()
	return nil
}

func (s *Session) joinRoom(code string) error {
	room, created, err := s.server.rooms.JoinOrCreate(code, s)
	if err != nil {
		return err
	}

	s.state.Store(int32(stateActive))

	if err := s.Send(s.buildRelayBecomeServerPacket(room)); err != nil {
		return err
	}
	roomScopedPrereq, preregisterErr := s.buildPreregisterPacket(room.ServerUUID)
	if preregisterErr != nil {
		return preregisterErr
	}
	if err := s.Send(roomScopedPrereq); err != nil {
		return err
	}
	if err := s.Send(s.buildServerInfoPacket(room)); err != nil {
		return err
	}
	if err := room.sendTeamListTo(s); err != nil {
		return err
	}

	if created {
		room.broadcastSystem(fmt.Sprintf("房间 %s 已创建", room.Code))
	} else {
		room.broadcastSystem(fmt.Sprintf("%s 加入了房间", s.name))
	}
	return room.broadcastTeamList()
}

func (s *Session) handleRoomCommand(room *Room, message string) error {
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return nil
	}

	switch strings.ToLower(fields[0]) {
	case "/help":
		s.systemMessage("可用命令: /players, /kick <槽位|玩家名>, /owner <槽位|玩家名>, /admin add|remove <槽位|玩家名>")
	case "/players", "/list":
		s.systemMessage(room.MembersText())
	case "/kick":
		if len(fields) < 2 {
			s.systemMessage("用法: /kick <槽位|玩家名>")
			return nil
		}
		target, err := room.FindTarget(strings.Join(fields[1:], " "))
		if err != nil {
			s.systemMessage(err.Error())
			return nil
		}
		if target == s {
			s.systemMessage("不能踢出自己。")
			return nil
		}
		if err := room.Kick(s, target); err != nil {
			s.systemMessage(err.Error())
			return nil
		}
		room.broadcastSystem(fmt.Sprintf("%s 移出了 %s", s.name, target.name))
	case "/owner", "/transfer":
		if len(fields) < 2 {
			s.systemMessage("用法: /owner <槽位|玩家名>")
			return nil
		}
		target, err := room.FindTarget(strings.Join(fields[1:], " "))
		if err != nil {
			s.systemMessage(err.Error())
			return nil
		}
		if err := room.TransferOwnership(s, target); err != nil {
			s.systemMessage(err.Error())
			return nil
		}
		room.broadcastSystem(fmt.Sprintf("%s 将房主转移给了 %s", s.name, target.name))
		_ = room.broadcastTeamList()
	case "/admin":
		if len(fields) < 3 {
			s.systemMessage("用法: /admin add|remove <槽位|玩家名>")
			return nil
		}
		action := strings.ToLower(fields[1])
		target, err := room.FindTarget(strings.Join(fields[2:], " "))
		if err != nil {
			s.systemMessage(err.Error())
			return nil
		}
		switch action {
		case "add":
			if err := room.SetAdmin(s, target, true); err != nil {
				s.systemMessage(err.Error())
				return nil
			}
			room.broadcastSystem(fmt.Sprintf("%s 设定 %s 为管理员", s.name, target.name))
			_ = room.broadcastTeamList()
		case "remove":
			if err := room.SetAdmin(s, target, false); err != nil {
				s.systemMessage(err.Error())
				return nil
			}
			room.broadcastSystem(fmt.Sprintf("%s 取消了 %s 的管理员", s.name, target.name))
			_ = room.broadcastTeamList()
		default:
			s.systemMessage("用法: /admin add|remove <槽位|玩家名>")
		}
	default:
		s.systemMessage("未知房间命令，使用 /help 查看可用命令。")
	}

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

func (s *Session) systemMessage(message string) {
	room := s.Room()
	sender := "SERVER"
	if room != nil {
		sender = "ROOM"
	}
	_ = s.Send(buildChatPacket(sender, message, 5))
}

func (s *Session) close() {
	s.once.Do(func() {
		s.state.Store(int32(stateClosed))
		close(s.closeCh)
		_ = s.conn.Close()

		room := s.Room()
		if room != nil {
			room.Leave(s)
			_ = room.broadcastTeamList()
			room.broadcastSystem(fmt.Sprintf("%s 离开了房间", s.name))
			s.server.rooms.RemoveIfEmpty(room)
		}

		s.server.logger.Printf("client disconnected: %s", s.conn.RemoteAddr())
	})
}

func (s *Session) setRoom(room *Room, slot int) {
	s.roomMu.Lock()
	defer s.roomMu.Unlock()
	s.room = room
	s.slot = slot
}

func (s *Session) Room() *Room {
	s.roomMu.RLock()
	defer s.roomMu.RUnlock()
	return s.room
}

func (s *Session) Slot() int {
	s.roomMu.RLock()
	defer s.roomMu.RUnlock()
	return s.slot
}

func (s *Session) buildPreregisterPacket(uuid string) (protocol.Packet, error) {
	w := protocol.NewWriter()
	if err := w.String(s.server.cfg.ServerID); err != nil {
		return protocol.Packet{}, err
	}
	w.Int32(1)
	w.Int32(int32(s.clientVersion))
	w.Int32(int32(s.clientVersion))
	if err := w.String("com.corrodinggames.rts.server"); err != nil {
		return protocol.Packet{}, err
	}
	if err := w.String(uuid); err != nil {
		return protocol.Packet{}, err
	}
	w.Int32(javaHash("rwgin"))
	return w.Packet(protocol.TypePreregister), nil
}

func (s *Session) buildRelayBecomeServerPacket(room *Room) protocol.Packet {
	w := protocol.NewWriter()
	w.Byte(2)
	w.Bool(true)
	w.Bool(true)
	w.Bool(true)
	protocol.RequireNoError(w.String(room.ServerUUID))
	w.Bool(false)
	w.Bool(false)
	w.Bool(true)
	protocol.RequireNoError(w.String(fmt.Sprintf("Room ID: %s\nRoom Name: %s", room.Code, room.Title)))
	w.Bool(false)
	protocol.RequireNoError(w.MaybeString(s.playerID))
	return w.Packet(protocol.TypeRelayBecomeServer)
}

func (s *Session) buildServerInfoPacket(room *Room) protocol.Packet {
	w := protocol.NewWriter()
	protocol.RequireNoError(w.String(s.server.cfg.ServerID))
	w.Int32(int32(s.clientVersion))
	w.Int32(0)
	protocol.RequireNoError(w.String(room.MapName))
	w.Int32(0)
	w.Int32(2)
	w.Bool(true)
	w.Int32(1)
	w.Byte(0)
	w.Bool(false)
	w.Bool(false)
	return w.Packet(protocol.TypeServerInfo)
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
