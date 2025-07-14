package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackship/backend/database"
	"github.com/stackship/backend/services/kubernetes"
	"github.com/stackship/backend/services/notification"
	"github.com/stackship/backend/utils"
	"github.com/stackship/backend/websocket"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// KubernetesManager gère les déploiements Kubernetes
type KubernetesManager struct {
	kubeClient *kubernetes.Client
	db         *database.Connection
	notifier   *notification.Service
	wsHub      *websocket.Hub
	logger     *utils.Logger
}

// NewKubernetesManager crée une nouvelle instance du gestionnaire Kubernetes
func NewKubernetesManager(kubeClient *kubernetes.Client, db *database.Connection, notifier *notification.Service, wsHub *websocket.Hub, logger *utils.Logger) *KubernetesManager {
	return &KubernetesManager{
		kubeClient: kubeClient,
		db:         db,
		notifier:   notifier,
		wsHub:      wsHub,
		logger:     logger,
	}
}

// DeploymentSpec définit les spécifications d'un déploiement
type DeploymentSpec struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Image       string            `json:"image"`
	Replicas    int32             `json:"replicas"`
	Port        int32             `json:"port"`
	Resources   ResourceRequests  `json:"resources"`
	Environment map[string]string `json:"environment"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	HealthCheck HealthCheckConfig `json:"healthCheck"`
}

// ResourceRequests définit les ressources demandées
type ResourceRequests struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// HealthCheckConfig définit la configuration des vérifications de santé
type HealthCheckConfig struct {
	Enabled          bool   `json:"enabled"`
	Path             string `json:"path"`
	Port             int32  `json:"port"`
	InitialDelay     int32  `json:"initialDelay"`
	Period           int32  `json:"period"`
	Timeout          int32  `json:"timeout"`
	FailureThreshold int32  `json:"failureThreshold"`
}

// DeploymentStatus représente le statut d'un déploiement
type DeploymentStatus struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Namespace         string                `json:"namespace"`
	Status            string                `json:"status"`
	ReadyReplicas     int32                 `json:"readyReplicas"`
	TotalReplicas     int32                 `json:"totalReplicas"`
	UpdatedReplicas   int32                 `json:"updatedReplicas"`
	AvailableReplicas int32                 `json:"availableReplicas"`
	Conditions        []DeploymentCondition `json:"conditions"`
	CreatedAt         time.Time             `json:"createdAt"`
	UpdatedAt         time.Time             `json:"updatedAt"`
}

// DeploymentCondition représente une condition du déploiement
type DeploymentCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// CreateDeployment crée un nouveau déploiement Kubernetes
func (km *KubernetesManager) CreateDeployment(ctx context.Context, spec *DeploymentSpec, projectID string, userID string) (*DeploymentStatus, error) {
	km.logger.Info("Creating Kubernetes deployment", map[string]interface{}{
		"name":      spec.Name,
		"namespace": spec.Namespace,
		"project":   projectID,
		"user":      userID,
	})

	// Créer le namespace s'il n'existe pas
	if err := km.ensureNamespace(ctx, spec.Namespace); err != nil {
		return nil, fmt.Errorf("failed to ensure namespace: %w", err)
	}

	// Créer le déploiement Kubernetes
	deployment := km.buildDeployment(spec)

	createdDeployment, err := km.kubeClient.AppsV1().Deployments(spec.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		km.logger.Error("Failed to create deployment", err, map[string]interface{}{
			"name":      spec.Name,
			"namespace": spec.Namespace,
		})
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	// Créer le service
	service := km.buildService(spec)
	_, err = km.kubeClient.CoreV1().Services(spec.Namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		km.logger.Error("Failed to create service", err, map[string]interface{}{
			"name":      spec.Name,
			"namespace": spec.Namespace,
		})
		// On continue même si le service échoue
	}

	// Sauvegarder dans la base de données
	deploymentRecord := &database.Deployment{
		Name:      spec.Name,
		Namespace: spec.Namespace,
		ProjectID: projectID,
		UserID:    userID,
		Status:    "Creating",
		Image:     spec.Image,
		Replicas:  spec.Replicas,
		Config:    km.serializeConfig(spec),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := km.db.CreateDeployment(deploymentRecord); err != nil {
		km.logger.Error("Failed to save deployment to database", err, map[string]interface{}{
			"name": spec.Name,
		})
	}

	// Notifier via WebSocket
	status := km.convertToStatus(createdDeployment, deploymentRecord.ID)
	km.broadcastDeploymentUpdate(status, projectID)

	// Envoyer notification
	go km.sendNotification("deployment_created", spec.Name, userID)

	return status, nil
}

// UpdateDeployment met à jour un déploiement existant
func (km *KubernetesManager) UpdateDeployment(ctx context.Context, deploymentID string, spec *DeploymentSpec) (*DeploymentStatus, error) {
	km.logger.Info("Updating Kubernetes deployment", map[string]interface{}{
		"id":        deploymentID,
		"name":      spec.Name,
		"namespace": spec.Namespace,
	})

	// Récupérer le déploiement existant
	deployment, err := km.kubeClient.AppsV1().Deployments(spec.Namespace).Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	// Mettre à jour les spécifications
	deployment.Spec.Replicas = &spec.Replicas
	deployment.Spec.Template.Spec.Containers[0].Image = spec.Image
	deployment.Spec.Template.Spec.Containers[0].Env = km.buildEnvVars(spec.Environment)

	// Appliquer les mises à jour
	updatedDeployment, err := km.kubeClient.AppsV1().Deployments(spec.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Mettre à jour en base de données
	if err := km.db.UpdateDeploymentStatus(deploymentID, "Updating"); err != nil {
		km.logger.Error("Failed to update deployment status in database", err, nil)
	}

	// Convertir et retourner le statut
	status := km.convertToStatus(updatedDeployment, deploymentID)
	km.broadcastDeploymentUpdate(status, "")

	return status, nil
}

// DeleteDeployment supprime un déploiement
func (km *KubernetesManager) DeleteDeployment(ctx context.Context, deploymentID, namespace, name string) error {
	km.logger.Info("Deleting Kubernetes deployment", map[string]interface{}{
		"id":        deploymentID,
		"name":      name,
		"namespace": namespace,
	})

	// Supprimer le déploiement
	err := km.kubeClient.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	// Supprimer le service
	err = km.kubeClient.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		km.logger.Error("Failed to delete service", err, map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		})
	}

	// Mettre à jour le statut en base de données
	if err := km.db.UpdateDeploymentStatus(deploymentID, "Deleting"); err != nil {
		km.logger.Error("Failed to update deployment status", err, nil)
	}

	return nil
}

// GetDeploymentStatus récupère le statut d'un déploiement
func (km *KubernetesManager) GetDeploymentStatus(ctx context.Context, namespace, name string) (*DeploymentStatus, error) {
	deployment, err := km.kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	// Récupérer l'ID depuis la base de données
	deploymentRecord, err := km.db.GetDeploymentByName(name, namespace)
	if err != nil {
		km.logger.Error("Failed to get deployment from database", err, nil)
		return km.convertToStatus(deployment, ""), nil
	}

	return km.convertToStatus(deployment, deploymentRecord.ID), nil
}

// ScaleDeployment modifie le nombre de réplicas d'un déploiement
func (km *KubernetesManager) ScaleDeployment(ctx context.Context, deploymentID, namespace, name string, replicas int32) error {
	km.logger.Info("Scaling deployment", map[string]interface{}{
		"id":       deploymentID,
		"name":     name,
		"replicas": replicas,
	})

	// Utiliser le service Kubernetes pour le scaling
	err := km.kubeClient.ScaleDeployment(ctx, namespace, name, replicas)
	if err != nil {
		return fmt.Errorf("failed to scale deployment: %w", err)
	}

	// Mettre à jour en base de données
	if err := km.db.UpdateDeploymentReplicas(deploymentID, replicas); err != nil {
		km.logger.Error("Failed to update replicas in database", err, nil)
	}

	return nil
}

// RollbackDeployment effectue un rollback d'un déploiement
func (km *KubernetesManager) RollbackDeployment(ctx context.Context, deploymentID, namespace, name string) error {
	km.logger.Info("Rolling back deployment", map[string]interface{}{
		"id":   deploymentID,
		"name": name,
	})

	// Effectuer le rollback
	err := km.kubeClient.RollbackDeployment(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to rollback deployment: %w", err)
	}

	// Mettre à jour le statut
	if err := km.db.UpdateDeploymentStatus(deploymentID, "Rolling back"); err != nil {
		km.logger.Error("Failed to update deployment status", err, nil)
	}

	return nil
}

// MonitorDeployments surveille les déploiements et met à jour leur statut
func (km *KubernetesManager) MonitorDeployments(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			km.updateDeploymentStatuses(ctx)
		}
	}
}

// Méthodes utilitaires privées

func (km *KubernetesManager) ensureNamespace(ctx context.Context, namespace string) error {
	_, err := km.kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		// Créer le namespace s'il n'existe pas
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		_, err = km.kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
	}
	return nil
}

func (km *KubernetesManager) buildDeployment(spec *DeploymentSpec) *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   spec.Namespace,
			Labels:      spec.Labels,
			Annotations: spec.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": spec.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": spec.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  spec.Name,
							Image: spec.Image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: spec.Port,
								},
							},
							Env: km.buildEnvVars(spec.Environment),
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(spec.Resources.CPU),
									corev1.ResourceMemory: resource.MustParse(spec.Resources.Memory),
								},
							},
						},
					},
				},
			},
		},
	}

	// Ajouter les health checks si activés
	if spec.HealthCheck.Enabled {
		deployment.Spec.Template.Spec.Containers[0].LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: spec.HealthCheck.Path,
					Port: intstr.FromInt(int(spec.HealthCheck.Port)),
				},
			},
			InitialDelaySeconds: spec.HealthCheck.InitialDelay,
			PeriodSeconds:       spec.HealthCheck.Period,
			TimeoutSeconds:      spec.HealthCheck.Timeout,
			FailureThreshold:    spec.HealthCheck.FailureThreshold,
		}
		deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = deployment.Spec.Template.Spec.Containers[0].LivenessProbe
	}

	return deployment
}

func (km *KubernetesManager) buildService(spec *DeploymentSpec) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": spec.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       spec.Port,
					TargetPort: intstr.FromInt(int(spec.Port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

func (km *KubernetesManager) buildEnvVars(env map[string]string) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for key, value := range env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}
	return envVars
}

func (km *KubernetesManager) convertToStatus(deployment *appsv1.Deployment, deploymentID string) *DeploymentStatus {
	var conditions []DeploymentCondition
	for _, condition := range deployment.Status.Conditions {
		conditions = append(conditions, DeploymentCondition{
			Type:    string(condition.Type),
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}

	status := "Unknown"
	if deployment.Status.ReadyReplicas == deployment.Status.Replicas {
		status = "Ready"
	} else if deployment.Status.ReadyReplicas > 0 {
		status = "Partial"
	} else {
		status = "Not Ready"
	}

	return &DeploymentStatus{
		ID:                deploymentID,
		Name:              deployment.Name,
		Namespace:         deployment.Namespace,
		Status:            status,
		ReadyReplicas:     deployment.Status.ReadyReplicas,
		TotalReplicas:     deployment.Status.Replicas,
		UpdatedReplicas:   deployment.Status.UpdatedReplicas,
		AvailableReplicas: deployment.Status.AvailableReplicas,
		Conditions:        conditions,
		CreatedAt:         deployment.CreationTimestamp.Time,
		UpdatedAt:         time.Now(),
	}
}

func (km *KubernetesManager) updateDeploymentStatuses(ctx context.Context) {
	deployments, err := km.db.GetAllDeployments()
	if err != nil {
		km.logger.Error("Failed to get deployments from database", err, nil)
		return
	}

	for _, deployment := range deployments {
		status, err := km.GetDeploymentStatus(ctx, deployment.Namespace, deployment.Name)
		if err != nil {
			km.logger.Error("Failed to get deployment status", err, map[string]interface{}{
				"id":   deployment.ID,
				"name": deployment.Name,
			})
			continue
		}

		// Mettre à jour en base de données si le statut a changé
		if status.Status != deployment.Status {
			if err := km.db.UpdateDeploymentStatus(deployment.ID, status.Status); err != nil {
				km.logger.Error("Failed to update deployment status", err, nil)
			}
		}

		// Diffuser les mises à jour
		km.broadcastDeploymentUpdate(status, deployment.ProjectID)
	}
}

func (km *KubernetesManager) broadcastDeploymentUpdate(status *DeploymentStatus, projectID string) {
	message := websocket.Message{
		Type: "deployment_update",
		Data: map[string]interface{}{
			"deployment": status,
			"project_id": projectID,
		},
		Timestamp: time.Now(),
	}

	km.wsHub.Broadcast <- message
}

func (km *KubernetesManager) sendNotification(eventType, deploymentName, userID string) {
	notification := &notification.Notification{
		Type:      eventType,
		Title:     fmt.Sprintf("Deployment %s", eventType),
		Message:   fmt.Sprintf("Deployment %s has been %s", deploymentName, eventType),
		UserID:    userID,
		Timestamp: time.Now(),
	}

	if err := km.notifier.Send(notification); err != nil {
		km.logger.Error("Failed to send notification", err, map[string]interface{}{
			"type":       eventType,
			"deployment": deploymentName,
			"user":       userID,
		})
	}
}

func (km *KubernetesManager) serializeConfig(spec *DeploymentSpec) string {
	configBytes, err := json.Marshal(spec)
	if err != nil {
		km.logger.Error("Failed to serialize deployment config", err, nil)
		return ""
	}
	return string(configBytes)
}
