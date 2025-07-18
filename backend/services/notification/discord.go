package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stackship/backend/config"
	"github.com/stackship/backend/utils"
)

// DiscordService handles Discord webhook notifications
type DiscordService struct {
	client     *http.Client
	webhookURL string
	logger     utils.Logger
	config     *config.Config
}

// DiscordMessage represents a Discord webhook message structure
type DiscordMessage struct {
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Content   string         `json:"content,omitempty"`
	Embeds    []DiscordEmbed `json:"embeds,omitempty"`
}

// DiscordEmbed represents a Discord embed structure
type DiscordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []DiscordEmbedField `json:"fields,omitempty"`
	Footer      *DiscordEmbedFooter `json:"footer,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
	URL         string              `json:"url,omitempty"`
}

// DiscordEmbedField represents a field in Discord embed
type DiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// DiscordEmbedFooter represents footer in Discord embed
type DiscordEmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

// NotificationLevel represents the severity level of notifications
type NotificationLevel int

const (
	LevelInfo NotificationLevel = iota
	LevelWarning
	LevelError
	LevelSuccess
)

// Color constants for Discord embeds
const (
	ColorInfo    = 0x3498db // Blue
	ColorWarning = 0xf39c12 // Orange
	ColorError   = 0xe74c3c // Red
	ColorSuccess = 0x2ecc71 // Green
)

// NewDiscordService creates a new Discord notification service
func NewDiscordService(cfg *config.Config, logger utils.Logger) *DiscordService {
	return &DiscordService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		webhookURL: cfg.Discord.WebhookURL,
		logger:     logger,
		config:     cfg,
	}
}

// SendDeploymentNotification sends deployment-related notifications
func (d *DiscordService) SendDeploymentNotification(deployment *DeploymentInfo) error {
	if !d.config.Discord.Enabled {
		d.logger.Debug("Discord notifications disabled")
		return nil
	}

	embed := d.createDeploymentEmbed(deployment)
	message := &DiscordMessage{
		Username:  "StackShip Bot",
		AvatarURL: d.config.Discord.AvatarURL,
		Embeds:    []DiscordEmbed{embed},
	}

	return d.sendMessage(message)
}

// SendProjectNotification sends project-related notifications
func (d *DiscordService) SendProjectNotification(project *ProjectInfo) error {
	if !d.config.Discord.Enabled {
		d.logger.Debug("Discord notifications disabled")
		return nil
	}

	embed := d.createProjectEmbed(project)
	message := &DiscordMessage{
		Username:  "StackShip Bot",
		AvatarURL: d.config.Discord.AvatarURL,
		Embeds:    []DiscordEmbed{embed},
	}

	return d.sendMessage(message)
}

// SendAlertNotification sends monitoring alerts
func (d *DiscordService) SendAlertNotification(alert *AlertInfo) error {
	if !d.config.Discord.Enabled {
		d.logger.Debug("Discord notifications disabled")
		return nil
	}

	embed := d.createAlertEmbed(alert)
	message := &DiscordMessage{
		Username:  "StackShip Alert",
		AvatarURL: d.config.Discord.AlertAvatarURL,
		Embeds:    []DiscordEmbed{embed},
	}

	return d.sendMessage(message)
}

// SendCustomNotification sends custom notifications
func (d *DiscordService) SendCustomNotification(title, description string, level NotificationLevel, fields map[string]string) error {
	if !d.config.Discord.Enabled {
		d.logger.Debug("Discord notifications disabled")
		return nil
	}

	embed := d.createCustomEmbed(title, description, level, fields)
	message := &DiscordMessage{
		Username:  "StackShip Bot",
		AvatarURL: d.config.Discord.AvatarURL,
		Embeds:    []DiscordEmbed{embed},
	}

	return d.sendMessage(message)
}

// createDeploymentEmbed creates an embed for deployment notifications
func (d *DiscordService) createDeploymentEmbed(deployment *DeploymentInfo) DiscordEmbed {
	color := d.getColorForStatus(deployment.Status)
	
	embed := DiscordEmbed{
		Title:       fmt.Sprintf("Deployment %s", deployment.Status),
		Description: fmt.Sprintf("Project: **%s**\nEnvironment: **%s**", deployment.ProjectName, deployment.Environment),
		Color:       color,
		Timestamp:   deployment.Timestamp.Format(time.RFC3339),
		URL:         fmt.Sprintf("%s/deployments/%s", d.config.App.FrontendURL, deployment.ID),
		Fields: []DiscordEmbedField{
			{
				Name:   "Version",
				Value:  deployment.Version,
				Inline: true,
			},
			{
				Name:   "Duration",
				Value:  deployment.Duration.String(),
				Inline: true,
			},
			{
				Name:   "Deployed By",
				Value:  deployment.DeployedBy,
				Inline: true,
			},
		},
		Footer: &DiscordEmbedFooter{
			Text:    "StackShip Deployment",
			IconURL: d.config.Discord.FooterIconURL,
		},
	}

	// Add additional fields based on status
	if deployment.Status == "failed" && deployment.Error != "" {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   "Error",
			Value:  d.truncateText(deployment.Error, 1024),
			Inline: false,
		})
	}

	if deployment.CommitHash != "" {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   "Commit",
			Value:  fmt.Sprintf("[%s](%s)", deployment.CommitHash[:7], deployment.CommitURL),
			Inline: true,
		})
	}

	return embed
}

// createProjectEmbed creates an embed for project notifications
func (d *DiscordService) createProjectEmbed(project *ProjectInfo) DiscordEmbed {
	color := ColorInfo
	if project.Action == "created" {
		color = ColorSuccess
	} else if project.Action == "deleted" {
		color = ColorError
	}

	embed := DiscordEmbed{
		Title:       fmt.Sprintf("Project %s", project.Action),
		Description: fmt.Sprintf("**%s**\n%s", project.Name, project.Description),
		Color:       color,
		Timestamp:   project.Timestamp.Format(time.RFC3339),
		URL:         fmt.Sprintf("%s/projects/%s", d.config.App.FrontendURL, project.ID),
		Fields: []DiscordEmbedField{
			{
				Name:   "Repository",
				Value:  project.Repository,
				Inline: true,
			},
			{
				Name:   "Branch",
				Value:  project.Branch,
				Inline: true,
			},
			{
				Name:   "Owner",
				Value:  project.Owner,
				Inline: true,
			},
		},
		Footer: &DiscordEmbedFooter{
			Text:    "StackShip Project",
			IconURL: d.config.Discord.FooterIconURL,
		},
	}

	return embed
}

// createAlertEmbed creates an embed for monitoring alerts
func (d *DiscordService) createAlertEmbed(alert *AlertInfo) DiscordEmbed {
	color := ColorWarning
	if alert.Severity == "critical" {
		color = ColorError
	}

	embed := DiscordEmbed{
		Title:       fmt.Sprintf("🚨 Alert: %s", alert.AlertName),
		Description: alert.Description,
		Color:       color,
		Timestamp:   alert.Timestamp.Format(time.RFC3339),
		Fields: []DiscordEmbedField{
			{
				Name:   "Severity",
				Value:  alert.Severity,
				Inline: true,
			},
			{
				Name:   "Service",
				Value:  alert.Service,
				Inline: true,
			},
			{
				Name:   "Environment",
				Value:  alert.Environment,
				Inline: true,
			},
		},
		Footer: &DiscordEmbedFooter{
			Text:    "StackShip Monitoring",
			IconURL: d.config.Discord.FooterIconURL,
		},
	}

	// Add metric information if available
	if alert.MetricName != "" {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   "Metric",
			Value:  fmt.Sprintf("%s: %s", alert.MetricName, alert.MetricValue),
			Inline: false,
		})
	}

	// Add runbook link if available
	if alert.RunbookURL != "" {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   "Runbook",
			Value:  fmt.Sprintf("[View Runbook](%s)", alert.RunbookURL),
			Inline: false,
		})
	}

	return embed
}

// createCustomEmbed creates a custom embed
func (d *DiscordService) createCustomEmbed(title, description string, level NotificationLevel, fields map[string]string) DiscordEmbed {
	color := d.getColorForLevel(level)

	embed := DiscordEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Timestamp:   time.Now().Format(time.RFC3339),
		Footer: &DiscordEmbedFooter{
			Text:    "StackShip",
			IconURL: d.config.Discord.FooterIconURL,
		},
	}

	// Add custom fields
	for name, value := range fields {
		embed.Fields = append(embed.Fields, DiscordEmbedField{
			Name:   name,
			Value:  value,
			Inline: true,
		})
	}

	return embed
}

// sendMessage sends a message to Discord webhook
func (d *DiscordService) sendMessage(message *DiscordMessage) error {
	if d.webhookURL == "" {
		return fmt.Errorf("discord webhook URL not configured")
	}

	payload, err := json.Marshal(message)
	if err != nil {
		d.logger.Error("Failed to marshal Discord message", "error", err)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequest("POST", d.webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		d.logger.Error("Failed to create Discord request", "error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "StackShip-Bot/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		d.logger.Error("Failed to send Discord message", "error", err)
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.logger.Error("Discord API returned error", "status", resp.StatusCode)
		return fmt.Errorf("discord API returned status %d", resp.StatusCode)
	}

	d.logger.Debug("Successfully sent Discord notification")
	return nil
}

// getColorForStatus returns appropriate color for deployment status
func (d *DiscordService) getColorForStatus(status string) int {
	switch status {
	case "success", "completed":
		return ColorSuccess
	case "failed", "error":
		return ColorError
	case "pending", "running":
		return ColorWarning
	default:
		return ColorInfo
	}
}

// getColorForLevel returns appropriate color for notification level
func (d *DiscordService) getColorForLevel(level NotificationLevel) int {
	switch level {
	case LevelSuccess:
		return ColorSuccess
	case LevelWarning:
		return ColorWarning
	case LevelError:
		return ColorError
	default:
		return ColorInfo
	}
}

// truncateText truncates text to specified length
func (d *DiscordService) truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength-3] + "..."
}

// TestConnection tests the Discord webhook connection
func (d *DiscordService) TestConnection() error {
	if !d.config.Discord.Enabled {
		return fmt.Errorf("discord notifications are disabled")
	}

	testMessage := &DiscordMessage{
		Username:  "StackShip Bot",
		AvatarURL: d.config.Discord.AvatarURL,
		Content:   "🧪 Discord integration test successful!",
	}

	return d.sendMessage(testMessage)
}

// DeploymentInfo represents deployment information for notifications
type DeploymentInfo struct {
	ID          string        `json:"id"`
	ProjectName string        `json:"project_name"`
	Environment string        `json:"environment"`
	Version     string        `json:"version"`
	Status      string        `json:"status"`
	DeployedBy  string        `json:"deployed_by"`
	Duration    time.Duration `json:"duration"`
	Timestamp   time.Time     `json:"timestamp"`
	CommitHash  string        `json:"commit_hash"`
	CommitURL   string        `json:"commit_url"`
	Error       string        `json:"error,omitempty"`
}

// ProjectInfo represents project information for notifications
type ProjectInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Repository  string    `json:"repository"`
	Branch      string    `json:"branch"`
	Owner       string    `json:"owner"`
	Action      string    `json:"action"` // created, updated, deleted
	Timestamp   time.Time `json:"timestamp"`
}

// AlertInfo represents alert information for notifications
type AlertInfo struct {
	AlertName   string    `json:"alert_name"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"`
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	MetricName  string    `json:"metric_name"`
	MetricValue string    `json:"metric_value"`
	RunbookURL  string    `json:"runbook_url"`
	Timestamp   time.Time `json:"timestamp"`
}

// NotificationManager interface for managing different notification services
type NotificationManager interface {
	SendDeploymentNotification(deployment *DeploymentInfo) error
	SendProjectNotification(project *ProjectInfo) error
	SendAlertNotification(alert *AlertInfo) error
	SendCustomNotification(title, description string, level NotificationLevel, fields map[string]string) error
	TestConnection() error
}

// Ensure DiscordService implements NotificationManager
var _ NotificationManager = (*DiscordService)(nil)