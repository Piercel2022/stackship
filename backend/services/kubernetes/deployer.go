package kubernetes

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"stackship/backend/config"
	"stackship/backend/database"
	"stackship/backend/services/docker"
	"stackship/backend/services/notification"
	"stackship/backend/utils"
)

// DeploymentStatus repr�sente le statut d'un d�ploiement
type DeploymentStatus string

const (
	StatusPending   DeploymentStatus = "pending"
	StatusRunning   DeploymentStatus = "running"
	StatusSucceeded DeploymentStatus = "succeeded"
	StatusFailed    DeploymentStatus = "failed"
	StatusRolling   DeploymentStatus = "rolling"
	StatusStopped   DeploymentStatus = "stopped"
)

// DeploymentConfig contient la configuration d'un d�ploiement
type DeploymentConfig struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Image       string            `json:"image"`
	Replicas    int32             `json:"replicas"`
	Port        int32             `json:"port"`
	Env         map[string]string `json:"env"`
	Resources   ResourceConfig    `json:"resources"`
	HealthCheck HealthCheckConfig `json:"healthCheck"`
	Ingress     IngressConfig     `json:"ingress"`
	Secrets     []SecretConfig    `json:"secrets"`
	ConfigMaps  []ConfigMapConfig `json:"configMaps"`
}

// ResourceConfig d�finit les ressources CPU/Memory
type ResourceConfig struct {
	Requests ResourceLimits `json:"requests"`
	Limits   ResourceLimits `json:"limits"`
}

type ResourceLimits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// HealthCheckConfig pour les probes de sant�
type HealthCheckConfig struct {
	LivenessProbe  ProbeConfig `json:"livenessProbe"`
	ReadinessProbe ProbeConfig `json:"readinessProbe"`
}

type ProbeConfig struct {
	Path                string `json:"path"`
	Port                int32  `json:"port"`
	InitialDelaySeconds int32  `json:"initialDelaySeconds"`
	PeriodSeconds       int32  `json:"periodSeconds"`
	TimeoutSeconds      int32  `json:"timeoutSeconds"`
	FailureThreshold    int32  `json:"failureThreshold"`
}

// IngressConfig pour l'exposition externe
type IngressConfig struct {
	Enabled     bool              `json:"enabled"`
	Host        string            `json:"host"`
	TLS         bool              `json:"tls"`
	Annotations map[string]string `json:"annotations"`
}

// SecretConfig pour les secrets Kubernetes
type SecretConfig struct {
	Name string            `json:"name"`
	Data map[string][]byte `json:"data"`
}

// ConfigMapConfig pour les ConfigMaps
type ConfigMapConfig struct {
	Name string            `json:"name"`
	Data map[string]string `json:"data"`
}

// DeploymentResult contient le r�sultat d'un d�ploiement
type DeploymentResult struct {
	Name          string           `json:"name"`
	Namespace     string           `json:"namespace"`
	Status        DeploymentStatus `json:"status"`
	Message       string           `json:"message"`
	Replicas      int32            `json:"replicas"`
	ReadyReplicas int32            `json:"readyReplicas"`
	URL           string           `json:"url"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// Deployer g�re les d�ploiements Kubernetes
type Deployer struct {
	client       *kubernetes.Clientset
	config       *config.Config
	logger       *utils.Logger
	db           *database.DB
	dockerClient *docker.Client
	notifier     *notification.Service
}

// NewDeployer cr�e une nouvelle instance du d�ployeur
func NewDeployer(
	client *kubernetes.Clientset,
	cfg *config.Config,
	logger *utils.Logger,
	db *database.DB,
	dockerClient *docker.Client,
	notifier *notification.Service,
) *Deployer {
	return &Deployer{
		client:       client,
		config:       cfg,
		logger:       logger,
		db:           db,
		dockerClient: dockerClient,
		notifier:     notifier,
	}
}

// Deploy d�ploie une application sur Kubernetes
func (d *Deployer) Deploy(ctx context.Context, config *DeploymentConfig) (*DeploymentResult, error) {
	d.logger.Info("Starting deployment", "name", config.Name, "namespace", config.Namespace)

	// V�rifier si l'image existe
	if err := d.dockerClient.ValidateImage(config.Image); err != nil {
		return nil, fmt.Errorf("invalid image: %w", err)
	}

	// Cr�er le namespace si n�cessaire
	if err := d.ensureNamespace(ctx, config.Namespace); err != nil {
		return nil, fmt.Errorf("failed to ensure namespace: %w", err)
	}

	// Cr�er les secrets
	if err := d.createSecrets(ctx, config); err != nil {
		return nil, fmt.Errorf("failed to create secrets: %w", err)
	}

	// Cr�er les ConfigMaps
	if err := d.createConfigMaps(ctx, config); err != nil {
		return nil, fmt.Errorf("failed to create configmaps: %w", err)
	}

	// Cr�er le d�ploiement
	deployment, err := d.createDeployment(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	// Cr�er le service
	service, err := d.createService(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create service: %w", err)
	}

	// Cr�er l'Ingress si n�cessaire
	var ingressURL string
	if config.Ingress.Enabled {
		if err := d.createIngress(ctx, config); err != nil {
			return nil, fmt.Errorf("failed to create ingress: %w", err)
		}
		ingressURL = d.buildIngressURL(config)
	}

	// Attendre que le d�ploiement soit pr�t
	if err := d.waitForDeploymentReady(ctx, config.Name, config.Namespace, 5*time.Minute); err != nil {
		return nil, fmt.Errorf("deployment not ready: %w", err)
	}

	// Enregistrer le d�ploiement en base de donn�es
	result := &DeploymentResult{
		Name:          config.Name,
		Namespace:     config.Namespace,
		Status:        StatusRunning,
		Message:       "Deployment successful",
		Replicas:      config.Replicas,
		ReadyReplicas: deployment.Status.ReadyReplicas,
		URL:           ingressURL,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := d.saveDeploymentResult(ctx, result); err != nil {
		d.logger.Error("Failed to save deployment result", "error", err)
	}

	// Envoyer une notification
	d.notifier.NotifyDeploymentSuccess(config.Name, config.Namespace, ingressURL)

	d.logger.Info("Deployment completed successfully", "name", config.Name, "namespace", config.Namespace)
	return result, nil
}

// Update met à jour un déploiement existant
func (d *Deployer) Update(ctx context.Context, config *DeploymentConfig) (*DeploymentResult, error) {
	d.logger.Info("Updating deployment", "name", config.Name, "namespace", config.Namespace)

	// V�rifier si le déploiement existe
	existing, err := d.client.AppsV1().Deployments(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("deployment not found: %w", err)
	}

	// Mettre � jour l'image si n�cessaire
	if existing.Spec.Template.Spec.Containers[0].Image != config.Image {
		if err := d.dockerClient.ValidateImage(config.Image); err != nil {
			return nil, fmt.Errorf("invalid image: %w", err)
		}
	}

	// Mettre � jour le d�ploiement
	deployment, err := d.updateDeployment(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Attendre que le rolling update soit termin�
	if err := d.waitForRollingUpdateComplete(ctx, config.Name, config.Namespace, 10*time.Minute); err != nil {
		return nil, fmt.Errorf("rolling update failed: %w", err)
	}

	result := &DeploymentResult{
		Name:          config.Name,
		Namespace:     config.Namespace,
		Status:        StatusSucceeded,
		Message:       "Update successful",
		Replicas:      config.Replicas,
		ReadyReplicas: deployment.Status.ReadyReplicas,
		UpdatedAt:     time.Now(),
	}

	if err := d.saveDeploymentResult(ctx, result); err != nil {
		d.logger.Error("Failed to save deployment result", "error", err)
	}

	d.notifier.NotifyDeploymentUpdate(config.Name, config.Namespace)
	return result, nil
}

// Delete supprime un d�ploiement
func (d *Deployer) Delete(ctx context.Context, name, namespace string) error {
	d.logger.Info("Deleting deployment", "name", name, "namespace", namespace)

	// Supprimer le d�ploiement
	if err := d.client.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	// Supprimer le service
	if err := d.client.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete service", "error", err)
	}

	// Supprimer l'Ingress
	if err := d.client.NetworkingV1().Ingresses(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete ingress", "error", err)
	}

	// Mettre � jour le statut en base
	result := &DeploymentResult{
		Name:      name,
		Namespace: namespace,
		Status:    StatusStopped,
		Message:   "Deployment deleted",
		UpdatedAt: time.Now(),
	}

	if err := d.saveDeploymentResult(ctx, result); err != nil {
		d.logger.Error("Failed to save deployment result", "error", err)
	}

	d.notifier.NotifyDeploymentDeleted(name, namespace)
	return nil
}

// GetStatus retourne le statut d'un d�ploiement
func (d *Deployer) GetStatus(ctx context.Context, name, namespace string) (*DeploymentResult, error) {
	deployment, err := d.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("deployment not found: %w", err)
	}

	status := d.determineDeploymentStatus(deployment)

	return &DeploymentResult{
		Name:          name,
		Namespace:     namespace,
		Status:        status,
		Replicas:      *deployment.Spec.Replicas,
		ReadyReplicas: deployment.Status.ReadyReplicas,
		UpdatedAt:     time.Now(),
	}, nil
}

// GetLogs retourne les logs d'un d�ploiement
func (d *Deployer) GetLogs(ctx context.Context, name, namespace string, lines int64) ([]string, error) {
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var logs []string
	for _, pod := range pods.Items {
		req := d.client.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			TailLines: &lines,
		})

		podLogs, err := req.Stream(ctx)
		if err != nil {
			d.logger.Warn("Failed to get pod logs", "pod", pod.Name, "error", err)
			continue
		}
		defer podLogs.Close()

		// Lire les logs (impl�mentation simplifiée)
		// Dans un vrai projet, vous devriez utiliser un scanner appropri�
		logs = append(logs, fmt.Sprintf("Pod: %s", pod.Name))
	}

	return logs, nil
}

// Rollback effectue un rollback vers une version précédente
func (d *Deployer) Rollback(ctx context.Context, name, namespace string, revision int64) error {
	d.logger.Info("Rolling back deployment", "name", name, "namespace", namespace, "revision", revision)

	// Effectuer le rollback
	rollbackBody := &appsv1.DeploymentRollback{
		Name: name,
		RollbackTo: appsv1.RollbackConfig{
			Revision: revision,
		},
	}

	_, err := d.client.AppsV1().Deployments(namespace).Rollback(ctx, rollbackBody, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to rollback: %w", err)
	}

	// Attendre que le rollback soit termin�
	if err := d.waitForRollingUpdateComplete(ctx, name, namespace, 10*time.Minute); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	d.notifier.NotifyDeploymentRollback(name, namespace, revision)
	return nil
}

// ensureNamespace crée le namespace s'il n'existe pas
func (d *Deployer) ensureNamespace(ctx context.Context, namespace string) error {
	_, err := d.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil // Le namespace existe déjà
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err = d.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	return err
}

// createSecrets crée les secrets Kubernetes
func (d *Deployer) createSecrets(ctx context.Context, config *DeploymentConfig) error {
	for _, secretConfig := range config.Secrets {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretConfig.Name,
				Namespace: config.Namespace,
			},
			Data: secretConfig.Data,
		}

		_, err := d.client.CoreV1().Secrets(config.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create secret %s: %w", secretConfig.Name, err)
		}
	}
	return nil
}

// createConfigMaps crée les ConfigMaps
func (d *Deployer) createConfigMaps(ctx context.Context, config *DeploymentConfig) error {
	for _, cmConfig := range config.ConfigMaps {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmConfig.Name,
				Namespace: config.Namespace,
			},
			Data: cmConfig.Data,
		}

		_, err := d.client.CoreV1().ConfigMaps(config.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create configmap %s: %w", cmConfig.Name, err)
		}
	}
	return nil
}

// createDeployment crée le déploiement Kubernetes
func (d *Deployer) createDeployment(ctx context.Context, config *DeploymentConfig) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"app":        config.Name,
				"managed-by": "stackship",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &config.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": config.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": config.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  config.Name,
							Image: config.Image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: config.Port,
								},
							},
							Env: d.buildEnvVars(config.Env),
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(config.Resources.Requests.CPU),
									corev1.ResourceMemory: resource.MustParse(config.Resources.Requests.Memory),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(config.Resources.Limits.CPU),
									corev1.ResourceMemory: resource.MustParse(config.Resources.Limits.Memory),
								},
							},
							LivenessProbe:  d.buildProbe(config.HealthCheck.LivenessProbe),
							ReadinessProbe: d.buildProbe(config.HealthCheck.ReadinessProbe),
						},
					},
				},
			},
		},
	}

	return d.client.AppsV1().Deployments(config.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
}

// createService crée le service Kubernetes
func (d *Deployer) createService(ctx context.Context, config *DeploymentConfig) (*corev1.Service, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"app":        config.Name,
				"managed-by": "stackship",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": config.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(int(config.Port)),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	return d.client.CoreV1().Services(config.Namespace).Create(ctx, service, metav1.CreateOptions{})
}

// createIngress crée l'Ingress pour l'exposition externe
func (d *Deployer) createIngress(ctx context.Context, config *DeploymentConfig) error {
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        config.Name,
			Namespace:   config.Namespace,
			Annotations: config.Ingress.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: config.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: config.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if config.Ingress.TLS {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{config.Ingress.Host},
				SecretName: fmt.Sprintf("%s-tls", config.Name),
			},
		}
	}

	_, err := d.client.NetworkingV1().Ingresses(config.Namespace).Create(ctx, ingress, metav1.CreateOptions{})
	return err
}

// buildEnvVars convertit les variables d'environnement
func (d *Deployer) buildEnvVars(envMap map[string]string) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for key, value := range envMap {
		envVars = append(envVars, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}
	return envVars
}

// buildProbe cr�e une probe Kubernetes
func (d *Deployer) buildProbe(config ProbeConfig) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: config.Path,
				Port: intstr.FromInt(int(config.Port)),
			},
		},
		InitialDelaySeconds: config.InitialDelaySeconds,
		PeriodSeconds:       config.PeriodSeconds,
		TimeoutSeconds:      config.TimeoutSeconds,
		FailureThreshold:    config.FailureThreshold,
	}
}

// waitForDeploymentReady attend que le d�ploiement soit pr�t
func (d *Deployer) waitForDeploymentReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			deployment, err := d.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
				return nil
			}

			time.Sleep(5 * time.Second)
		}
	}
}

// waitForRollingUpdateComplete attend que le rolling update soit termin�
func (d *Deployer) waitForRollingUpdateComplete(ctx context.Context, name, namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			deployment, err := d.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			if deployment.Status.UpdatedReplicas == *deployment.Spec.Replicas &&
				deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
				return nil
			}

			time.Sleep(5 * time.Second)
		}
	}
}

// determineDeploymentStatus d�termine le statut d'un d�ploiement
func (d *Deployer) determineDeploymentStatus(deployment *appsv1.Deployment) DeploymentStatus {
	if deployment.Status.ReadyReplicas == 0 {
		return StatusPending
	}

	if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
		return StatusRunning
	}

	if deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
		return StatusRolling
	}

	return StatusFailed
}

// buildIngressURL construit l'URL d'acc�s via Ingress
func (d *Deployer) buildIngressURL(config *DeploymentConfig) string {
	protocol := "http"
	if config.Ingress.TLS {
		protocol = "https"
	}
	return fmt.Sprintf("%s://%s", protocol, config.Ingress.Host)
}

// saveDeploymentResult sauvegarde le résultat du déploiement
func (d *Deployer) saveDeploymentResult(ctx context.Context, result *DeploymentResult) error {
	// Ici, vous int�greriez avec votre couche de base de donn�es
	// Par exemple, en utilisant une fonction de votre package database
	return d.db.SaveDeploymentResult(ctx, result)
}

// updateDeployment met à jour un déploiement existant
func (d *Deployer) updateDeployment(ctx context.Context, config *DeploymentConfig) (*appsv1.Deployment, error) {
	deployment, err := d.client.AppsV1().Deployments(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Mettre à jour l'image
	deployment.Spec.Template.Spec.Containers[0].Image = config.Image

	// Mettre à jour les r�plicas
	deployment.Spec.Replicas = &config.Replicas

	// Mettre à jour les variables d'environnement
	deployment.Spec.Template.Spec.Containers[0].Env = d.buildEnvVars(config.Env)

	return d.client.AppsV1().Deployments(config.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
}
