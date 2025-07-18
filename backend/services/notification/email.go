package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"
	"time"

	"github.com/jordan-wright/email"
	"github.com/stackship/backend/config"
	"github.com/stackship/backend/database/models"
	"github.com/stackship/backend/utils"
)

// EmailNotificationService gère les notifications par email
type EmailNotificationService struct {
	config     *config.Config
	logger     utils.Logger
	templates  map[string]*template.Template
	smtpClient *smtp.Client
}

// EmailTemplate représente un template d'email
type EmailTemplate struct {
	Subject  string
	HTMLBody string
	TextBody string
}

// NotificationData contient les données pour personnaliser les notifications
type NotificationData struct {
	UserName        string
	ProjectName     string
	DeploymentID    string
	DeploymentURL   string
	Status          string
	ErrorMessage    string
	Duration        time.Duration
	Environment     string
	CommitHash      string
	CommitMessage   string
	Branch          string
	LogsURL         string
	MetricsURL      string
	Timestamp       time.Time
	ActionURL       string
	UnsubscribeURL  string
}

// EmailNotification représente une notification email à envoyer
type EmailNotification struct {
	ID          string
	To          []string
	CC          []string
	BCC         []string
	Subject     string
	HTMLBody    string
	TextBody    string
	Priority    string
	Attachments []EmailAttachment
	Metadata    map[string]string
	ScheduledAt *time.Time
	RetryCount  int
	MaxRetries  int
	CreatedAt   time.Time
}

// EmailAttachment représente une pièce jointe
type EmailAttachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// NewEmailNotificationService crée une nouvelle instance du service email
func NewEmailNotificationService(cfg *config.Config, logger utils.Logger) (*EmailNotificationService, error) {
	service := &EmailNotificationService{
		config:    cfg,
		logger:    logger,
		templates: make(map[string]*template.Template),
	}

	// Chargement des templates d'email
	if err := service.loadTemplates(); err != nil {
		return nil, fmt.Errorf("failed to load email templates: %w", err)
	}

	// Initialisation de la connexion SMTP
	if err := service.initSMTPConnection(); err != nil {
		return nil, fmt.Errorf("failed to initialize SMTP connection: %w", err)
	}

	return service, nil
}

// loadTemplates charge les templates d'email depuis les fichiers
func (s *EmailNotificationService) loadTemplates() error {
	templateFiles := map[string]string{
		"deployment_success":   "templates/deployment_success.html",
		"deployment_failed":    "templates/deployment_failed.html",
		"deployment_started":   "templates/deployment_started.html",
		"deployment_rollback":  "templates/deployment_rollback.html",
		"system_alert":         "templates/system_alert.html",
		"user_invitation":      "templates/user_invitation.html",
		"password_reset":       "templates/password_reset.html",
		"project_created":      "templates/project_created.html",
		"weekly_report":        "templates/weekly_report.html",
		"maintenance_notice":   "templates/maintenance_notice.html",
	}

	for name, file := range templateFiles {
		tmpl, err := template.ParseFiles(file)
		if err != nil {
			// Utiliser un template par défaut si le fichier n'existe pas
			tmpl, err = s.createDefaultTemplate(name)
			if err != nil {
				return fmt.Errorf("failed to create default template for %s: %w", name, err)
			}
		}
		s.templates[name] = tmpl
	}

	return nil
}

// createDefaultTemplate crée un template par défaut pour un type de notification
func (s *EmailNotificationService) createDefaultTemplate(templateName string) (*template.Template, error) {
	var templateContent string

	switch templateName {
	case "deployment_success":
		templateContent = `
<!DOCTYPE html>
<html>
<head><title>Deployment Success</title></head>
<body>
<h2>✅ Deployment Successful</h2>
<p>Hello {{.UserName}},</p>
<p>Your deployment for project <strong>{{.ProjectName}}</strong> has been completed successfully!</p>
<ul>
<li>Deployment ID: {{.DeploymentID}}</li>
<li>Environment: {{.Environment}}</li>
<li>Duration: {{.Duration}}</li>
<li>Commit: {{.CommitHash}} - {{.CommitMessage}}</li>
</ul>
<p><a href="{{.DeploymentURL}}">View Deployment</a> | <a href="{{.LogsURL}}">View Logs</a></p>
</body>
</html>`

	case "deployment_failed":
		templateContent = `
<!DOCTYPE html>
<html>
<head><title>Deployment Failed</title></head>
<body>
<h2>❌ Deployment Failed</h2>
<p>Hello {{.UserName}},</p>
<p>Your deployment for project <strong>{{.ProjectName}}</strong> has failed.</p>
<ul>
<li>Deployment ID: {{.DeploymentID}}</li>
<li>Environment: {{.Environment}}</li>
<li>Error: {{.ErrorMessage}}</li>
<li>Duration: {{.Duration}}</li>
</ul>
<p><a href="{{.DeploymentURL}}">View Deployment</a> | <a href="{{.LogsURL}}">View Logs</a></p>
</body>
</html>`

	case "system_alert":
		templateContent = `
<!DOCTYPE html>
<html>
<head><title>System Alert</title></head>
<body>
<h2>🚨 System Alert</h2>
<p>Hello {{.UserName}},</p>
<p>A system alert has been triggered for project <strong>{{.ProjectName}}</strong>.</p>
<p>Status: {{.Status}}</p>
<p>Message: {{.ErrorMessage}}</p>
<p><a href="{{.ActionURL}}">Take Action</a> | <a href="{{.MetricsURL}}">View Metrics</a></p>
</body>
</html>`

	default:
		templateContent = `
<!DOCTYPE html>
<html>
<head><title>StackShip Notification</title></head>
<body>
<h2>StackShip Notification</h2>
<p>Hello {{.UserName}},</p>
<p>You have a new notification from StackShip.</p>
<p>Project: {{.ProjectName}}</p>
<p>Status: {{.Status}}</p>
</body>
</html>`
	}

	return template.New(templateName).Parse(templateContent)
}

// initSMTPConnection initialise la connexion SMTP
func (s *EmailNotificationService) initSMTPConnection() error {
	// Configuration SMTP depuis les variables d'environnement
	smtpConfig := s.config.SMTP
	if smtpConfig.Host == "" {
		return fmt.Errorf("SMTP host not configured")
	}

	return nil
}

// SendDeploymentNotification envoie une notification de déploiement
func (s *EmailNotificationService) SendDeploymentNotification(ctx context.Context, 
	deployment *models.Deployment, 
	project *models.Project, 
	users []models.User, 
	notificationType string) error {

	data := &NotificationData{
		ProjectName:    project.Name,
		DeploymentID:   deployment.ID,
		DeploymentURL:  fmt.Sprintf("%s/projects/%s/deployments/%s", s.config.Frontend.BaseURL, project.ID, deployment.ID),
		Status:         deployment.Status,
		ErrorMessage:   deployment.ErrorMessage,
		Duration:       time.Since(deployment.CreatedAt),
		Environment:    deployment.Environment,
		CommitHash:     deployment.CommitHash,
		CommitMessage:  deployment.CommitMessage,
		Branch:         deployment.Branch,
		LogsURL:        fmt.Sprintf("%s/projects/%s/deployments/%s/logs", s.config.Frontend.BaseURL, project.ID, deployment.ID),
		MetricsURL:     fmt.Sprintf("%s/projects/%s/metrics", s.config.Frontend.BaseURL, project.ID),
		Timestamp:      time.Now(),
	}

	var templateName string
	switch notificationType {
	case "deployment_started":
		templateName = "deployment_started"
	case "deployment_success":
		templateName = "deployment_success"
	case "deployment_failed":
		templateName = "deployment_failed"
	case "deployment_rollback":
		templateName = "deployment_rollback"
	default:
		templateName = "deployment_success"
	}

	// Envoyer la notification à tous les utilisateurs concernés
	for _, user := range users {
		if user.NotificationPreferences.Email {
			data.UserName = user.Name
			data.UnsubscribeURL = fmt.Sprintf("%s/unsubscribe?token=%s", s.config.Frontend.BaseURL, user.UnsubscribeToken)
			
			if err := s.sendTemplateEmail(ctx, []string{user.Email}, templateName, data); err != nil {
				s.logger.Error("Failed to send deployment notification", "error", err, "user", user.Email)
				continue
			}
		}
	}

	return nil
}

// SendSystemAlert envoie une alerte système
func (s *EmailNotificationService) SendSystemAlert(ctx context.Context, 
	project *models.Project, 
	alert *models.Alert, 
	users []models.User) error {

	data := &NotificationData{
		ProjectName:   project.Name,
		Status:        alert.Status,
		ErrorMessage:  alert.Message,
		Environment:   alert.Environment,
		ActionURL:     fmt.Sprintf("%s/projects/%s/alerts/%s", s.config.Frontend.BaseURL, project.ID, alert.ID),
		MetricsURL:    fmt.Sprintf("%s/projects/%s/metrics", s.config.Frontend.BaseURL, project.ID),
		Timestamp:     time.Now(),
	}

	for _, user := range users {
		if user.NotificationPreferences.Email && user.NotificationPreferences.Alerts {
			data.UserName = user.Name
			data.UnsubscribeURL = fmt.Sprintf("%s/unsubscribe?token=%s", s.config.Frontend.BaseURL, user.UnsubscribeToken)
			
			if err := s.sendTemplateEmail(ctx, []string{user.Email}, "system_alert", data); err != nil {
				s.logger.Error("Failed to send system alert", "error", err, "user", user.Email)
				continue
			}
		}
	}

	return nil
}

// SendUserInvitation envoie une invitation utilisateur
func (s *EmailNotificationService) SendUserInvitation(ctx context.Context, 
	invitation *models.Invitation, 
	invitedBy *models.User, 
	project *models.Project) error {

	data := &NotificationData{
		UserName:      invitation.Email,
		ProjectName:   project.Name,
		ActionURL:     fmt.Sprintf("%s/invitations/accept?token=%s", s.config.Frontend.BaseURL, invitation.Token),
		Timestamp:     time.Now(),
	}

	subject := fmt.Sprintf("You've been invited to join %s on StackShip", project.Name)
	
	return s.sendTemplateEmail(ctx, []string{invitation.Email}, "user_invitation", data, subject)
}

// SendPasswordResetEmail envoie un email de réinitialisation de mot de passe
func (s *EmailNotificationService) SendPasswordResetEmail(ctx context.Context, 
	user *models.User, 
	resetToken string) error {

	data := &NotificationData{
		UserName:  user.Name,
		ActionURL: fmt.Sprintf("%s/auth/reset-password?token=%s", s.config.Frontend.BaseURL, resetToken),
		Timestamp: time.Now(),
	}

	return s.sendTemplateEmail(ctx, []string{user.Email}, "password_reset", data)
}

// SendWeeklyReport envoie un rapport hebdomadaire
func (s *EmailNotificationService) SendWeeklyReport(ctx context.Context, 
	user *models.User, 
	report *models.WeeklyReport) error {

	data := &NotificationData{
		UserName:       user.Name,
		UnsubscribeURL: fmt.Sprintf("%s/unsubscribe?token=%s", s.config.Frontend.BaseURL, user.UnsubscribeToken),
		Timestamp:      time.Now(),
	}

	return s.sendTemplateEmail(ctx, []string{user.Email}, "weekly_report", data)
}

// sendTemplateEmail envoie un email en utilisant un template
func (s *EmailNotificationService) sendTemplateEmail(ctx context.Context, 
	to []string, 
	templateName string, 
	data *NotificationData, 
	customSubject ...string) error {

	template, exists := s.templates[templateName]
	if !exists {
		return fmt.Errorf("template %s not found", templateName)
	}

	// Rendu du template
	var htmlBody bytes.Buffer
	if err := template.Execute(&htmlBody, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Déterminer le sujet
	subject := s.getDefaultSubject(templateName, data)
	if len(customSubject) > 0 {
		subject = customSubject[0]
	}

	// Créer la notification email
	notification := &EmailNotification{
		ID:         utils.GenerateID(),
		To:         to,
		Subject:    subject,
		HTMLBody:   htmlBody.String(),
		TextBody:   s.htmlToText(htmlBody.String()),
		Priority:   s.getNotificationPriority(templateName),
		Metadata:   map[string]string{"template": templateName},
		CreatedAt:  time.Now(),
		MaxRetries: 3,
	}

	return s.SendEmail(ctx, notification)
}

// SendEmail envoie un email
func (s *EmailNotificationService) SendEmail(ctx context.Context, notification *EmailNotification) error {
	// Validation des données
	if len(notification.To) == 0 {
		return fmt.Errorf("no recipients specified")
	}

	// Création de l'email
	e := email.NewEmail()
	e.From = s.config.SMTP.From
	e.To = notification.To
	e.Cc = notification.CC
	e.Bcc = notification.BCC
	e.Subject = notification.Subject
	e.HTML = []byte(notification.HTMLBody)
	e.Text = []byte(notification.TextBody)

	// Ajout des pièces jointes
	for _, attachment := range notification.Attachments {
		e.Attach(bytes.NewReader(attachment.Content), attachment.Filename, attachment.ContentType)
	}

	// Configuration SMTP
	smtpConfig := s.config.SMTP
	addr := fmt.Sprintf("%s:%d", smtpConfig.Host, smtpConfig.Port)

	// Authentification
	var auth smtp.Auth
	if smtpConfig.Username != "" && smtpConfig.Password != "" {
		auth = smtp.PlainAuth("", smtpConfig.Username, smtpConfig.Password, smtpConfig.Host)
	}

	// Configuration TLS
	tlsConfig := &tls.Config{
		InsecureSkipVerify: smtpConfig.InsecureSkipVerify,
		ServerName:         smtpConfig.Host,
	}

	// Envoi de l'email
	var err error
	if smtpConfig.UseTLS {
		err = e.SendWithTLS(addr, auth, tlsConfig)
	} else if smtpConfig.UseStartTLS {
		err = e.SendWithStartTLS(addr, auth, tlsConfig)
	} else {
		err = e.Send(addr, auth)
	}

	if err != nil {
		s.logger.Error("Failed to send email", "error", err, "to", notification.To)
		return fmt.Errorf("failed to send email: %w", err)
	}

	s.logger.Info("Email sent successfully", "to", notification.To, "subject", notification.Subject)
	return nil
}

// SendBulkEmail envoie des emails en masse
func (s *EmailNotificationService) SendBulkEmail(ctx context.Context, notifications []*EmailNotification) error {
	for _, notification := range notifications {
		if err := s.SendEmail(ctx, notification); err != nil {
			s.logger.Error("Failed to send bulk email", "error", err, "id", notification.ID)
			continue
		}
	}
	return nil
}

// getDefaultSubject retourne le sujet par défaut pour un template
func (s *EmailNotificationService) getDefaultSubject(templateName string, data *NotificationData) string {
	switch templateName {
	case "deployment_success":
		return fmt.Sprintf("✅ Deployment successful for %s", data.ProjectName)
	case "deployment_failed":
		return fmt.Sprintf("❌ Deployment failed for %s", data.ProjectName)
	case "deployment_started":
		return fmt.Sprintf("🚀 Deployment started for %s", data.ProjectName)
	case "deployment_rollback":
		return fmt.Sprintf("⚠️ Rollback initiated for %s", data.ProjectName)
	case "system_alert":
		return fmt.Sprintf("🚨 System alert for %s", data.ProjectName)
	case "user_invitation":
		return fmt.Sprintf("You're invited to join %s on StackShip", data.ProjectName)
	case "password_reset":
		return "Reset your StackShip password"
	case "weekly_report":
		return "Your weekly StackShip report"
	case "maintenance_notice":
		return "Scheduled maintenance notice"
	default:
		return "StackShip Notification"
	}
}

// getNotificationPriority détermine la priorité d'une notification
func (s *EmailNotificationService) getNotificationPriority(templateName string) string {
	switch templateName {
	case "deployment_failed", "system_alert":
		return "high"
	case "deployment_success", "deployment_started":
		return "medium"
	case "user_invitation", "password_reset":
		return "medium"
	case "weekly_report", "maintenance_notice":
		return "low"
	default:
		return "medium"
	}
}

// htmlToText convertit le HTML en texte brut
func (s *EmailNotificationService) htmlToText(html string) string {
	// Supprimer les balises HTML de base
	text := strings.ReplaceAll(html, "<br>", "\n")
	text = strings.ReplaceAll(text, "<br/>", "\n")
	text = strings.ReplaceAll(text, "<p>", "")
	text = strings.ReplaceAll(text, "</p>", "\n\n")
	text = strings.ReplaceAll(text, "<h1>", "")
	text = strings.ReplaceAll(text, "</h1>", "\n")
	text = strings.ReplaceAll(text, "<h2>", "")
	text = strings.ReplaceAll(text, "</h2>", "\n")
	text = strings.ReplaceAll(text, "<strong>", "")
	text = strings.ReplaceAll(text, "</strong>", "")
	text = strings.ReplaceAll(text, "<ul>", "")
	text = strings.ReplaceAll(text, "</ul>", "")
	text = strings.ReplaceAll(text, "<li>", "• ")
	text = strings.ReplaceAll(text, "</li>", "\n")
	
	// Supprimer les liens mais garder le texte
	// Ceci est une version simplifiée, une regex serait plus robuste
	text = strings.ReplaceAll(text, "<a href=\"", "")
	text = strings.ReplaceAll(text, "\">", ": ")
	text = strings.ReplaceAll(text, "</a>", "")
	
	return strings.TrimSpace(text)
}

// ValidateEmailAddress valide une adresse email
func (s *EmailNotificationService) ValidateEmailAddress(email string) bool {
	return strings.Contains(email, "@") && strings.Contains(email, ".")
}

// GetEmailStatus retourne le statut d'un email
func (s *EmailNotificationService) GetEmailStatus(ctx context.Context, emailID string) (*EmailStatus, error) {
	// Cette méthode serait connectée à une base de données pour traquer les statuts
	return &EmailStatus{
		ID:        emailID,
		Status:    "sent",
		SentAt:    time.Now(),
		DeliveredAt: time.Now().Add(5 * time.Second),
	}, nil
}

// EmailStatus représente le statut d'un email
type EmailStatus struct {
	ID          string
	Status      string // pending, sent, delivered, failed, bounced
	SentAt      time.Time
	DeliveredAt time.Time
	Error       string
}

// HealthCheck vérifie la santé du service email
func (s *EmailNotificationService) HealthCheck(ctx context.Context) error {
	// Tester la connexion SMTP
	smtpConfig := s.config.SMTP
	addr := fmt.Sprintf("%s:%d", smtpConfig.Host, smtpConfig.Port)
	
	conn, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer conn.Close()

	return nil
}

// Close ferme les connexions et nettoie les ressources
func (s *EmailNotificationService) Close() error {
	s.logger.Info("Closing email notification service")
	return nil
}
