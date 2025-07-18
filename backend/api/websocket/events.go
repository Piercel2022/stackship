package websocket
package websocket

import (
	"encoding/json"
	"time"
)

// Event types for WebSocket communication
const (
	// Connection events
	EventTypeConnection    = "connection"
	EventTypeDisconnection = "disconnection"
	EventTypePing          = "ping"
	EventTypePong          = "pong"
	EventTypeHeartbeat     = "heartbeat"
	EventTypeError         = "error"
	EventTypeSuccess       = "success"

	// Room management events
	EventTypeJoinRoom    = "join_room"
	EventTypeLeaveRoom   = "leave_room"
	EventTypeRoomJoined  = "room_joined"
	EventTypeRoomLeft    = "room_left"

	// Project events
	EventTypeProjectCreated      = "project_created"
	EventTypeProjectUpdated      = "project_updated"
	EventTypeProjectDeleted      = "project_deleted"
	EventTypeProjectStatusChange = "project_status_change"
	EventTypeSubscribeProject    = "subscribe_project"
	EventTypeUnsubscribeProject  = "unsubscribe_project"

	// Deployment events
	EventTypeDeploymentStarted          = "deployment_started"
	EventTypeDeploymentUpdate           = "deployment_update"
	EventTypeDeploymentCompleted        = "deployment_completed"
	EventTypeDeploymentFailed           = "deployment_failed"
	EventTypeDeploymentCancelled        = "deployment_cancelled"
	EventTypeDeploymentRollback         = "deployment_rollback"
	EventTypeDeploymentStatusChange     = "deployment_status_change"
	EventTypeSubscribeDeployment        = "subscribe_deployment"
	EventTypeUnsubscribeDeployment      = "unsubscribe_deployment"

	// Build events
	EventTypeBuildStarted    = "build_started"
	EventTypeBuildUpdate     = "build_update"
	EventTypeBuildCompleted  = "build_completed"
	EventTypeBuildFailed     = "build_failed"
	EventTypeBuildCancelled  = "build_cancelled"

	// Log events
	EventTypeLogUpdate     = "log_update"
	EventTypeLogStreamEnd  = "log_stream_end"
	EventTypeLogError      = "log_error"

	// Monitoring events
	EventTypeMetricsUpdate   = "metrics_update"
	EventTypeAlertTriggered  = "alert_triggered"
	EventTypeAlertResolved   = "alert_resolved"
	EventTypeHealthCheck     = "health_check"

	// User events
	EventTypeUserJoined     = "user_joined"
	EventTypeUserLeft       = "user_left"
	EventTypeUserActivity   = "user_activity"

	// System events
	EventTypeSystemAlert       = "system_alert"
	EventTypeSystemMaintenance = "system_maintenance"
	EventTypeSystemUpdate      = "system_update"

	// Notification events
	EventTypeNotification = "notification"
)

// Event severity levels
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
	SeverityCritical = "critical"
)

// Event represents a WebSocket event
type Event struct {
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Severity  string                 `json:"severity,omitempty"`
	UserID    string                 `json:"user_id,omitempty"`
	ProjectID string                 `json:"project_id,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
}

// NewEvent creates a new event
func NewEvent(eventType string, data map[string]interface{}) *Event {
	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
	}
}

// NewEventWithSeverity creates a new event with severity
func NewEventWithSeverity(eventType string, data map[string]interface{}, severity string) *Event {
	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		Severity:  severity,
	}
}

// NewProjectEvent creates a new project-related event
func NewProjectEvent(eventType string, projectID string, data map[string]interface{}) *Event {
	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		ProjectID: projectID,
	}
}

// NewDeploymentEvent creates a new deployment-related event
func NewDeploymentEvent(eventType string, projectID string, deploymentID string, data map[string]interface{}) *Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["deployment_id"] = deploymentID

	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		ProjectID: projectID,
	}
}

// NewUserEvent creates a new user-related event
func NewUserEvent(eventType string, userID string, data map[string]interface{}) *Event {
	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		UserID:    userID,
	}
}

// NewBuildEvent creates a new build-related event
func NewBuildEvent(eventType string, projectID string, buildID string, data map[string]interface{}) *Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["build_id"] = buildID

	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		ProjectID: projectID,
	}
}

// NewLogEvent creates a new log-related event
func NewLogEvent(eventType string, projectID string, logLevel string, message string, data map[string]interface{}) *Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["log_level"] = logLevel
	data["message"] = message

	return &Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
		ProjectID: projectID,
	}
}

// NewMetricsEvent creates a new metrics-related event
func NewMetricsEvent(eventType string, projectID string, metrics map[string]interface{}) *Event {
	return &Event{
		Type:      eventType,
		Data:      metrics,
		Timestamp: time.Now(),
		ProjectID: projectID,
	}
}

// NewNotificationEvent creates a new notification event
func NewNotificationEvent(userID string, title string, message string, severity string) *Event {
	data := map[string]interface{}{
		"title":   title,
		"message": message,
	}

	return &Event{
		Type:      EventTypeNotification,
		Data:      data,
		Timestamp: time.Now(),
		UserID:    userID,
		Severity:  severity,
	}
}

// ToJSON converts the event to JSON
func (e *Event) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// FromJSON creates an event from JSON
func FromJSON(data []byte) (*Event, error) {
	var event Event
	err := json.Unmarshal(data, &event)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// SetRequestID sets the request ID for the event
func (e *Event) SetRequestID(requestID string) *Event {
	e.RequestID = requestID
	return e
}

// SetUserID sets the user ID for the event
func (e *Event) SetUserID(userID string) *Event {
	e.UserID = userID
	return e
}

// SetProjectID sets the project ID for the event
func (e *Event) SetProjectID(projectID string) *Event {
	e.ProjectID = projectID
	return e
}

// SetSeverity sets the severity for the event
func (e *Event) SetSeverity(severity string) *Event {
	e.Severity = severity
	return e
}

// AddData adds data to the event
func (e *Event) AddData(key string, value interface{}) *Event {
	if e.Data == nil {
		e.Data = make(map[string]interface{})
	}
	e.Data[key] = value
	return e
}

// GetData retrieves data from the event
func (e *Event) GetData(key string) (interface{}, bool) {
	if e.Data == nil {
		return nil, false
	}
	value, exists := e.Data[key]
	return value, exists
}

// IsConnectionEvent checks if the event is a connection-related event
func (e *Event) IsConnectionEvent() bool {
	connectionEvents := []string{
		EventTypeConnection,
		EventTypeDisconnection,
		EventTypePing,
		EventTypePong,
		EventTypeHeartbeat,
	}
	
	for _, eventType := range connectionEvents {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

// IsProjectEvent checks if the event is a project-related event
func (e *Event) IsProjectEvent() bool {
	return e.ProjectID != ""
}

// IsUserEvent checks if the event is a user-related event
func (e *Event) IsUserEvent() bool {
	return e.UserID != ""
}

// IsSystemEvent checks if the event is a system-related event
func (e *Event) IsSystemEvent() bool {
	systemEvents := []string{
		EventTypeSystemAlert,
		EventTypeSystemMaintenance,
		EventTypeSystemUpdate,
	}
	
	for _, eventType := range systemEvents {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

// HasSeverity checks if the event has a severity level
func (e *Event) HasSeverity() bool {
	return e.Severity != ""
}

// IsCritical checks if the event is critical
func (e *Event) IsCritical() bool {
	return e.Severity == SeverityCritical
}

// IsError checks if the event is an error
func (e *Event) IsError() bool {
	return e.Severity == SeverityError
}

// IsWarning checks if the event is a warning
func (e *Event) IsWarning() bool {
	return e.Severity == SeverityWarning
}

// IsInfo checks if the event is informational
func (e *Event) IsInfo() bool {
	return e.Severity == SeverityInfo
}

// Clone creates a copy of the event
func (e *Event) Clone() *Event {
	clone := &Event{
		Type:      e.Type,
		Timestamp: e.Timestamp,
		Severity:  e.Severity,
		UserID:    e.UserID,
		ProjectID: e.ProjectID,
		RequestID: e.RequestID,
	}
	
	if e.Data != nil {
		clone.Data = make(map[string]interface{})
		for k, v := range e.Data {
			clone.Data[k] = v
		}
	}
	
	return clone
}

// String returns a string representation of the event
func (e *Event) String() string {
	jsonData, _ := e.ToJSON()
	return string(jsonData)
}

// EventFilter represents a filter for events
type EventFilter struct {
	Types     []string `json:"types,omitempty"`
	UserID    string   `json:"user_id,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	Severity  string   `json:"severity,omitempty"`
}

// Matches checks if an event matches the filter
func (f *EventFilter) Matches(event *Event) bool {
	// Check types
	if len(f.Types) > 0 {
		found := false
		for _, eventType := range f.Types {
			if event.Type == eventType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	// Check user ID
	if f.UserID != "" && event.UserID != f.UserID {
		return false
	}
	
	// Check project ID
	if f.ProjectID != "" && event.ProjectID != f.ProjectID {
		return false
	}
	
	// Check severity
	if f.Severity != "" && event.Severity != f.Severity {
		return false
	}
	
	return true
}

// EventHandler represents a function that handles events
type EventHandler func(*Event)

// EventSubscription represents a subscription to events
type EventSubscription struct {
	ID      string       `json:"id"`
	Filter  EventFilter  `json:"filter"`
	Handler EventHandler `json:"-"`
}

// EventBus represents an event bus for managing events
type EventBus struct {
	subscriptions map[string]*EventSubscription
	eventChan     chan *Event
}

// NewEventBus creates a new event bus
func NewEventBus() *EventBus {
	return &EventBus{
		subscriptions: make(map[string]*EventSubscription),
		eventChan:     make(chan *Event, 100),
	}
}

// Subscribe adds a subscription to the event bus
func (eb *EventBus) Subscribe(id string, filter EventFilter, handler EventHandler) {
	eb.subscriptions[id] = &EventSubscription{
		ID:      id,
		Filter:  filter,
		Handler: handler,
	}
}

// Unsubscribe removes a subscription from the event bus
func (eb *EventBus) Unsubscribe(id string) {
	delete(eb.subscriptions, id)
}

// Publish publishes an event to the event bus
func (eb *EventBus) Publish(event *Event) {
	select {
	case eb.eventChan <- event:
	default:
		// Channel is full, drop the event
	}
}

// Start starts the event bus
func (eb *EventBus) Start() {
	go func() {
		for event := range eb.eventChan {
			for _, subscription := range eb.subscriptions {
				if subscription.Filter.Matches(event) {
					go subscription.Handler(event)
				}
			}
		}
	}()
}

// Stop stops the event bus
func (eb *EventBus) Stop() {
	close(eb.eventChan)
}