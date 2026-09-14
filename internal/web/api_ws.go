package web

import (
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"mockcam/internal/logger"
)

// wsClientMessage is what the dashboard sends over /ws.
type wsClientMessage struct {
	Action string  `json:"action"` // "move" (absolute) or "stop"
	Pan    float64 `json:"pan"`
	Tilt   float64 `json:"tilt"`
	Zoom   float64 `json:"zoom"`
}

// handleWS upgrades the connection, pushes the initial PTZ state, then
// relays PTZ commands from the client until it disconnects. Broadcasts
// (PTZ updates, log entries) are delivered by broadcastPTZ/broadcastLog.
func (h *APIHandler) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[api] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	h.addWSClient(conn)
	defer h.removeWSClient(conn)

	_ = h.writeWS(conn, h.ptzMessage())

	for {
		var msg wsClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch strings.ToLower(msg.Action) {
		case "move":
			h.ptz.AbsoluteMove(msg.Pan, msg.Tilt, msg.Zoom)
		case "stop":
			h.ptz.Stop()
		}
	}
}

func (h *APIHandler) addWSClient(conn *websocket.Conn) {
	h.wsMu.Lock()
	h.wsClients[conn] = true
	h.wsMu.Unlock()
}

func (h *APIHandler) removeWSClient(conn *websocket.Conn) {
	h.wsMu.Lock()
	delete(h.wsClients, conn)
	h.wsMu.Unlock()
}

// WSClientCount returns the number of connected dashboard sockets.
func (h *APIHandler) WSClientCount() int {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	return len(h.wsClients)
}

// writeWS serialises writes; gorilla/websocket allows one concurrent writer.
func (h *APIHandler) writeWS(conn *websocket.Conn, msg any) error {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	return conn.WriteJSON(msg)
}

func (h *APIHandler) ptzMessage() map[string]any {
	pan, tilt, zoom, moving := h.ptz.GetStatus()
	return map[string]any{
		"type":      "ptz",
		"pan":       pan,
		"tilt":      tilt,
		"zoom":      zoom,
		"is_moving": moving,
	}
}

// broadcast sends msg to every client, dropping clients whose write fails.
func (h *APIHandler) broadcast(msg any) {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	for conn := range h.wsClients {
		if err := conn.WriteJSON(msg); err != nil {
			_ = conn.Close()
			delete(h.wsClients, conn)
		}
	}
}

func (h *APIHandler) broadcastPTZ(pan, tilt, zoom float64, isMoving bool) {
	h.broadcast(map[string]any{
		"type":      "ptz",
		"pan":       pan,
		"tilt":      tilt,
		"zoom":      zoom,
		"is_moving": isMoving,
	})
}

func (h *APIHandler) broadcastLog(entry logger.LogEntry) {
	h.broadcast(map[string]any{
		"type":  "log",
		"entry": entry,
	})
}
