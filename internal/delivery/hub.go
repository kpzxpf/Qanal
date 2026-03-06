package delivery

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

type client struct {
	conn *websocket.Conn
	send chan []byte
	code string
	hub  *Hub
}

// Hub manages WebSocket clients grouped by transfer code.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*client]struct{}
	reg     chan *client
	unreg   chan *client
	bcast   chan broadcast
}

type broadcast struct {
	code string
	data []byte
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]map[*client]struct{}),
		reg:     make(chan *client, 64),
		unreg:   make(chan *client, 64),
		bcast:   make(chan broadcast, 512),
	}
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.reg:
			h.mu.Lock()
			if h.clients[c.code] == nil {
				h.clients[c.code] = make(map[*client]struct{})
			}
			h.clients[c.code][c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unreg:
			h.mu.Lock()
			if set, ok := h.clients[c.code]; ok {
				delete(set, c)
				if len(set) == 0 {
					delete(h.clients, c.code)
				}
			}
			h.mu.Unlock()
			close(c.send)

		case b := <-h.bcast:
			h.mu.RLock()
			set := h.clients[b.code]
			h.mu.RUnlock()
			for c := range set {
				select {
				case c.send <- b.data:
				default:
					// slow client — drop message
				}
			}
		}
	}
}

func (h *Hub) Broadcast(code string, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case h.bcast <- broadcast{code: code, data: data}:
	default:
		slog.Warn("hub broadcast channel full, dropping message", "code", code)
	}
}

func (h *Hub) ServeWS(conn *websocket.Conn, code string) {
	c := &client{conn: conn, send: make(chan []byte, 64), code: code, hub: h}
	h.reg <- c

	go c.writePump()
	c.readPump()

	h.unreg <- c
}

func (c *client) readPump() {
	defer c.conn.Close()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
