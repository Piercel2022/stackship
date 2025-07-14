package projects

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stackship/backend/api/auth"
	"github.com/stackship/backend/api/websocket"
	"github.com/stackship/backend/services/git"
	"github.com/stackship/backend/services/notification"
	"github.com/stackship/backend/utils"
)

// Handler structure contenant toutes les dépendances nécessaires
type Handler struct {
	service         *Service
	logger          *logrus.Logger
	websocketHub    *websocket.Hub
	gitClient       *git.Client
	notificationSvc *notification.Service
	validator       *utils.Validator
}

// NewHandler crée une nouvelle instance du handler projects
func NewHandler(
	service *Service,
	logger *logrus.Logger,
	wsHub *websocket.Hub,
	gitClient *git.Client,
	notificationSvc *notification.Service,
	validator *utils.Validator,
) *Handler {
	return &Handler{
		service:         service,
		logger:          logger,
		websocketHub:    wsHub,
		gitClient:       gitClient,
		notificationSvc: notificationSvc,
		validator:       validator,
	}
}

// RegisterRoutes enregistre toutes les routes pour les projets
func (h *Handler) RegisterRoutes(router *mux.Router) {
	projectsRouter := router.PathPrefix("/projects").Subrouter()

	// Middleware d'authentification pour toutes les routes
	projectsRouter.Use(auth.AuthMiddleware)

	// Routes CRUD basiques
	projectsRouter.HandleFunc("", h.GetProjects).Methods("GET")
	projectsRouter.HandleFunc("", h.CreateProject).Methods("POST")
	projectsRouter.HandleFunc("/{id}", h.GetProject).Methods("GET")
	projectsRouter.HandleFunc("/{id}", h.UpdateProject).Methods("PUT")
	projectsRouter.HandleFunc("/{id}", h.DeleteProject).Methods("DELETE")

	// Routes spécialisées
	projectsRouter.HandleFunc("/{id}/status", h.GetProjectStatus).Methods("GET")
	projectsRouter.HandleFunc("/{id}/deployments", h.GetProjectDeployments).Methods("GET")
	projectsRouter.HandleFunc("/{id}/environments", h.GetProjectEnvironments).Methods("GET")
	projectsRouter.HandleFunc("/{id}/members", h.GetProjectMembers).Methods("GET")
	projectsRouter.HandleFunc("/{id}/members", h.AddProjectMember).Methods("POST")
	projectsRouter.HandleFunc("/{id}/members/{userId}", h.RemoveProjectMember).Methods("DELETE")

	// Routes Git
	projectsRouter.HandleFunc("/{id}/git/sync", h.SyncGitRepository).Methods("POST")
	projectsRouter.HandleFunc("/{id}/git/branches", h.GetGitBranches).Methods("GET")
	projectsRouter.HandleFunc("/{id}/git/commits", h.GetGitCommits).Methods("GET")

	// Routes de configuration
	projectsRouter.HandleFunc("/{id}/config", h.GetProjectConfig).Methods("GET")
	projectsRouter.HandleFunc("/{id}/config", h.UpdateProjectConfig).Methods("PUT")

	// Routes de monitoring
	projectsRouter.HandleFunc("/{id}/metrics", h.GetProjectMetrics).Methods("GET")
	projectsRouter.HandleFunc("/{id}/logs", h.GetProjectLogs).Methods("GET")

	// Routes d'actions
	projectsRouter.HandleFunc("/{id}/archive", h.ArchiveProject).Methods("POST")
	projectsRouter.HandleFunc("/{id}/restore", h.RestoreProject).Methods("POST")
	projectsRouter.HandleFunc("/{id}/duplicate", h.DuplicateProject).Methods("POST")
}

// GetProjects récupère tous les projets avec filtres et pagination
func (h *Handler) GetProjects(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserIDFromContext(r.Context())

	// Paramètres de pagination
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// Filtres
	filters := ProjectFilters{
		UserID:     userID,
		Status:     r.URL.Query().Get("status"),
		Technology: r.URL.Query().Get("technology"),
		Search:     r.URL.Query().Get("search"),
		Tags:       strings.Split(r.URL.Query().Get("tags"), ","),
	}

	// Tri
	sortBy := r.URL.Query().Get("sort_by")
	if sortBy == "" {
		sortBy = "updated_at"
	}
	sortOrder := r.URL.Query().Get("sort_order")
	if sortOrder == "" {
		sortOrder = "desc"
	}

	projects, total, err := h.service.GetProjects(r.Context(), filters, page, limit, sortBy, sortOrder)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get projects")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve projects")
		return
	}

	response := map[string]interface{}{
		"projects": projects,
		"pagination": map[string]interface{}{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": (total + limit - 1) / limit,
		},
	}

	utils.WriteJSONResponse(w, http.StatusOK, response)
}

// CreateProject crée un nouveau projet
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserIDFromContext(r.Context())

	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validation
	if err := h.validator.Validate(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Validation error: %s", err.Error()))
		return
	}

	// Vérification des permissions (RBAC)
	if !auth.HasPermission(r.Context(), "projects:create") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	// Création du projet
	project, err := h.service.CreateProject(r.Context(), userID, &req)
	if err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to create project")
		if strings.Contains(err.Error(), "already exists") {
			utils.WriteErrorResponse(w, http.StatusConflict, "Project with this name already exists")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to create project")
		return
	}

	// Clonage du repository Git si spécifié
	if req.GitURL != "" {
		go func() {
			if err := h.gitClient.CloneRepository(project.ID, req.GitURL, req.GitBranch); err != nil {
				h.logger.WithError(err).WithField("project_id", project.ID).Error("Failed to clone git repository")

				// Notification d'erreur via WebSocket
				h.websocketHub.BroadcastToUser(userID, websocket.Event{
					Type: "project_git_error",
					Data: map[string]interface{}{
						"project_id": project.ID,
						"error":      "Failed to clone git repository",
					},
				})
			} else {
				// Notification de succès
				h.websocketHub.BroadcastToUser(userID, websocket.Event{
					Type: "project_git_cloned",
					Data: map[string]interface{}{
						"project_id": project.ID,
						"message":    "Git repository cloned successfully",
					},
				})
			}
		}()
	}

	// Notification de création
	h.notificationSvc.SendNotification(userID, notification.Notification{
		Type:      "project_created",
		Title:     "Project Created",
		Message:   fmt.Sprintf("Project '%s' has been created successfully", project.Name),
		ProjectID: &project.ID,
	})

	// Événement WebSocket
	h.websocketHub.BroadcastToUser(userID, websocket.Event{
		Type: "project_created",
		Data: project,
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": project.ID,
		"user_id":    userID,
		"name":       project.Name,
	}).Info("Project created successfully")

	utils.WriteJSONResponse(w, http.StatusCreated, project)
}

// GetProject récupère un projet spécifique
func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	project, err := h.service.GetProject(r.Context(), projectID, userID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve project")
		return
	}

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, project)
}

// UpdateProject met à jour un projet
func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	var req UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validation
	if err := h.validator.Validate(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Validation error: %s", err.Error()))
		return
	}

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "write") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	project, err := h.service.UpdateProject(r.Context(), projectID, userID, &req)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to update project")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update project")
		return
	}

	// Notification de mise à jour
	h.notificationSvc.SendNotification(userID, notification.Notification{
		Type:      "project_updated",
		Title:     "Project Updated",
		Message:   fmt.Sprintf("Project '%s' has been updated", project.Name),
		ProjectID: &project.ID,
	})

	// Événement WebSocket
	h.websocketHub.BroadcastToProject(projectID, websocket.Event{
		Type: "project_updated",
		Data: project,
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": projectID,
		"user_id":    userID,
	}).Info("Project updated successfully")

	utils.WriteJSONResponse(w, http.StatusOK, project)
}

// DeleteProject supprime un projet
func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "delete") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	// Récupération du projet pour les notifications
	project, err := h.service.GetProject(r.Context(), projectID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve project")
		return
	}

	// Suppression du projet
	if err := h.service.DeleteProject(r.Context(), projectID, userID); err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to delete project")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to delete project")
		return
	}

	// Notification de suppression
	h.notificationSvc.SendNotification(userID, notification.Notification{
		Type:      "project_deleted",
		Title:     "Project Deleted",
		Message:   fmt.Sprintf("Project '%s' has been deleted", project.Name),
		ProjectID: &project.ID,
	})

	// Événement WebSocket
	h.websocketHub.BroadcastToProject(projectID, websocket.Event{
		Type: "project_deleted",
		Data: map[string]interface{}{
			"project_id": projectID,
			"name":       project.Name,
		},
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": projectID,
		"user_id":    userID,
		"name":       project.Name,
	}).Info("Project deleted successfully")

	utils.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Project deleted successfully",
	})
}

// GetProjectStatus récupère le statut d'un projet
func (h *Handler) GetProjectStatus(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	status, err := h.service.GetProjectStatus(r.Context(), projectID, userID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project status")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve project status")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, status)
}

// GetProjectDeployments récupère les déploiements d'un projet
func (h *Handler) GetProjectDeployments(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	// Paramètres de pagination
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 10
	}

	deployments, total, err := h.service.GetProjectDeployments(r.Context(), projectID, userID, page, limit)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project deployments")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve deployments")
		return
	}

	response := map[string]interface{}{
		"deployments": deployments,
		"pagination": map[string]interface{}{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": (total + limit - 1) / limit,
		},
	}

	utils.WriteJSONResponse(w, http.StatusOK, response)
}

// SyncGitRepository synchronise le repository Git
func (h *Handler) SyncGitRepository(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "write") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	var req SyncGitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Synchronisation asynchrone
	go func() {
		if err := h.service.SyncGitRepository(r.Context(), projectID, userID, req.Branch); err != nil {
			h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to sync git repository")

			// Notification d'erreur
			h.websocketHub.BroadcastToUser(userID, websocket.Event{
				Type: "git_sync_error",
				Data: map[string]interface{}{
					"project_id": projectID,
					"error":      err.Error(),
				},
			})
		} else {
			// Notification de succès
			h.websocketHub.BroadcastToUser(userID, websocket.Event{
				Type: "git_sync_success",
				Data: map[string]interface{}{
					"project_id": projectID,
					"message":    "Git repository synchronized successfully",
				},
			})
		}
	}()

	utils.WriteJSONResponse(w, http.StatusAccepted, map[string]interface{}{
		"message": "Git synchronization started",
	})
}

// GetProjectMetrics récupère les métriques d'un projet
func (h *Handler) GetProjectMetrics(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	// Paramètres de temps
	timeRange := r.URL.Query().Get("time_range")
	if timeRange == "" {
		timeRange = "24h"
	}

	metrics, err := h.service.GetProjectMetrics(r.Context(), projectID, userID, timeRange)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project metrics")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve metrics")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, metrics)
}

// ArchiveProject archive un projet
func (h *Handler) ArchiveProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "write") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	if err := h.service.ArchiveProject(r.Context(), projectID, userID); err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to archive project")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to archive project")
		return
	}

	// Notification d'archivage
	h.websocketHub.BroadcastToProject(projectID, websocket.Event{
		Type: "project_archived",
		Data: map[string]interface{}{
			"project_id":  projectID,
			"archived_at": time.Now(),
		},
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": projectID,
		"user_id":    userID,
	}).Info("Project archived successfully")

	utils.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Project archived successfully",
	})
}

// DuplicateProject duplique un projet
func (h *Handler) DuplicateProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	var req DuplicateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validation
	if err := h.validator.Validate(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Validation error: %s", err.Error()))
		return
	}

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions to read source project")
		return
	}

	if !auth.HasPermission(r.Context(), "projects:create") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions to create project")
		return
	}

	newProject, err := h.service.DuplicateProject(r.Context(), projectID, userID, &req)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to duplicate project")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to duplicate project")
		return
	}

	// Notification de duplication
	h.notificationSvc.SendNotification(userID, notification.Notification{
		Type:      "project_duplicated",
		Title:     "Project Duplicated",
		Message:   fmt.Sprintf("Project '%s' has been duplicated as '%s'", req.SourceName, newProject.Name),
		ProjectID: &newProject.ID,
	})

	h.logger.WithFields(logrus.Fields{
		"source_project_id": projectID,
		"new_project_id":    newProject.ID,
		"user_id":           userID,
	}).Info("Project duplicated successfully")

	utils.WriteJSONResponse(w, http.StatusCreated, newProject)
}

// Handlers supplémentaires pour les fonctionnalités avancées

// GetProjectEnvironments récupère les environnements d'un projet
func (h *Handler) GetProjectEnvironments(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	environments, err := h.service.GetProjectEnvironments(r.Context(), projectID, userID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project environments")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve environments")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, environments)
}

// GetProjectMembers récupère les membres d'un projet
func (h *Handler) GetProjectMembers(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	members, err := h.service.GetProjectMembers(r.Context(), projectID, userID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project members")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve members")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, members)
}

// AddProjectMember ajoute un membre à un projet
func (h *Handler) AddProjectMember(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	var req AddMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !auth.HasProjectAccess(r.Context(), projectID, "admin") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	if err := h.service.AddProjectMember(r.Context(), projectID, userID, &req); err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to add project member")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to add member")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Member added successfully",
	})
}

// RemoveProjectMember supprime un membre d'un projet
func (h *Handler) RemoveProjectMember(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	memberUserID := mux.Vars(r)["userId"]
	currentUserID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "admin") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	if err := h.service.RemoveProjectMember(r.Context(), projectID, memberUserID, currentUserID); err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to remove project member")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to remove member")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Member removed successfully",
	})
}

// GetGitBranches récupère les branches Git d'un projet
func (h *Handler) GetGitBranches(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	branches, err := h.gitClient.GetBranches(projectID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get git branches")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve branches")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, branches)
}

// GetGitCommits récupère les commits Git d'un projet
// GetGitCommits récupère les commits Git d'un projet
func (h *Handler) GetGitCommits(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = "main"
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	commits, err := h.gitClient.GetCommits(projectID, branch, limit)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get git commits")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve commits")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, commits)
}

// GetProjectConfig récupère la configuration d'un projet
func (h *Handler) GetProjectConfig(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	config, err := h.service.GetProjectConfig(r.Context(), projectID, userID)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project config")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project config not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve config")
		return
	}

	utils.WriteJSONResponse(w, http.StatusOK, config)
}

// UpdateProjectConfig met à jour la configuration d'un projet
func (h *Handler) UpdateProjectConfig(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	var req UpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validation
	if err := h.validator.Validate(&req); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Validation error: %s", err.Error()))
		return
	}

	if !auth.HasProjectAccess(r.Context(), projectID, "write") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	config, err := h.service.UpdateProjectConfig(r.Context(), projectID, userID, &req)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to update project config")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to update config")
		return
	}

	// Notification de mise à jour de configuration
	h.websocketHub.BroadcastToProject(projectID, websocket.Event{
		Type: "project_config_updated",
		Data: map[string]interface{}{
			"project_id": projectID,
			"config":     config,
			"updated_at": time.Now(),
		},
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": projectID,
		"user_id":    userID,
	}).Info("Project config updated successfully")

	utils.WriteJSONResponse(w, http.StatusOK, config)
}

// GetProjectLogs récupère les logs d'un projet
func (h *Handler) GetProjectLogs(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	if !auth.HasProjectAccess(r.Context(), projectID, "read") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	// Paramètres de pagination et filtrage
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}

	level := r.URL.Query().Get("level")
	startTime := r.URL.Query().Get("start_time")
	endTime := r.URL.Query().Get("end_time")

	logs, total, err := h.service.GetProjectLogs(r.Context(), projectID, userID, page, limit, level, startTime, endTime)
	if err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to get project logs")
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve logs")
		return
	}

	response := map[string]interface{}{
		"logs": logs,
		"pagination": map[string]interface{}{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": (total + limit - 1) / limit,
		},
	}

	utils.WriteJSONResponse(w, http.StatusOK, response)
}

// RestoreProject restaure un projet archivé
func (h *Handler) RestoreProject(w http.ResponseWriter, r *http.Request) {
	projectID := mux.Vars(r)["id"]
	userID := auth.GetUserIDFromContext(r.Context())

	// Vérification des permissions
	if !auth.HasProjectAccess(r.Context(), projectID, "write") {
		utils.WriteErrorResponse(w, http.StatusForbidden, "Insufficient permissions")
		return
	}

	if err := h.service.RestoreProject(r.Context(), projectID, userID); err != nil {
		h.logger.WithError(err).WithField("project_id", projectID).Error("Failed to restore project")
		if strings.Contains(err.Error(), "not found") {
			utils.WriteErrorResponse(w, http.StatusNotFound, "Project not found")
			return
		}
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "Failed to restore project")
		return
	}

	// Notification de restauration
	h.websocketHub.BroadcastToProject(projectID, websocket.Event{
		Type: "project_restored",
		Data: map[string]interface{}{
			"project_id":  projectID,
			"restored_at": time.Now(),
		},
	})

	h.logger.WithFields(logrus.Fields{
		"project_id": projectID,
		"user_id":    userID,
	}).Info("Project restored successfully")

	utils.WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Project restored successfully",
	})
}
