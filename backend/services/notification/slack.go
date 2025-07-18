package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/stackship/backend/config"
	"github.com/stackship/backend/utils"
)

// SlackNotifier handles Slack notifications
type SlackNotifier struct {
	webhookURL string
	channel    string
	username   string
	logger     *utils.Logger
	client     *http.Client
}

// SlackMessage represents a Slack message structure
type SlackMessage struct {
	Channel     string            `json:"channel,omitempty"`
	Username    string            `json:"username,omitempty"`
	Text        string            `json:"text,omitempty"`
	IconEmoji   string            `json:"icon_emoji,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
	Blocks      []SlackBlock      `json:"blocks,omitempty"`
}

// SlackAttachment represents a Slack message attachment
type SlackAttachment struct {
	Color      string              `json:"color,omitempty"`
	Title      string              `json:"title,omitempty"`
	Text       string              `json:"text,omitempty"`
	Fields     []SlackField        `json:"fields,omitempty"`
	Footer     string              `json:"footer,omitempty"`
	Timestamp  int64               `json:"ts,omitempty"`
	Actions    []SlackAction       `json:"actions,omitempty"`
	AuthorName string              `json:"author_name,omitempty"`
	AuthorLink string              `json:"author_link,omitempty"`
	TitleLink  string              `json:"title_link,omitempty"`
}

// SlackField represents a field in a Slack attachment
type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// SlackAction represents an action button in Slack
type SlackAction struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	URL   string `json:"url,omitempty"`
	Style string `json:"style,omitempty"`
}

// SlackBlock represents a Slack block element
type SlackBlock struct {
	Type string      `json:"type"`
	Text *SlackText  `json:"text,omitempty"`
	Fields []SlackText `json:"fields,omitempty"`
}

// SlackText represents text in Slack blocks
type SlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NotificationEvent represents different types of events
type NotificationEvent struct {
	Type        string                 `json:"type"`
	ProjectID   string                 `json:"project_id"`
	ProjectName string                 `json:"project_name"`
	UserID      string                 `json:"user_id"`
	UserName    string                 `json:"user_name"`
	Message     string                 `json:"message"`
	Severity    string                 `json:"severity"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// Event types constants
const (
	EventDeploymentStarted   = "deployment_started"
	EventDeploymentSuccess   = "deployment_success"
	EventDeploymentFailed    = "deployment_failed"
	EventDeploymentRollback  = "deployment_rollback"
	EventProjectCreated      = "project_created"
	EventProjectDeleted      = "project_deleted"
	EventHealthCheckFailed   = "health_check_failed"
	EventResourceLimit       = "resource_limit"
	EventSecurityAlert       = "security_alert"
	EventSystemMaintenance   = "system_maintenance"
)

// Severity levels
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// NewSlackNotifier creates a new Slack notifier instance
func NewSlackNotifier(cfg *config.Config, logger *utils.Logger) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: cfg.Slack.WebhookURL,
		channel:    cfg.Slack.Channel,
		username:   cfg.Slack.Username,
		logger:     logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendNotification sends a notification to Slack
func (s *SlackNotifier) SendNotification(ctx context.Context, event NotificationEvent) error {
	if s.webhookURL == "" {
		s.logger.Warn("Slack webhook URL not configured, skipping notification")
		return nil
	}

	message, err := s.buildMessage(event)
	if err != nil {
		s.logger.Error("Failed to build Slack message", "error", err)
		return fmt.Errorf("failed to build message: %w", err)
	}

	return s.sendMessage(ctx, message)
}

// buildMessage creates a Slack message based on the event type
func (s *SlackNotifier) buildMessage(event NotificationEvent) (*SlackMessage, error) {
	message := &SlackMessage{
		Channel:   s.channel,
		Username:  s.username,
		IconEmoji: s.getIconForEvent(event.Type),
	}

	switch event.Type {
	case EventDeploymentStarted:
		message.Attachments = s.buildDeploymentStartedAttachment(event)
	case EventDeploymentSuccess:
		message.Attachments = s.buildDeploymentSuccessAttachment(event)
	case EventDeploymentFailed:
		message.Attachments = s.buildDeploymentFailedAttachment(event)
	case EventDeploymentRollback:
		message.Attachments = s.buildDeploymentRollbackAttachment(event)
	case EventProjectCreated:
		message.Attachments = s.buildProjectCreatedAttachment(event)
	case EventProjectDeleted:
		message.Attachments = s.buildProjectDeletedAttachment(event)
	case EventHealthCheckFailed:
		message.Attachments = s.buildHealthCheckFailedAttachment(event)
	case EventResourceLimit:
		message.Attachments = s.buildResourceLimitAttachment(event)
	case EventSecurityAlert:
		message.Attachments = s.buildSecurityAlertAttachment(event)
	case EventSystemMaintenance:
		message.Attachments = s.buildSystemMaintenanceAttachment(event)
	default:
		message.Attachments = s.buildGenericAttachment(event)
	}

	return message, nil
}

// buildDeploymentStartedAttachment creates attachment for deployment started event
func (s *SlackNotifier) buildDeploymentStartedAttachment(event NotificationEvent) []SlackAttachment {
	deploymentID := s.getMetadataString(event.Metadata, "deployment_id")
	environment := s.getMetadataString(event.Metadata, "environment")
	gitCommit := s.getMetadataString(event.Metadata, "git_commit")
	gitBranch := s.getMetadataString(event.Metadata, "git_branch")

	return []SlackAttachment{
		{
			Color: "#36a64f",
			Title: fmt.Sprintf("🚀 Deployment Started - %s", event.ProjectName),
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Environment", Value: environment, Short: true},
				{Title: "Deployment ID", Value: deploymentID, Short: true},
				{Title: "Triggered By", Value: event.UserName, Short: true},
				{Title: "Git Branch", Value: gitBranch, Short: true},
				{Title: "Git Commit", Value: s.truncateString(gitCommit, 8), Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Deployment",
					URL:   s.buildDeploymentURL(event.ProjectID, deploymentID),
					Style: "primary",
				},
			},
		},
	}
}

// buildDeploymentSuccessAttachment creates attachment for deployment success event
func (s *SlackNotifier) buildDeploymentSuccessAttachment(event NotificationEvent) []SlackAttachment {
	deploymentID := s.getMetadataString(event.Metadata, "deployment_id")
	environment := s.getMetadataString(event.Metadata, "environment")
	duration := s.getMetadataString(event.Metadata, "duration")
	url := s.getMetadataString(event.Metadata, "url")

	return []SlackAttachment{
		{
			Color: "#36a64f",
			Title: fmt.Sprintf("✅ Deployment Successful - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Environment", Value: environment, Short: true},
				{Title: "Deployment ID", Value: deploymentID, Short: true},
				{Title: "Duration", Value: duration, Short: true},
				{Title: "Application URL", Value: url, Short: false},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Application",
					URL:   url,
					Style: "primary",
				},
				{
					Type: "button",
					Text: "View Logs",
					URL:  s.buildLogsURL(event.ProjectID, deploymentID),
				},
			},
		},
	}
}

// buildDeploymentFailedAttachment creates attachment for deployment failed event
func (s *SlackNotifier) buildDeploymentFailedAttachment(event NotificationEvent) []SlackAttachment {
	deploymentID := s.getMetadataString(event.Metadata, "deployment_id")
	environment := s.getMetadataString(event.Metadata, "environment")
	errorMessage := s.getMetadataString(event.Metadata, "error")

	return []SlackAttachment{
		{
			Color: "#ff0000",
			Title: fmt.Sprintf("❌ Deployment Failed - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Environment", Value: environment, Short: true},
				{Title: "Deployment ID", Value: deploymentID, Short: true},
				{Title: "Triggered By", Value: event.UserName, Short: true},
				{Title: "Error", Value: s.truncateString(errorMessage, 200), Short: false},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Logs",
					URL:   s.buildLogsURL(event.ProjectID, deploymentID),
					Style: "danger",
				},
				{
					Type: "button",
					Text: "Rollback",
					URL:  s.buildRollbackURL(event.ProjectID, deploymentID),
				},
			},
		},
	}
}

// buildDeploymentRollbackAttachment creates attachment for deployment rollback event
func (s *SlackNotifier) buildDeploymentRollbackAttachment(event NotificationEvent) []SlackAttachment {
	deploymentID := s.getMetadataString(event.Metadata, "deployment_id")
	previousVersion := s.getMetadataString(event.Metadata, "previous_version")
	environment := s.getMetadataString(event.Metadata, "environment")

	return []SlackAttachment{
		{
			Color: "#ff9900",
			Title: fmt.Sprintf("🔄 Deployment Rollback - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Environment", Value: environment, Short: true},
				{Title: "Deployment ID", Value: deploymentID, Short: true},
				{Title: "Previous Version", Value: previousVersion, Short: true},
				{Title: "Triggered By", Value: event.UserName, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Deployment",
					URL:   s.buildDeploymentURL(event.ProjectID, deploymentID),
					Style: "primary",
				},
			},
		},
	}
}

// buildProjectCreatedAttachment creates attachment for project created event
func (s *SlackNotifier) buildProjectCreatedAttachment(event NotificationEvent) []SlackAttachment {
	repository := s.getMetadataString(event.Metadata, "repository")
	framework := s.getMetadataString(event.Metadata, "framework")

	return []SlackAttachment{
		{
			Color: "#36a64f",
			Title: fmt.Sprintf("📁 New Project Created - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Repository", Value: repository, Short: true},
				{Title: "Framework", Value: framework, Short: true},
				{Title: "Created By", Value: event.UserName, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Project",
					URL:   s.buildProjectURL(event.ProjectID),
					Style: "primary",
				},
			},
		},
	}
}

// buildProjectDeletedAttachment creates attachment for project deleted event
func (s *SlackNotifier) buildProjectDeletedAttachment(event NotificationEvent) []SlackAttachment {
	return []SlackAttachment{
		{
			Color: "#ff9900",
			Title: fmt.Sprintf("🗑️ Project Deleted - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Deleted By", Value: event.UserName, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
		},
	}
}

// buildHealthCheckFailedAttachment creates attachment for health check failed event
func (s *SlackNotifier) buildHealthCheckFailedAttachment(event NotificationEvent) []SlackAttachment {
	service := s.getMetadataString(event.Metadata, "service")
	endpoint := s.getMetadataString(event.Metadata, "endpoint")
	statusCode := s.getMetadataString(event.Metadata, "status_code")

	return []SlackAttachment{
		{
			Color: "#ff0000",
			Title: fmt.Sprintf("🚨 Health Check Failed - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Service", Value: service, Short: true},
				{Title: "Endpoint", Value: endpoint, Short: true},
				{Title: "Status Code", Value: statusCode, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Metrics",
					URL:   s.buildMetricsURL(event.ProjectID),
					Style: "danger",
				},
			},
		},
	}
}

// buildResourceLimitAttachment creates attachment for resource limit event
func (s *SlackNotifier) buildResourceLimitAttachment(event NotificationEvent) []SlackAttachment {
	resource := s.getMetadataString(event.Metadata, "resource")
	currentUsage := s.getMetadataString(event.Metadata, "current_usage")
	limit := s.getMetadataString(event.Metadata, "limit")

	return []SlackAttachment{
		{
			Color: "#ff9900",
			Title: fmt.Sprintf("⚠️ Resource Limit Warning - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Resource", Value: resource, Short: true},
				{Title: "Current Usage", Value: currentUsage, Short: true},
				{Title: "Limit", Value: limit, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "View Metrics",
					URL:   s.buildMetricsURL(event.ProjectID),
					Style: "primary",
				},
			},
		},
	}
}

// buildSecurityAlertAttachment creates attachment for security alert event
func (s *SlackNotifier) buildSecurityAlertAttachment(event NotificationEvent) []SlackAttachment {
	alertType := s.getMetadataString(event.Metadata, "alert_type")
	severity := s.getMetadataString(event.Metadata, "severity")
	source := s.getMetadataString(event.Metadata, "source")

	return []SlackAttachment{
		{
			Color: "#ff0000",
			Title: fmt.Sprintf("🔒 Security Alert - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Alert Type", Value: alertType, Short: true},
				{Title: "Severity", Value: strings.ToUpper(severity), Short: true},
				{Title: "Source", Value: source, Short: true},
			},
			Footer:    "StackShip Security",
			Timestamp: event.Timestamp.Unix(),
			Actions: []SlackAction{
				{
					Type:  "button",
					Text:  "Investigate",
					URL:   s.buildSecurityURL(event.ProjectID),
					Style: "danger",
				},
			},
		},
	}
}

// buildSystemMaintenanceAttachment creates attachment for system maintenance event
func (s *SlackNotifier) buildSystemMaintenanceAttachment(event NotificationEvent) []SlackAttachment {
	maintenanceType := s.getMetadataString(event.Metadata, "maintenance_type")
	startTime := s.getMetadataString(event.Metadata, "start_time")
	estimatedDuration := s.getMetadataString(event.Metadata, "estimated_duration")

	return []SlackAttachment{
		{
			Color: "#0099cc",
			Title: "🔧 System Maintenance Notice",
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Maintenance Type", Value: maintenanceType, Short: true},
				{Title: "Start Time", Value: startTime, Short: true},
				{Title: "Estimated Duration", Value: estimatedDuration, Short: true},
			},
			Footer:    "StackShip Operations",
			Timestamp: event.Timestamp.Unix(),
		},
	}
}

// buildGenericAttachment creates a generic attachment for unknown event types
func (s *SlackNotifier) buildGenericAttachment(event NotificationEvent) []SlackAttachment {
	return []SlackAttachment{
		{
			Color: "#36a64f",
			Title: fmt.Sprintf("StackShip Notification - %s", event.ProjectName),
			Text:  event.Message,
			Fields: []SlackField{
				{Title: "Event Type", Value: event.Type, Short: true},
				{Title: "Project", Value: event.ProjectName, Short: true},
				{Title: "Severity", Value: event.Severity, Short: true},
			},
			Footer:    "StackShip",
			Timestamp: event.Timestamp.Unix(),
		},
	}
}

// sendMessage sends the message to Slack
func (s *SlackNotifier) sendMessage(ctx context.Context, message *SlackMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned non-200 status: %d", resp.StatusCode)
	}

	s.logger.Info("Slack notification sent successfully", 
		"project", message.Attachments[0].Fields[0].Value,
		"webhook", s.webhookURL[:20]+"...",
	)

	return nil
}

// Helper methods for building URLs and extracting metadata

// getIconForEvent returns appropriate emoji for event type
func (s *SlackNotifier) getIconForEvent(eventType string) string {
	icons := map[string]string{
		EventDeploymentStarted:  ":rocket:",
		EventDeploymentSuccess:  ":white_check_mark:",
		EventDeploymentFailed:   ":x:",
		EventDeploymentRollback: ":leftwards_arrow_with_hook:",
		EventProjectCreated:     ":file_folder:",
		EventProjectDeleted:     ":wastebasket:",
		EventHealthCheckFailed:  ":rotating_light:",
		EventResourceLimit:      ":warning:",
		EventSecurityAlert:      ":lock:",
		EventSystemMaintenance:  ":wrench:",
	}

	if icon, exists := icons[eventType]; exists {
		return icon
	}
	return ":information_source:"
}

// getMetadataString safely extracts string from metadata
func (s *SlackNotifier) getMetadataString(metadata map[string]interface{}, key string) string {
	if value, exists := metadata[key]; exists {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return "N/A"
}

// truncateString truncates string to specified length
func (s *SlackNotifier) truncateString(str string, maxLength int) string {
	if len(str) <= maxLength {
		return str
	}
	return str[:maxLength] + "..."
}

// URL building methods - these would be configured based on your frontend routes
func (s *SlackNotifier) buildDeploymentURL(projectID, deploymentID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s/deployments/%s", projectID, deploymentID)
}

func (s *SlackNotifier) buildProjectURL(projectID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s", projectID)
}

func (s *SlackNotifier) buildLogsURL(projectID, deploymentID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s/deployments/%s/logs", projectID, deploymentID)
}

func (s *SlackNotifier) buildMetricsURL(projectID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s/metrics", projectID)
}

func (s *SlackNotifier) buildRollbackURL(projectID, deploymentID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s/deployments/%s/rollback", projectID, deploymentID)
}

func (s *SlackNotifier) buildSecurityURL(projectID string) string {
	return fmt.Sprintf("https://stackship.app/projects/%s/security", projectID)
}

// SendTestNotification sends a test notification to verify configuration
func (s *SlackNotifier) SendTestNotification(ctx context.Context) error {
	testEvent := NotificationEvent{
		Type:        "test",
		ProjectName: "Test Project",
		UserName:    "StackShip System",
		Message:     "This is a test notification to verify Slack integration is working correctly.",
		Severity:    SeverityInfo,
		Timestamp:   time.Now(),
		Metadata:    map[string]interface{}{},
	}

	return s.SendNotification(ctx, testEvent)
}

// SendCustomMessage sends a custom message to Slack
func (s *SlackNotifier) SendCustomMessage(ctx context.Context, text string, color string) error {
	message := &SlackMessage{
		Channel:   s.channel,
		Username:  s.username,
		IconEmoji: ":information_source:",
		Attachments: []SlackAttachment{
			{
				Color:     color,
				Text:      text,
				Footer:    "StackShip",
				Timestamp: time.Now().Unix(),
			},
		},
	}

	return s.sendMessage(ctx, message)
}