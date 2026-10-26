package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/charltonadam/tilefall/internal/scene"
)

// WebSocket protocol (JSON text messages; see web/src/lib/protocol.ts):
//
//	client -> server
//	  {"t":"cmd",   "id":N, "cmd":{"type":...}}        apply one command
//	  {"t":"batch", "id":N, "cmds":[{"type":...},...]} apply atomically
//	  {"t":"undo",  "id":N}
//	  {"t":"sync"}                                     request a fresh snapshot
//	server -> client
//	  {"t":"hello", "role":"dm"|"player"}
//	  {"t":"snapshot", "scene":{...}}
//	  {"t":"library", "templates":[...]}             DM only: the whole token library,
//	                                                 on connect and after every change
//	  {"t":"patch", "patch":{...}}
//	  {"t":"ack", "id":N, "ok":true, "version":V, "result":...}
//	  {"t":"ack", "id":N, "ok":false, "error":"..."}
//
// Acks and patches travel independently, so a client must not assume the
// patch for its command arrives before or after the ack.

type clientMsg struct {
	T    string            `json:"t"`
	ID   int64             `json:"id"`
	Cmd  json.RawMessage   `json:"cmd"`
	Cmds []json.RawMessage `json:"cmds"`
}

type ackMsg struct {
	T       string `json:"t"`
	ID      int64  `json:"id"`
	OK      bool   `json:"ok"`
	Version uint64 `json:"version,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

const (
	roleDM     = "dm"
	rolePlayer = "player"
)

// playerCommands are the commands a player connection may send.
var playerCommands = map[string]bool{"move_token": true}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role != roleDM {
		role = rolePlayer
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// allow the Vite dev server, which proxies /ws from another port
		OriginPatterns: []string{"localhost:5173", "127.0.0.1:5173"},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub, snap := s.g.Table.Subscribe()
	defer s.g.Table.Unsubscribe(sub)

	send := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, data)
	}
	if send(map[string]string{"t": "hello", "role": role}) != nil ||
		send(map[string]json.RawMessage{"t": json.RawMessage(`"snapshot"`), "scene": snap}) != nil {
		return
	}

	// the token library is edited over REST; DM connections get it pushed
	var libC <-chan struct{}
	if role == roleDM {
		var unsub func()
		libC, unsub = s.g.Library.Subscribe()
		defer unsub()
		if send(map[string]any{"t": "library", "templates": s.g.Library.List()}) != nil {
			return
		}
	}

	go func() {
		defer cancel()
		for {
			var ev scene.Event
			select {
			case <-libC:
				if send(map[string]any{"t": "library", "templates": s.g.Library.List()}) != nil {
					return
				}
				continue
			case e, ok := <-sub.C:
				if !ok {
					// channel closed: we fell behind (or are shutting down); the
					// client reconnects and gets a fresh snapshot
					conn.Close(websocket.StatusTryAgainLater, "resync")
					return
				}
				ev = e
			}
			key := "patch"
			if ev.Kind == "snapshot" {
				key = "scene"
			}
			msg := map[string]json.RawMessage{"t": json.RawMessage(`"` + ev.Kind + `"`), key: ev.Data}
			if send(msg) != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				log.Printf("ws read: %v", err)
			}
			return
		}
		var m clientMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		var ack *ackMsg
		switch m.T {
		case "cmd":
			ack = s.applyFromClient(role, m.ID, []json.RawMessage{m.Cmd})
		case "batch":
			ack = s.applyFromClient(role, m.ID, m.Cmds)
		case "undo":
			if role != roleDM {
				ack = &ackMsg{ID: m.ID, Error: "only the DM can undo"}
			} else if v, err := s.g.Table.Undo(); err != nil {
				ack = &ackMsg{ID: m.ID, Error: err.Error()}
			} else {
				ack = &ackMsg{ID: m.ID, OK: true, Version: v}
			}
		case "sync":
			if send(map[string]json.RawMessage{"t": json.RawMessage(`"snapshot"`), "scene": s.g.Table.Snapshot()}) != nil {
				return
			}
		}
		if ack != nil {
			ack.T = "ack"
			if send(ack) != nil {
				return
			}
		}
	}
}

func (s *Server) applyFromClient(role string, id int64, raws []json.RawMessage) *ackMsg {
	cmds := make([]scene.Command, 0, len(raws))
	for _, raw := range raws {
		c, err := scene.DecodeCommand(raw)
		if err != nil {
			return &ackMsg{ID: id, Error: err.Error()}
		}
		if role != roleDM && !playerCommands[c.Type()] {
			return &ackMsg{ID: id, Error: "players cannot " + c.Type()}
		}
		cmds = append(cmds, c)
	}
	res, err := s.g.ApplyBatch(cmds)
	if err != nil {
		return &ackMsg{ID: id, Error: err.Error()}
	}
	return &ackMsg{ID: id, OK: true, Version: res.Version, Result: res.Result}
}
