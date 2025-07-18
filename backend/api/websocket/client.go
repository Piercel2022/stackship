package websocket

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512
)

// Client represents a websocket client connection
type Client struct {
	// The websocket connection
	conn *websocket.Conn

	// The hub this client belongs to
	hub *Hub

	// Buffered channel of outbound messages
	send chan []byte

	// User ID associated with this client
	UserID string

	// Rooms this client has joined
	rooms map[string]bool

	// Client metadata
	IPAddress   string
	UserAgent   string
	ConnectedAt time.Time

	// Authentication status
	IsAuthenticated bool
	Permissions     []string
}

// NewClient creates a new websocket client
func NewClient(hub *Hub, conn *websocket.Conn, userID string, r *http.Request) *Client {
	return &Client{
		hub:             hub,
		conn:            conn,
		send:            make(chan []byte, 256),
		UserID:          userID,
		rooms:           make(map[string]bool),
		IPAddress:       getClientIP(r),
		UserAgent:       r.UserAgent(),
		ConnectedAt:     time.Now(),
		IsAuthenticated: true,       // Assuming auth is handled before websocket upgrade
		Permissions:     []string{}, // To be populated based on user role
	}
}

// ReadPump pumps messages from the websocket connection to the hub
func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.hub.logger.Error("Websocket error", "error", err)
			}
			break
		}

		// Handle incoming message
		c.handleMessage(message)
	}
}

// WritePump pumps messages from the hub to the websocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
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

// handleMessage processes incoming messages from the client
func (c *Client) handleMessage(message []byte) {
	var incomingEvent Event
	if err := json.Unmarshal(message, &incomingEvent); err != nil {
		c.hub.logger.Error("Failed to unmarshal client message", "error", err)
		c.SendError("Invalid message format")
		return
	}

	// Handle different message types
	switch incomingEvent.Type {
	case EventTypeJoinRoom:
		c.handleJoinRoom(incomingEvent.Data)
	case EventTypeLeaveRoom:
		c.handleLeaveRoom(incomingEvent.Data)
	case EventTypeSubscribeProject:
		c.handleSubscribeProject(incomingEvent.Data)
	case EventTypeUnsubscribeProject:
		c.handleUnsubscribeProject(incomingEvent.Data)
	case EventTypeSubscribeDeployment:
		c.handleSubscribeDeployment(incomingEvent.Data)
	case EventTypeUnsubscribeDeployment:
		c.handleUnsubscribeDeployment(incomingEvent.Data)
	case EventTypePong:
		// Handle pong response
		c.hub.logger.Debug("Received pong from client", "user_id", c.UserID)
	default:
		c.hub.logger.Warn("Unknown message type", "type", incomingEvent.Type)
		c.SendError("Unknown message type")
	}
}

// handleJoinRoom handles room join requests
func (c *Client) handleJoinRoom(data map[string]interface{}) {
	roomID, ok := data["room_id"].(string)
	if !ok {
		c.SendError("Invalid room_id")
		return
	}

	// Validate room access permissions
	if !c.canAccessRoom(roomID) {
		c.SendError("Access denied to room")
		return
	}

	c.hub.JoinRoom(c, roomID)
	c.SendSuccess("Joined room successfully")
}

// handleLeaveRoom handles room leave requests
func (c *Client) handleLeaveRoom(data map[string]interface{}) {
	roomID, ok := data["room_id"].(string)
	if !ok {
		c.SendError("Invalid room_id")
		return
	}

	c.hub.LeaveRoom(c, roomID)
	c.SendSuccess("Left room successfully")
}

// handleSubscribeProject handles project subscription requests
func (c *Client) handleSubscribeProject(data map[string]interface{}) {
	projectID, ok := data["project_id"].(string)
	if !ok {
		c.SendError("Invalid project_id")
		return
	}

	// Check if user has access to this project
	if !c.hasProjectAccess(projectID) {
		c.SendError("Access denied to project")
		return
	}

	// Join project room
	c.hub.JoinRoom(c, "project_"+projectID)
	c.SendSuccess("Subscribed to project updates")
}

// handleUnsubscribeProject handles project unsubscription requests
func (c *Client) handleUnsubscribeProject(data map[string]interface{}) {
	projectID, ok := data["project_id"].(string)
	if !ok {
		c.SendError("Invalid project_id")
		return
	}

	c.hub.LeaveRoom(c, "project_"+projectID)
	c.SendSuccess("Unsubscribed from project updates")
}

// handleSubscribeDeployment handles deployment subscription requests
func (c *Client) handleSubscribeDeployment(data map[string]interface{}) {
	deploymentID, ok := data["deployment_id"].(string)
	if !ok {
		c.SendError("Invalid deployment_id")
		return
	}

	// Check if user has access to this deployment
	if !c.hasDeploymentAccess(deploymentID) {
		c.SendError("Access denied to deployment")
		return
	}

	// Join deployment room
	c.hub.JoinRoom(c, "deployment_"+deploymentID)
	c.SendSuccess("Subscribed to deployment updates")
}

// handleUnsubscribeDeployment handles deployment unsubscription requests
func (c *Client) handleUnsubscribeDeployment(data map[string]interface{}) {
	deploymentID, ok := data["deployment_id"].(string)
	if !ok {
		c.SendError("Invalid deployment_id")
		return
	}

	c.hub.LeaveRoom(c, "deployment_"+deploymentID)
	c.SendSuccess("Unsubscribed from deployment updates")
}

// Send sends an event to the client
func (c *Client) Send(event *Event) {
	message, err := json.Marshal(event)
	if err != nil {
		c.hub.logger.Error("Failed to marshal event", "error", err)
		return
	}

	select {
	case c.send <- message:
	default:
		close(c.send)
	}
}

// SendError sends an error message to the client
func (c *Client) SendError(message string) {
	event := &Event{
		Type: EventTypeError,
		Data: map[string]interface{}{
			"message": message,
		},
		Timestamp: time.Now(),
	}
	c.Send(event)
}

// SendSuccess sends a success message to the client
func (c *Client) SendSuccess(message string) {
	event := &Event{
		Type: EventTypeSuccess,
		Data: map[string]interface{}{
			"message": message,
		},
		Timestamp: time.Now(),
	}
	c.Send(event)
}

// canAccessRoom checks if the client can access a specific room
func (c *Client) canAccessRoom(roomID string) bool {
	// Implement room access control logic
	// This could check user permissions, project membership, etc.

	// For now, allow access to all rooms for authenticated users
	return c.IsAuthenticated
}

// hasProjectAccess checks if the user has access to a specific project
func (c *Client) hasProjectAccess(projectID string) bool {
	// Implement project access control logic
	// This would typically check:
	// 1. User's role in the project
	// 2. Project visibility settings
	// 3. Team membership

	// For now, allow access for authenticated users
	return c.IsAuthenticated
}

// hasDeploymentAccess checks if the user has access to a specific deployment
func (c *Client) hasDeploymentAccess(deploymentID string) bool {
	// Implement deployment access control logic
	// This would typically check:
	// 1. User's access to the parent project
	// 2. Deployment visibility settings
	// 3. User's role permissions

	// For now, allow access for authenticated users
	return c.IsAuthenticated
}

// GetInfo returns client information
func (c *Client) GetInfo() map[string]interface{} {
	return map[string]interface{}{
		"user_id":          c.UserID,
		"ip_address":       c.IPAddress,
		"user_agent":       c.UserAgent,
		"connected_at":     c.ConnectedAt,
		"is_authenticated": c.IsAuthenticated,
		"rooms":            c.getRoomsList(),
		"permissions":      c.Permissions,
	}
}

// getRoomsList returns a list of rooms the client has joined
func (c *Client) getRoomsList() []string {
	rooms := make([]string, 0, len(c.rooms))
	for room := range c.rooms {
		rooms = append(rooms, room)
	}
	return rooms
}

// getClientIP extracts the client IP address from the request
func getClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Check for X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// Close closes the client connection
func (c *Client) Close() {
	c.conn.Close()
}

// IsConnected checks if the client is still connected
func (c *Client) IsConnected() bool {
	select {
	case <-c.send:
		return false
	default:
		return true
	}
}

// SendHeartbeat sends a heartbeat message to check connection
func (c *Client) SendHeartbeat() {
	event := &Event{
		Type:      EventTypeHeartbeat,
		Timestamp: time.Now(),
	}
	c.Send(event)
}

// UpdatePermissions updates the client's permissions
func (c *Client) UpdatePermissions(permissions []string) {
	c.Permissions = permissions
	c.hub.logger.Info("Updated client permissions", "user_id", c.UserID, "permissions", permissions)
}

// JoinProjectRoom joins a project-specific room
func (c *Client) JoinProjectRoom(projectID string) {
	if c.hasProjectAccess(projectID) {
		c.hub.JoinRoom(c, "project_"+projectID)
	}
}

// JoinDeploymentRoom joins a deployment-specific room
func (c *Client) JoinDeploymentRoom(deploymentID string) {
	if c.hasDeploymentAccess(deploymentID) {
		c.hub.JoinRoom(c, "deployment_"+deploymentID)
	}
}

// JoinMonitoringRoom joins a monitoring-specific room
func (c *Client) JoinMonitoringRoom(projectID string) {
	if c.hasProjectAccess(projectID) {
		c.hub.JoinRoom(c, "monitoring_"+projectID)
	}
}
