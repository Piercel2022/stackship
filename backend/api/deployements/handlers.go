package deployments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"stackship/backend/api/websocket"
	"stackship/backend/database"
	"stackship/backend/services/docker"
	"stackship/backend/services/kubernetes"
	"stackship/backend/services/notification"
	"stackship/backend/utils"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// DeploymentHandler struct contient toutes les dépendances pour les handlers de déploiement
type DeploymentHandler struct {
	db            *database.DB
	dockerService *docker.Service
	k8sService    *kubernetes.Service
	notifier      *notification.Service
	wsHub         *websocket.Hub
	logger        *utils.Logger
	validator     *utils.Validator
}

// NewDeploymentHandler crée une nouvelle instance du handler de déploiement
func NewDeploymentHandler(db *database.DB, dockerSvc *docker.Service, k8sSvc *kubernetes.Service,
	notifier *notification.Service, wsHub *websocket.Hub, logger *utils.Logger, validator *utils.Validator) *DeploymentHandler {
	return &DeploymentHandler{
		db:            db,
		dockerService: dockerSvc,
		k8sService:    k8sSvc,
		notifier:      notifier,
		wsHub:         wsHub,
		logger:        logger,
		validator:     validator,
	}
}

// CreateDeployment crée un nouveau déploiement
func (h *DeploymentHandler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req CreateDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode deployment request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validation des données
	if err := h.validator.ValidateStruct(req); err != nil {
		h.logger.Error("Validation failed", "error", err)
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}

	// Vérification de l'existence du projet
	project, err := h.db.GetProject(req.ProjectID)
	if err != nil {
		h.logger.Error("Failed to get project", "project_id", req.ProjectID, "error", err)
		http.Error(w, "Project not found", http.StatusNotFound)
		return
	}

	// Création du déploiement en base
	deployment := &database.Deployment{
		ProjectID:   req.ProjectID,
		Environment: req.Environment,
		Branch:      req.Branch,
		CommitSHA:   req.CommitSHA,
		Status:      "pending",
		Config:      req.Config,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	deploymentID, err := h.db.CreateDeployment(deployment)
	if err != nil {
		h.logger.Error("Failed to create deployment", "error", err)
		http.Error(w, "Failed to create deployment", http.StatusInternalServerError)
		return
	}

	deployment.ID = deploymentID

	// Notification WebSocket de création
	h.wsHub.Broadcast(websocket.Event{
		Type: "deployment_created",
		Data: deployment,
	})

	// Démarrage du processus de déploiement en arrière-plan
	go h.processDeployment(deployment, project)

	// Réponse
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      deploymentID,
		"status":  "pending",
		"message": "Deployment created successfully",
	})
}

// GetDeployments récupère la liste des déploiements avec pagination
func (h *DeploymentHandler) GetDeployments(w http.ResponseWriter, r *http.Request) {
	// Paramètres de requête
	projectID := r.URL.Query().Get("project_id")
	environment := r.URL.Query().Get("environment")
	status := r.URL.Query().Get("status")

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Récupération des déploiements
	deployments, total, err := h.db.GetDeployments(database.DeploymentFilter{
		ProjectID:   projectID,
		Environment: environment,
		Status:      status,
		Page:        page,
		Limit:       limit,
	})
	if err != nil {
		h.logger.Error("Failed to get deployments", "error", err)
		http.Error(w, "Failed to get deployments", http.StatusInternalServerError)
		return
	}

	// Réponse avec métadonnées de pagination
	response := map[string]interface{}{
		"deployments": deployments,
		"pagination": map[string]interface{}{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": (total + limit - 1) / limit,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetDeployment récupère un déploiement spécifique
func (h *DeploymentHandler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deploymentID := vars["id"]

	deployment, err := h.db.GetDeployment(deploymentID)
	if err != nil {
		h.logger.Error("Failed to get deployment", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deployment)
}

// GetDeploymentLogs récupère les logs d'un déploiement
func (h *DeploymentHandler) GetDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deploymentID := vars["id"]

	// Vérification de l'existence du déploiement
	deployment, err := h.db.GetDeployment(deploymentID)
	if err != nil {
		h.logger.Error("Failed to get deployment", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Récupération des logs selon l'environnement
	var logs []string
	if deployment.Environment == "kubernetes" {
		logs, err = h.k8sService.GetDeploymentLogs(deploymentID)
	} else {
		logs, err = h.dockerService.GetContainerLogs(deployment.ContainerID)
	}

	if err != nil {
		h.logger.Error("Failed to get deployment logs", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Failed to get logs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	})
}

// RollbackDeployment effectue un rollback d'un déploiement
func (h *DeploymentHandler) RollbackDeployment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deploymentID := vars["id"]

	// Vérification de l'existence du déploiement
	deployment, err := h.db.GetDeployment(deploymentID)
	if err != nil {
		h.logger.Error("Failed to get deployment", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Vérification que le déploiement peut être rollback
	if deployment.Status != "success" {
		http.Error(w, "Only successful deployments can be rolled back", http.StatusBadRequest)
		return
	}

	// Création d'un nouveau déploiement pour le rollback
	rollbackDeployment := &database.Deployment{
		ProjectID:    deployment.ProjectID,
		Environment:  deployment.Environment,
		Branch:       deployment.Branch,
		CommitSHA:    deployment.PreviousCommitSHA,
		Status:       "pending",
		Config:       deployment.Config,
		IsRollback:   true,
		RollbackFrom: deploymentID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	rollbackID, err := h.db.CreateDeployment(rollbackDeployment)
	if err != nil {
		h.logger.Error("Failed to create rollback deployment", "error", err)
		http.Error(w, "Failed to create rollback", http.StatusInternalServerError)
		return
	}

	rollbackDeployment.ID = rollbackID

	// Notification WebSocket
	h.wsHub.Broadcast(websocket.Event{
		Type: "rollback_started",
		Data: rollbackDeployment,
	})

	// Démarrage du processus de rollback
	go h.processRollback(rollbackDeployment, deployment)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      rollbackID,
		"status":  "pending",
		"message": "Rollback started successfully",
	})
}

// DeleteDeployment supprime un déploiement
func (h *DeploymentHandler) DeleteDeployment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deploymentID := vars["id"]

	// Vérification de l'existence du déploiement
	deployment, err := h.db.GetDeployment(deploymentID)
	if err != nil {
		h.logger.Error("Failed to get deployment", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Suppression des ressources selon l'environnement
	if deployment.Environment == "kubernetes" {
		if err := h.k8sService.DeleteDeployment(deploymentID); err != nil {
			h.logger.Error("Failed to delete K8s deployment", "deployment_id", deploymentID, "error", err)
			http.Error(w, "Failed to delete deployment resources", http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.dockerService.StopAndRemoveContainer(deployment.ContainerID); err != nil {
			h.logger.Error("Failed to stop Docker container", "deployment_id", deploymentID, "error", err)
			http.Error(w, "Failed to stop deployment", http.StatusInternalServerError)
			return
		}
	}

	// Suppression en base
	if err := h.db.DeleteDeployment(deploymentID); err != nil {
		h.logger.Error("Failed to delete deployment from database", "deployment_id", deploymentID, "error", err)
		http.Error(w, "Failed to delete deployment", http.StatusInternalServerError)
		return
	}

	// Notification WebSocket
	h.wsHub.Broadcast(websocket.Event{
		Type: "deployment_deleted",
		Data: map[string]string{"id": deploymentID},
	})

	w.WriteHeader(http.StatusNoContent)
}

// processDeployment traite un déploiement de manière asynchrone
func (h *DeploymentHandler) processDeployment(deployment *database.Deployment, project *database.Project) {
	h.logger.Info("Starting deployment process", "deployment_id", deployment.ID)

	// Mise à jour du statut à "building"
	h.updateDeploymentStatus(deployment.ID, "building", "Building application...")

	// Phase 1: Build Docker image
	imageTag, err := h.dockerService.BuildImage(docker.BuildRequest{
		ProjectID:  deployment.ProjectID,
		Branch:     deployment.Branch,
		CommitSHA:  deployment.CommitSHA,
		Dockerfile: project.DockerfilePath,
		Context:    project.SourcePath,
	})
	if err != nil {
		h.logger.Error("Failed to build Docker image", "deployment_id", deployment.ID, "error", err)
		h.updateDeploymentStatus(deployment.ID, "failed", fmt.Sprintf("Build failed: %v", err))
		h.notifyDeploymentFailed(deployment, err)
		return
	}

	// Mise à jour du statut à "deploying"
	h.updateDeploymentStatus(deployment.ID, "deploying", "Deploying application...")

	// Phase 2: Deploy selon l'environnement
	var deployResult interface{}
	if deployment.Environment == "kubernetes" {
		deployResult, err = h.k8sService.Deploy(kubernetes.DeployRequest{
			DeploymentID: deployment.ID,
			ImageTag:     imageTag,
			Config:       deployment.Config,
			Environment:  deployment.Environment,
		})
	} else {
		deployResult, err = h.dockerService.Deploy(docker.DeployRequest{
			DeploymentID: deployment.ID,
			ImageTag:     imageTag,
			Config:       deployment.Config,
			Environment:  deployment.Environment,
		})
	}

	if err != nil {
		h.logger.Error("Failed to deploy application", "deployment_id", deployment.ID, "error", err)
		h.updateDeploymentStatus(deployment.ID, "failed", fmt.Sprintf("Deployment failed: %v", err))
		h.notifyDeploymentFailed(deployment, err)
		return
	}

	// Mise à jour du statut à "success"
	h.updateDeploymentStatus(deployment.ID, "success", "Deployment completed successfully")

	// Mise à jour des informations de déploiement
	if containerInfo, ok := deployResult.(*docker.ContainerInfo); ok {
		h.db.UpdateDeploymentInfo(deployment.ID, containerInfo.ID, containerInfo.URL)
	}

	// Notification de succès
	h.notifyDeploymentSuccess(deployment)

	h.logger.Info("Deployment completed successfully", "deployment_id", deployment.ID)
}

// processRollback traite un rollback de manière asynchrone
func (h *DeploymentHandler) processRollback(rollbackDeployment *database.Deployment, originalDeployment *database.Deployment) {
	h.logger.Info("Starting rollback process", "rollback_id", rollbackDeployment.ID, "original_id", originalDeployment.ID)

	// Mise à jour du statut à "rolling_back"
	h.updateDeploymentStatus(rollbackDeployment.ID, "rolling_back", "Rolling back deployment...")

	// Rollback selon l'environnement
	var err error
	if originalDeployment.Environment == "kubernetes" {
		err = h.k8sService.Rollback(originalDeployment.ID, rollbackDeployment.ID)
	} else {
		err = h.dockerService.Rollback(originalDeployment.ID, rollbackDeployment.ID)
	}

	if err != nil {
		h.logger.Error("Failed to rollback deployment", "rollback_id", rollbackDeployment.ID, "error", err)
		h.updateDeploymentStatus(rollbackDeployment.ID, "failed", fmt.Sprintf("Rollback failed: %v", err))
		h.notifyRollbackFailed(rollbackDeployment, err)
		return
	}

	// Mise à jour du statut à "success"
	h.updateDeploymentStatus(rollbackDeployment.ID, "success", "Rollback completed successfully")

	// Notification de succès
	h.notifyRollbackSuccess(rollbackDeployment)

	h.logger.Info("Rollback completed successfully", "rollback_id", rollbackDeployment.ID)
}

// updateDeploymentStatus met à jour le statut d'un déploiement
func (h *DeploymentHandler) updateDeploymentStatus(deploymentID, status, message string) {
	if err := h.db.UpdateDeploymentStatus(deploymentID, status, message); err != nil {
		h.logger.Error("Failed to update deployment status", "deployment_id", deploymentID, "error", err)
		return
	}

	// Notification WebSocket
	h.wsHub.Broadcast(websocket.Event{
		Type: "deployment_status_updated",
		Data: map[string]interface{}{
			"id":      deploymentID,
			"status":  status,
			"message": message,
		},
	})
}

// Fonctions de notification
func (h *DeploymentHandler) notifyDeploymentSuccess(deployment *database.Deployment) {
	h.notifier.SendNotification(notification.Notification{
		Type:    "deployment_success",
		Title:   "Deployment Successful",
		Message: fmt.Sprintf("Deployment %s completed successfully", deployment.ID),
		Data:    deployment,
	})
}

func (h *DeploymentHandler) notifyDeploymentFailed(deployment *database.Deployment, err error) {
	h.notifier.SendNotification(notification.Notification{
		Type:    "deployment_failed",
		Title:   "Deployment Failed",
		Message: fmt.Sprintf("Deployment %s failed: %v", deployment.ID, err),
		Data:    deployment,
	})
}

func (h *DeploymentHandler) notifyRollbackSuccess(deployment *database.Deployment) {
	h.notifier.SendNotification(notification.Notification{
		Type:    "rollback_success",
		Title:   "Rollback Successful",
		Message: fmt.Sprintf("Rollback %s completed successfully", deployment.ID),
		Data:    deployment,
	})
}

func (h *DeploymentHandler) notifyRollbackFailed(deployment *database.Deployment, err error) {
	h.notifier.SendNotification(notification.Notification{
		Type:    "rollback_failed",
		Title:   "Rollback Failed",
		Message: fmt.Sprintf("Rollback %s failed: %v", deployment.ID, err),
		Data:    deployment,
	})
}
