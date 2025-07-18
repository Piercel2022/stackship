package git

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/stackship/backend/api/deployments"
	"github.com/stackship/backend/api/projects"
	"github.com/stackship/backend/api/websocket"
	"github.com/stackship/backend/config"
	"github.com/stackship/backend/database"
	"github.com/stackship/backend/utils"
)

// WebhookService gère les webhooks Git pour les déploiements automatiques
type WebhookService struct {
	config            *config.Config
	logger            *utils.Logger
	db                *database.Database
	cloneService      *CloneService
	projectsService   *projects.Service
	deploymentService *deployments.Service
	websocketHub      *websocket.Hub
	handlers          map[string]WebhookHandler
}

// WebhookHandler interface pour les différents providers Git
type WebhookHandler interface {
	ValidateSignature(payload []byte, signature string, secret string) bool
	ParsePayload(payload []byte) (*WebhookPayload, error)
	GetEventType(headers http.Header) string
	ShouldTriggerDeploy(payload *WebhookPayload) bool
}

// WebhookPayload structure commune pour tous les providers
type WebhookPayload struct {
	Repository  Repository   `json:"repository"`
	Ref         string       `json:"ref"`
	Before      string       `json:"before"`
	After       string       `json:"after"`
	Commits     []Commit     `json:"commits"`
	Pusher      User         `json:"pusher"`
	HeadCommit  *Commit      `json:"head_commit"`
	EventType   string       `json:"event_type"`
	Action      string       `json:"action"`
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	Deployment  *Deployment  `json:"deployment,omitempty"`
	Branch      string       `json:"branch"`
	Tag         string       `json:"tag,omitempty"`
	Forced      bool         `json:"forced"`
	Created     bool         `json:"created"`
	Deleted     bool         `json:"deleted"`
	Compare     string       `json:"compare"`
	Timestamp   time.Time    `json:"timestamp"`
}

// Repository information
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Description   string `json:"description"`
	Owner         User   `json:"owner"`
}

// Commit information
type Commit struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
	Author    User      `json:"author"`
	Committer User      `json:"committer"`
	Added     []string  `json:"added"`
	Removed   []string  `json:"removed"`
	Modified  []string  `json:"modified"`
}

// User information
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// PullRequest information
type PullRequest struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Head   Branch `json:"head"`
	Base   Branch `json:"base"`
	User   User   `json:"user"`
}

// Branch information
type Branch struct {
	Ref  string     `json:"ref"`
	SHA  string     `json:"sha"`
	Repo Repository `json:"repo"`
}

// Deployment information
type Deployment struct {
	ID          int64  `json:"id"`
	SHA         string `json:"sha"`
	Ref         string `json:"ref"`
	Environment string `json:"environment"`
	Description string `json:"description"`
	Creator     User   `json:"creator"`
}

// WebhookEvent repr�sente un �v�nement webhook trait�
type WebhookEvent struct {
	ID          string                 `json:"id"`
	ProjectID   string                 `json:"project_id"`
	Provider    string                 `json:"provider"`
	Event       string                 `json:"event"`
	Payload     *WebhookPayload        `json:"payload"`
	Headers     map[string]string      `json:"headers"`
	ProcessedAt time.Time              `json:"processed_at"`
	Status      string                 `json:"status"`
	Error       string                 `json:"error,omitempty"`
	Deployment  *DeploymentResult      `json:"deployment,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// DeploymentResult r�sultat d'un d�ploiement d�clench� par webhook
type DeploymentResult struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	LogURL      string    `json:"log_url,omitempty"`
}

// NewWebhookService cr�e une nouvelle instance du service webhook
func NewWebhookService(
	config *config.Config,
	logger *utils.Logger,
	db *database.Database,
	cloneService *CloneService,
	projectsService *projects.Service,
	deploymentService *deployments.Service,
	websocketHub *websocket.Hub,
) *WebhookService {
	ws := &WebhookService{
		config:            config,
		logger:            logger,
		db:                db,
		cloneService:      cloneService,
		projectsService:   projectsService,
		deploymentService: deploymentService,
		websocketHub:      websocketHub,
		handlers:          make(map[string]WebhookHandler),
	}

	// Enregistrer les handlers pour diff�rents providers
	ws.handlers["github"] = &GitHubHandler{}
	ws.handlers["gitlab"] = &GitLabHandler{}
	ws.handlers["bitbucket"] = &BitbucketHandler{}
	ws.handlers["gitea"] = &GiteaHandler{}

	return ws
}

// RegisterRoutes enregistre les routes webhook
func (ws *WebhookService) RegisterRoutes(router *mux.Router) {
	webhookRouter := router.PathPrefix("/webhooks").Subrouter()

	// Route g�n�rique pour tous les providers
	webhookRouter.HandleFunc("/{provider}/{project_id}", ws.HandleWebhook).Methods("POST")

	// Routes sp�cifiques pour chaque provider
	webhookRouter.HandleFunc("/github/{project_id}", ws.HandleGitHubWebhook).Methods("POST")
	webhookRouter.HandleFunc("/gitlab/{project_id}", ws.HandleGitLabWebhook).Methods("POST")
	webhookRouter.HandleFunc("/bitbucket/{project_id}", ws.HandleBitbucketWebhook).Methods("POST")
	webhookRouter.HandleFunc("/gitea/{project_id}", ws.HandleGiteaWebhook).Methods("POST")

	// Routes de gestion des webhooks
	webhookRouter.HandleFunc("/events", ws.GetWebhookEvents).Methods("GET")
	webhookRouter.HandleFunc("/events/{event_id}", ws.GetWebhookEvent).Methods("GET")
	webhookRouter.HandleFunc("/events/{event_id}/retry", ws.RetryWebhookEvent).Methods("POST")
}

// HandleWebhook g�re les webhooks de mani�re g�n�rique
func (ws *WebhookService) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	provider := vars["provider"]
	projectID := vars["project_id"]

	ws.logger.Info("Received webhook", "provider", provider, "projectID", projectID)

	// Lire le payload
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		ws.logger.Error("Failed to read webhook payload", "error", err)
		http.Error(w, "Failed to read payload", http.StatusBadRequest)
		return
	}

	// Traiter le webhook
	event, err := ws.processWebhook(provider, projectID, payload, r.Header)
	if err != nil {
		ws.logger.Error("Failed to process webhook", "error", err, "provider", provider)
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}

	// R�pondre avec succ�s
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"event_id": event.ID,
		"message":  "Webhook processed successfully",
	})
}

// HandleGitHubWebhook g�re sp�cifiquement les webhooks GitHub
func (ws *WebhookService) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	projectID := vars["project_id"]

	ws.logger.Info("Received GitHub webhook", "projectID", projectID)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		ws.logger.Error("Failed to read GitHub webhook payload", "error", err)
		http.Error(w, "Failed to read payload", http.StatusBadRequest)
		return
	}

	event, err := ws.processWebhook("github", projectID, payload, r.Header)
	if err != nil {
		ws.logger.Error("Failed to process GitHub webhook", "error", err)
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"event_id": event.ID,
	})
}

// HandleGitLabWebhook g�re sp�cifiquement les webhooks GitLab
func (ws *WebhookService) HandleGitLabWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	projectID := vars["project_id"]

	ws.logger.Info("Received GitLab webhook", "projectID", projectID)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		ws.logger.Error("Failed to read GitLab webhook payload", "error", err)
		http.Error(w, "Failed to read payload", http.StatusBadRequest)
		return
	}

	event, err := ws.processWebhook("gitlab", projectID, payload, r.Header)
	if err != nil {
		ws.logger.Error("Failed to process GitLab webhook", "error", err)
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"event_id": event.ID,
	})
}

// HandleBitbucketWebhook g�re sp�cifiquement les webhooks Bitbucket
func (ws *WebhookService) HandleBitbucketWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	projectID := vars["project_id"]

	ws.logger.Info("Received Bitbucket webhook", "projectID", projectID)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		ws.logger.Error("Failed to read Bitbucket webhook payload", "error", err)
		http.Error(w, "Failed to read payload", http.StatusBadRequest)
		return
	}

	event, err := ws.processWebhook("bitbucket", projectID, payload, r.Header)
	if err != nil {
		ws.logger.Error("Failed to process Bitbucket webhook", "error", err)
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"event_id": event.ID,
	})
}

// HandleGiteaWebhook g�re sp�cifiquement les webhooks Gitea
func (ws *WebhookService) HandleGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	projectID := vars["project_id"]

	ws.logger.Info("Received Gitea webhook", "projectID", projectID)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		ws.logger.Error("Failed to read Gitea webhook payload", "error", err)
		http.Error(w, "Failed to read payload", http.StatusBadRequest)
		return
	}

	event, err := ws.processWebhook("gitea", projectID, payload, r.Header)
	if err != nil {
		ws.logger.Error("Failed to process Gitea webhook", "error", err)
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"event_id": event.ID,
	})
}

// processWebhook traite un webhook de mani�re g�n�rique
func (ws *WebhookService) processWebhook(provider, projectID string, payload []byte, headers http.Header) (*WebhookEvent, error) {
	// V�rifier si le handler existe
	handler, exists := ws.handlers[provider]
	if !exists {
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}

	// R�cup�rer le projet
	project, err := ws.projectsService.GetProject(context.Background(), projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	// Cr�er l'�v�nement webhook
	event := &WebhookEvent{
		ID:          utils.GenerateID(),
		ProjectID:   projectID,
		Provider:    provider,
		Headers:     make(map[string]string),
		ProcessedAt: time.Now(),
		Status:      "processing",
		Metadata:    make(map[string]interface{}),
	}

	// Copier les headers
	for key, values := range headers {
		if len(values) > 0 {
			event.Headers[key] = values[0]
		}
	}

	// Obtenir le type d'�v�nement
	event.Event = handler.GetEventType(headers)

	// Valider la signature si configur�e
	if project.WebhookSecret != "" {
		signature := ""
		switch provider {
		case "github":
			signature = headers.Get("X-Hub-Signature-256")
		case "gitlab":
			signature = headers.Get("X-Gitlab-Token")
		case "bitbucket":
			signature = headers.Get("X-Hub-Signature")
		case "gitea":
			signature = headers.Get("X-Gitea-Signature")
		}

		if signature != "" && !handler.ValidateSignature(payload, signature, project.WebhookSecret) {
			event.Status = "failed"
			event.Error = "invalid signature"
			ws.saveWebhookEvent(event)
			return event, fmt.Errorf("invalid webhook signature")
		}
	}

	// Parser le payload
	webhookPayload, err := handler.ParsePayload(payload)
	if err != nil {
		event.Status = "failed"
		event.Error = fmt.Sprintf("failed to parse payload: %v", err)
		ws.saveWebhookEvent(event)
		return event, fmt.Errorf("failed to parse payload: %w", err)
	}

	event.Payload = webhookPayload

	// V�rifier si le d�ploiement doit �tre d�clench�
	if !handler.ShouldTriggerDeploy(webhookPayload) {
		event.Status = "skipped"
		event.Metadata["reason"] = "deployment not triggered for this event"
		ws.saveWebhookEvent(event)
		return event, nil
	}

	// D�clencher le d�ploiement
	deploymentResult, err := ws.triggerDeployment(project, webhookPayload)
	if err != nil {
		event.Status = "failed"
		event.Error = fmt.Sprintf("failed to trigger deployment: %v", err)
		ws.saveWebhookEvent(event)
		return event, fmt.Errorf("failed to trigger deployment: %w", err)
	}

	event.Deployment = deploymentResult
	event.Status = "success"
	ws.saveWebhookEvent(event)

	// Notifier via WebSocket
	ws.notifyWebhookEvent(event)

	return event, nil
}

// triggerDeployment d�clenche un d�ploiement bas� sur le webhook
func (ws *WebhookService) triggerDeployment(project *projects.Project, payload *WebhookPayload) (*DeploymentResult, error) {
	// Extraire la branche du ref
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	// V�rifier si la branche correspond � celle configur�e pour le d�ploiement
	if project.DeployBranch != "" && project.DeployBranch != branch {
		return nil, fmt.Errorf("branch %s does not match deploy branch %s", branch, project.DeployBranch)
	}

	// Pr�parer les param�tres de d�ploiement
	deploymentParams := &deployments.DeploymentParams{
		ProjectID:   project.ID,
		Branch:      branch,
		CommitSHA:   payload.After,
		Trigger:     "webhook",
		Environment: project.Environment,
		Metadata: map[string]interface{}{
			"webhook_provider": payload.Repository.FullName,
			"commit_message":   payload.HeadCommit.Message,
			"commit_author":    payload.HeadCommit.Author.Name,
			"pusher":           payload.Pusher.Login,
		},
	}

	// Lancer le d�ploiement
	deployment, err := ws.deploymentService.CreateDeployment(context.Background(), deploymentParams)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	return &DeploymentResult{
		ID:        deployment.ID,
		Status:    deployment.Status,
		StartedAt: deployment.StartedAt,
		LogURL:    fmt.Sprintf("/deployments/%s/logs", deployment.ID),
	}, nil
}

// GetWebhookEvents retourne la liste des �v�nements webhook
func (ws *WebhookService) GetWebhookEvents(w http.ResponseWriter, r *http.Request) {
	// Param�tres de pagination
	page := 1
	limit := 50

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	// Filtres
	projectID := r.URL.Query().Get("project_id")
	provider := r.URL.Query().Get("provider")
	status := r.URL.Query().Get("status")

	// R�cup�rer les �v�nements depuis la base de donn�es
	events, total, err := ws.getWebhookEvents(projectID, provider, status, page, limit)
	if err != nil {
		ws.logger.Error("Failed to get webhook events", "error", err)
		http.Error(w, "Failed to get webhook events", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"events": events,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetWebhookEvent retourne un �v�nement webhook sp�cifique
func (ws *WebhookService) GetWebhookEvent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventID := vars["event_id"]

	event, err := ws.getWebhookEvent(eventID)
	if err != nil {
		ws.logger.Error("Failed to get webhook event", "error", err, "eventID", eventID)
		http.Error(w, "Failed to get webhook event", http.StatusInternalServerError)
		return
	}

	if event == nil {
		http.Error(w, "Webhook event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
}

// RetryWebhookEvent retraite un �v�nement webhook
func (ws *WebhookService) RetryWebhookEvent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventID := vars["event_id"]

	event, err := ws.getWebhookEvent(eventID)
	if err != nil {
		ws.logger.Error("Failed to get webhook event for retry", "error", err, "eventID", eventID)
		http.Error(w, "Failed to get webhook event", http.StatusInternalServerError)
		return
	}

	if event == nil {
		http.Error(w, "Webhook event not found", http.StatusNotFound)
		return
	}

	// Recr�er les headers
	headers := make(http.Header)
	for key, value := range event.Headers {
		headers.Set(key, value)
	}

	// Retraiter l'�v�nement
	payload, _ := json.Marshal(event.Payload)
	newEvent, err := ws.processWebhook(event.Provider, event.ProjectID, payload, headers)
	if err != nil {
		ws.logger.Error("Failed to retry webhook event", "error", err, "eventID", eventID)
		http.Error(w, fmt.Sprintf("Failed to retry webhook: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(newEvent)
}

// saveWebhookEvent sauvegarde un événement webhook en base de données
func (ws *WebhookService) saveWebhookEvent(event *WebhookEvent) error {
	// Implémenter la sauvegarde en base de données
	// Cette fonction d�pend de votre couche de base de données
	ws.logger.Info("Saving webhook event", "eventID", event.ID, "status", event.Status)
	return nil
}

// getWebhookEvents récupere les événements webhook depuis la base de données
func (ws *WebhookService) getWebhookEvents(projectID, provider, status string, page, limit int) ([]*WebhookEvent, int, error) {
	// Implémenter la récupération depuis la base de données
	// Cette fonction depend de votre couche de base de données
	events := []*WebhookEvent{}
	total := 0
	return events, total, nil
}

// getWebhookEvent récupére un événement webhook spécifique
func (ws *WebhookService) getWebhookEvent(eventID string) (*WebhookEvent, error) {
	// Implémenter la r�cup�ration depuis la base de données
	// Cette fonction d�pend de votre couche de base de donn�es
	return nil, nil
}

// notifyWebhookEvent notifie les clients WebSocket d'un nouvel événement
func (ws *WebhookService) notifyWebhookEvent(event *WebhookEvent) {
	if ws.websocketHub != nil {
		ws.websocketHub.Broadcast <- &websocket.Message{
			Type: "webhook_event",
			Data: event,
		}
	}
}

// Impl�mentations des handlers pour chaque provider

// GitHubHandler handler pour GitHub
type GitHubHandler struct{}

func (h *GitHubHandler) ValidateSignature(payload []byte, signature string, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expectedMAC))
}

func (h *GitHubHandler) ParsePayload(payload []byte) (*WebhookPayload, error) {
	var githubPayload map[string]interface{}
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		return nil, err
	}

	// Convertir le payload GitHub vers notre structure commune
	webhookPayload := &WebhookPayload{
		Timestamp: time.Now(),
	}

	// Mapper les champs GitHub vers notre structure
	if repo, ok := githubPayload["repository"].(map[string]interface{}); ok {
		webhookPayload.Repository = Repository{
			ID:            int64(repo["id"].(float64)),
			Name:          repo["name"].(string),
			FullName:      repo["full_name"].(string),
			CloneURL:      repo["clone_url"].(string),
			SSHURL:        repo["ssh_url"].(string),
			HTMLURL:       repo["html_url"].(string),
			Private:       repo["private"].(bool),
			DefaultBranch: repo["default_branch"].(string),
		}
	}

	if ref, ok := githubPayload["ref"].(string); ok {
		webhookPayload.Ref = ref
		webhookPayload.Branch = strings.TrimPrefix(ref, "refs/heads/")
	}

	if before, ok := githubPayload["before"].(string); ok {
		webhookPayload.Before = before
	}

	if after, ok := githubPayload["after"].(string); ok {
		webhookPayload.After = after
	}

	if headCommit, ok := githubPayload["head_commit"].(map[string]interface{}); ok && headCommit != nil {
		webhookPayload.HeadCommit = &Commit{
			ID:      headCommit["id"].(string),
			Message: headCommit["message"].(string),
			URL:     headCommit["url"].(string),
		}
	}

	return webhookPayload, nil
}

func (h *GitHubHandler) GetEventType(headers http.Header) string {
	return headers.Get("X-GitHub-Event")
}

func (h *GitHubHandler) ShouldTriggerDeploy(payload *WebhookPayload) bool {
	return payload.EventType == "push" && !payload.Deleted
}

// GitLabHandler handler pour GitLab
type GitLabHandler struct{}

func (h *GitLabHandler) ValidateSignature(payload []byte, signature string, secret string) bool {
	return signature == secret
}

func (h *GitLabHandler) ParsePayload(payload []byte) (*WebhookPayload, error) {
	var gitlabPayload map[string]interface{}
	if err := json.Unmarshal(payload, &gitlabPayload); err != nil {
		return nil, err
	}

	webhookPayload := &WebhookPayload{
		Timestamp: time.Now(),
	}

	// Mapper les champs GitLab vers notre structure
	if repo, ok := gitlabPayload["project"].(map[string]interface{}); ok {
		webhookPayload.Repository = Repository{
			ID:            int64(repo["id"].(float64)),
			Name:          repo["name"].(string),
			FullName:      repo["path_with_namespace"].(string),
			CloneURL:      repo["git_http_url"].(string),
			SSHURL:        repo["git_ssh_url"].(string),
			HTMLURL:       repo["web_url"].(string),
			DefaultBranch: repo["default_branch"].(string),
		}
	}

	if ref, ok := gitlabPayload["ref"].(string); ok {
		webhookPayload.Ref = ref
		webhookPayload.Branch = strings.TrimPrefix(ref, "refs/heads/")
	}

	if before, ok := gitlabPayload["before"].(string); ok {
		webhookPayload.Before = before
	}

	if after, ok := gitlabPayload["after"].(string); ok {
		webhookPayload.After = after
	}

	return webhookPayload, nil
}

func (h *GitLabHandler) GetEventType(headers http.Header) string {
	return headers.Get("X-Gitlab-Event")
}

func (h *GitLabHandler) ShouldTriggerDeploy(payload *WebhookPayload) bool {
	return payload.EventType == "Push Hook"
}

// BitbucketHandler handler pour Bitbucket
// BitbucketHandler handler pour Bitbucket
type BitbucketHandler struct{}

func (h *BitbucketHandler) ValidateSignature(payload []byte, signature string, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expectedMAC))
}

func (h *BitbucketHandler) ParsePayload(payload []byte) (*WebhookPayload, error) {
	var bitbucketPayload map[string]interface{}
	if err := json.Unmarshal(payload, &bitbucketPayload); err != nil {
		return nil, err
	}

	webhookPayload := &WebhookPayload{
		Timestamp: time.Now(),
	}

	// Mapper les champs Bitbucket vers notre structure
	if repo, ok := bitbucketPayload["repository"].(map[string]interface{}); ok {
		webhookPayload.Repository = Repository{
			Name:     repo["name"].(string),
			FullName: repo["full_name"].(string),
			Private:  repo["is_private"].(bool),
		}

		if links, ok := repo["links"].(map[string]interface{}); ok {
			if html, ok := links["html"].(map[string]interface{}); ok {
				webhookPayload.Repository.HTMLURL = html["href"].(string)
			}
			if clone, ok := links["clone"].([]interface{}); ok {
				for _, c := range clone {
					if cloneLink, ok := c.(map[string]interface{}); ok {
						if cloneLink["name"].(string) == "https" {
							webhookPayload.Repository.CloneURL = cloneLink["href"].(string)
						}
						if cloneLink["name"].(string) == "ssh" {
							webhookPayload.Repository.SSHURL = cloneLink["href"].(string)
						}
					}
				}
			}
		}
	}

	// Traiter les changements push
	if push, ok := bitbucketPayload["push"].(map[string]interface{}); ok {
		if changes, ok := push["changes"].([]interface{}); ok && len(changes) > 0 {
			if change, ok := changes[0].(map[string]interface{}); ok {
				if newCommit, ok := change["new"].(map[string]interface{}); ok {
					webhookPayload.Ref = "refs/heads/" + newCommit["name"].(string)
					webhookPayload.Branch = newCommit["name"].(string)

					if target, ok := newCommit["target"].(map[string]interface{}); ok {
						webhookPayload.After = target["hash"].(string)

						webhookPayload.HeadCommit = &Commit{
							ID:      target["hash"].(string),
							Message: target["message"].(string),
						}

						if author, ok := target["author"].(map[string]interface{}); ok {
							webhookPayload.HeadCommit.Author = User{
								Name:  author["raw"].(string),
								Email: author["raw"].(string),
							}
						}
					}
				}

				if oldCommit, ok := change["old"].(map[string]interface{}); ok {
					if target, ok := oldCommit["target"].(map[string]interface{}); ok {
						webhookPayload.Before = target["hash"].(string)
					}
				}
			}
		}
	}

	return webhookPayload, nil
}

func (h *BitbucketHandler) GetEventType(headers http.Header) string {
	return headers.Get("X-Event-Key")
}

func (h *BitbucketHandler) ShouldTriggerDeploy(payload *WebhookPayload) bool {
	return payload.EventType == "repo:push" && !payload.Deleted
}

// GiteaHandler handler pour Gitea
type GiteaHandler struct{}

func (h *GiteaHandler) ValidateSignature(payload []byte, signature string, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expectedMAC))
}

func (h *GiteaHandler) ParsePayload(payload []byte) (*WebhookPayload, error) {
	var giteaPayload map[string]interface{}
	if err := json.Unmarshal(payload, &giteaPayload); err != nil {
		return nil, err
	}

	webhookPayload := &WebhookPayload{
		Timestamp: time.Now(),
	}

	// Mapper les champs Gitea vers notre structure (similaire � GitHub)
	if repo, ok := giteaPayload["repository"].(map[string]interface{}); ok {
		webhookPayload.Repository = Repository{
			ID:            int64(repo["id"].(float64)),
			Name:          repo["name"].(string),
			FullName:      repo["full_name"].(string),
			CloneURL:      repo["clone_url"].(string),
			SSHURL:        repo["ssh_url"].(string),
			HTMLURL:       repo["html_url"].(string),
			Private:       repo["private"].(bool),
			DefaultBranch: repo["default_branch"].(string),
		}

		if owner, ok := repo["owner"].(map[string]interface{}); ok {
			webhookPayload.Repository.Owner = User{
				ID:        int64(owner["id"].(float64)),
				Login:     owner["login"].(string),
				Name:      owner["full_name"].(string),
				Email:     owner["email"].(string),
				AvatarURL: owner["avatar_url"].(string),
			}
		}
	}

	if ref, ok := giteaPayload["ref"].(string); ok {
		webhookPayload.Ref = ref
		webhookPayload.Branch = strings.TrimPrefix(ref, "refs/heads/")
	}

	if before, ok := giteaPayload["before"].(string); ok {
		webhookPayload.Before = before
	}

	if after, ok := giteaPayload["after"].(string); ok {
		webhookPayload.After = after
	}

	if headCommit, ok := giteaPayload["head_commit"].(map[string]interface{}); ok && headCommit != nil {
		webhookPayload.HeadCommit = &Commit{
			ID:      headCommit["id"].(string),
			Message: headCommit["message"].(string),
			URL:     headCommit["url"].(string),
		}

		if author, ok := headCommit["author"].(map[string]interface{}); ok {
			webhookPayload.HeadCommit.Author = User{
				Name:  author["name"].(string),
				Email: author["email"].(string),
			}
		}

		if committer, ok := headCommit["committer"].(map[string]interface{}); ok {
			webhookPayload.HeadCommit.Committer = User{
				Name:  committer["name"].(string),
				Email: committer["email"].(string),
			}
		}

		if added, ok := headCommit["added"].([]interface{}); ok {
			webhookPayload.HeadCommit.Added = make([]string, len(added))
			for i, file := range added {
				webhookPayload.HeadCommit.Added[i] = file.(string)
			}
		}

		if removed, ok := headCommit["removed"].([]interface{}); ok {
			webhookPayload.HeadCommit.Removed = make([]string, len(removed))
			for i, file := range removed {
				webhookPayload.HeadCommit.Removed[i] = file.(string)
			}
		}

		if modified, ok := headCommit["modified"].([]interface{}); ok {
			webhookPayload.HeadCommit.Modified = make([]string, len(modified))
			for i, file := range modified {
				webhookPayload.HeadCommit.Modified[i] = file.(string)
			}
		}
	}

	if pusher, ok := giteaPayload["pusher"].(map[string]interface{}); ok {
		webhookPayload.Pusher = User{
			ID:        int64(pusher["id"].(float64)),
			Login:     pusher["login"].(string),
			Name:      pusher["full_name"].(string),
			Email:     pusher["email"].(string),
			AvatarURL: pusher["avatar_url"].(string),
		}
	}

	if commits, ok := giteaPayload["commits"].([]interface{}); ok {
		webhookPayload.Commits = make([]Commit, len(commits))
		for i, c := range commits {
			if commit, ok := c.(map[string]interface{}); ok {
				webhookPayload.Commits[i] = Commit{
					ID:      commit["id"].(string),
					Message: commit["message"].(string),
					URL:     commit["url"].(string),
				}

				if author, ok := commit["author"].(map[string]interface{}); ok {
					webhookPayload.Commits[i].Author = User{
						Name:  author["name"].(string),
						Email: author["email"].(string),
					}
				}

				if committer, ok := commit["committer"].(map[string]interface{}); ok {
					webhookPayload.Commits[i].Committer = User{
						Name:  committer["name"].(string),
						Email: committer["email"].(string),
					}
				}
			}
		}
	}

	// Gérer les flags spéciaux
	if created, ok := giteaPayload["created"].(bool); ok {
		webhookPayload.Created = created
	}

	if deleted, ok := giteaPayload["deleted"].(bool); ok {
		webhookPayload.Deleted = deleted
	}

	if forced, ok := giteaPayload["forced"].(bool); ok {
		webhookPayload.Forced = forced
	}

	if compare, ok := giteaPayload["compare"].(string); ok {
		webhookPayload.Compare = compare
	}

	return webhookPayload, nil
}

func (h *GiteaHandler) GetEventType(headers http.Header) string {
	return headers.Get("X-Gitea-Event")
}

func (h *GiteaHandler) ShouldTriggerDeploy(payload *WebhookPayload) bool {
	return payload.EventType == "push" && !payload.Deleted
}

// Fonctions utilitaires pour la gestion des webhooks

// GenerateWebhookSecret g�n�re un secret sécurisé pour les webhooks
func GenerateWebhookSecret() string {
	return utils.GenerateRandomString(32)
}

// ValidateWebhookURL valide qu'une URL de webhook est correcte
func ValidateWebhookURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("webhook URL must start with http:// or https://")
	}
	return nil
}

// GetWebhookURLForProject g�n�re l'URL de webhook pour un projet
func (ws *WebhookService) GetWebhookURLForProject(projectID, provider string) string {
	baseURL := ws.config.Server.BaseURL
	if baseURL == "" {
		baseURL = "https://api.stackship.com"
	}

	return fmt.Sprintf("%s/webhooks/%s/%s", baseURL, provider, projectID)
}

// IsWebhookEventRelevant vérifie si un événement webhook est pertinent pour le d�ploiement
func (ws *WebhookService) IsWebhookEventRelevant(eventType string, provider string) bool {
	relevantEvents := map[string][]string{
		"github":    {"push", "pull_request"},
		"gitlab":    {"Push Hook", "Merge Request Hook"},
		"bitbucket": {"repo:push", "pullrequest:created"},
		"gitea":     {"push", "pull_request"},
	}

	events, exists := relevantEvents[provider]
	if !exists {
		return false
	}

	for _, event := range events {
		if event == eventType {
			return true
		}
	}

	return false
}

// ParseWebhookHeaders extrait les informations importantes des headers
func ParseWebhookHeaders(headers http.Header) map[string]string {
	important := map[string]string{}

	// Headers communs � surveiller
	importantHeaders := []string{
		"X-GitHub-Event",
		"X-GitHub-Delivery",
		"X-Hub-Signature",
		"X-Hub-Signature-256",
		"X-Gitlab-Event",
		"X-Gitlab-Token",
		"X-Event-Key",
		"X-Request-UUID",
		"X-Gitea-Event",
		"X-Gitea-Signature",
		"User-Agent",
		"Content-Type",
	}

	for _, header := range importantHeaders {
		if value := headers.Get(header); value != "" {
			important[header] = value
		}
	}

	return important
}

// LogWebhookEvent enregistre un événement webhook dans les logs
func (ws *WebhookService) LogWebhookEvent(event *WebhookEvent) {
	ws.logger.Info("Webhook event processed",
		"event_id", event.ID,
		"project_id", event.ProjectID,
		"provider", event.Provider,
		"event_type", event.Event,
		"status", event.Status,
		"branch", event.Payload.Branch,
		"commit", event.Payload.After,
		"repository", event.Payload.Repository.FullName,
	)
}
