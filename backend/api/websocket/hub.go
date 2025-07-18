package websocket

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stackship/backend/utils"
)

// Hub maintains the set of active clients and broadcasts messages to the clients
type Hub struct {
	// Registered clients
	clients map[*Client]bool

	// Inbound messages from the clients
	broadcast chan []byte

	// Register requests from the clients
	register chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Room-based messaging for project-specific updates
	rooms map[string]map[*Client]bool

	// Mutex for thread-safe operations
	mu sync.RWMutex

	// Logger
	logger *utils.Logger

	// Context for graceful shutdown
	ctx context.Context
	cancel context.CancelFunc
}

// NewHub creates a new WebSocket hub
func NewHub(logger *utils.Logger) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rooms:      make(map[string]map[*Client]bool),
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Run starts the hub and handles client registration/unregistration
func (h *Hub) Run() {
	h.logger.Info("Starting WebSocket hub")
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			h.logger.Info("Shutting down WebSocket hub")
			return

		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)

		case <-ticker.C:
			h.pingClients()
		}
	}
}

// registerClient adds a client to the hub
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client] = true
	h.logger.Info("Client registered", "user_id", client.UserID, "total_clients", len(h.clients))

	// Send welcome message
	welcomeEvent := &Event{
		Type:      EventTypeConnection,
		Data:      map[string]interface{}{"status": "connected"},
		Timestamp: time.Now(),
	}
	client.Send(welcomeEvent)
}

// unregisterClient removes a client from the hub
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
		
		// Remove from all rooms
		for roomID, room := range h.rooms {
			if _, exists := room[client]; exists {
				delete(room, client)
				if len(room) == 0 {
					delete(h.rooms, roomID)
				}
			}
		}
		
		h.logger.Info("Client unregistered", "user_id", client.UserID, "total_clients", len(h.clients))
	}
}

// broadcastMessage sends a message to all connected clients
func (h *Hub) broadcastMessage(message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- message:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// pingClients sends ping messages to all clients to keep connections alive
func (h *Hub) pingClients() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	pingEvent := &Event{
		Type:      EventTypePing,
		Timestamp: time.Now(),
	}

	for client := range h.clients {
		client.Send(pingEvent)
	}
}

// JoinRoom adds a client to a specific room (e.g., project-specific room)
func (h *Hub) JoinRoom(client *Client, roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[*Client]bool)
	}
	
	h.rooms[roomID][client] = true
	client.rooms[roomID] = true
	
	h.logger.Info("Client joined room", "user_id", client.UserID, "room_id", roomID)
}

// LeaveRoom removes a client from a specific room
func (h *Hub) LeaveRoom(client *Client, roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if room, exists := h.rooms[roomID]; exists {
		delete(room, client)
		delete(client.rooms, roomID)
		
		// Clean up empty rooms
		if len(room) == 0 {
			delete(h.rooms, roomID)
		}
		
		h.logger.Info("Client left room", "user_id", client.UserID, "room_id", roomID)
	}
}

// BroadcastToRoom sends a message to all clients in a specific room
func (h *Hub) BroadcastToRoom(roomID string, event *Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	room, exists := h.rooms[roomID]
	if !exists {
		return
	}

	message, err := json.Marshal(event)
	if err != nil {
		h.logger.Error("Failed to marshal event", "error", err)
		return
	}

	for client := range room {
		select {
		case client.send <- message:
		default:
			close(client.send)
			delete(h.clients, client)
			delete(room, client)
		}
	}
}

// BroadcastToUser sends a message to all connections of a specific user
func (h *Hub) BroadcastToUser(userID string, event *Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	message, err := json.Marshal(event)
	if err != nil {
		h.logger.Error("Failed to marshal event", "error", err)
		return
	}

	for client := range h.clients {
		if client.UserID == userID {
			select {
			case client.send <- message:
			default:
				close(client.send)
				delete(h.clients, client)
			}
		}
	}
}

// BroadcastEvent sends an event to all connected clients
func (h *Hub) BroadcastEvent(event *Event) {
	message, err := json.Marshal(event)
	if err != nil {
		h.logger.Error("Failed to marshal event", "error", err)
		return
	}

	select {
	case h.broadcast <- message:
	default:
		h.logger.Warn("Broadcast channel full, dropping message")
	}
}

// GetStats returns hub statistics
func (h *Hub) GetStats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	roomStats := make(map[string]int)
	for roomID, room := range h.rooms {
		roomStats[roomID] = len(room)
	}

	return map[string]interface{}{
		"total_clients": len(h.clients),
		"total_rooms":   len(h.rooms),
		"rooms":         roomStats,
	}
}

// Shutdown gracefully shuts down the hub
func (h *Hub) Shutdown() {
	h.logger.Info("Shutting down WebSocket hub")
	
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close all client connections
	for client := range h.clients {
		close(client.send)
	}

	// Cancel context to stop the run loop
	h.cancel()
}

// WebSocket upgrader with proper configuration
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, implement proper origin checking
		return true
	},
}

// ServeWS handles websocket requests from clients
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, userID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade connection", "error", err)
		return
	}

	client := &Client{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 256),
		UserID: userID,
		rooms:  make(map[string]bool),
	}

	client.hub.register <- client

	// Start goroutines for reading and writing
	go client.WritePump()
	go client.ReadPump()
}

// NotifyDeploymentUpdate sends deployment updates to relevant clients
func (h *Hub) NotifyDeploymentUpdate(projectID string, deployment interface{}) {
	event := &Event{
		Type: EventTypeDeploymentUpdate,
		Data: map[string]interface{}{
			"project_id":  projectID,
			"deployment":  deployment,
		},
		Timestamp: time.Now(),
	}

	// Broadcast to project room
	h.BroadcastToRoom("project_"+projectID, event)
}

// NotifyLogUpdate sends log updates to relevant clients
func (h *Hub) NotifyLogUpdate(projectID string, deploymentID string, logEntry interface{}) {
	event := &Event{
		Type: EventTypeLogUpdate,
		Data: map[string]interface{}{
			"project_id":    projectID,
			"deployment_id": deploymentID,
			"log_entry":     logEntry,
		},
		Timestamp: time.Now(),
	}

	// Broadcast to deployment-specific room
	h.BroadcastToRoom("deployment_"+deploymentID, event)
}

// NotifyMetricsUpdate sends metrics updates to monitoring clients
func (h *Hub) NotifyMetricsUpdate(projectID string, metrics interface{}) {
	event := &Event{
		Type: EventTypeMetricsUpdate,
		Data: map[string]interface{}{
			"project_id": projectID,
			"metrics":    metrics,
		},
		Timestamp: time.Now(),
	}

	// Broadcast to project monitoring room
	h.BroadcastToRoom("monitoring_"+projectID, event)
}