package kubernetes

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/retry"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client représente le client Kubernetes pour StackShip
type Client struct {
	clientset     *kubernetes.Clientset
	metricsClient *metricsclient.Clientset
	config        *rest.Config
	logger        *logrus.Logger
	namespace     string
}

// Config contient la configuration pour le client Kubernetes
type Config struct {
	KubeConfig     string
	Namespace      string
	InCluster      bool
	RequestTimeout time.Duration
	RetryAttempts  int
}

// DeploymentSpec représente les spécifications d'un déploiement
type DeploymentSpec struct {
	Name            string
	Namespace       string
	Image           string
	Replicas        int32
	Port            int32
	Environment     map[string]string
	Resources       ResourceRequirements
	Labels          map[string]string
	Annotations     map[string]string
	HealthCheck     HealthCheckConfig
	SecurityContext SecurityContextConfig
}

// ResourceRequirements définit les ressources CPU et mémoire
type ResourceRequirements struct {
	Limits   ResourceList
	Requests ResourceList
}

// ResourceList définit les limites de ressources
type ResourceList struct {
	CPU    string
	Memory string
}

// HealthCheckConfig définit la configuration des health checks
type HealthCheckConfig struct {
	LivenessProbe  ProbeConfig
	ReadinessProbe ProbeConfig
}

// ProbeConfig définit la configuration d'une probe
type ProbeConfig struct {
	Path                string
	Port                int32
	InitialDelaySeconds int32
	PeriodSeconds       int32
	TimeoutSeconds      int32
	FailureThreshold    int32
}

// SecurityContextConfig définit le contexte de sécurité
type SecurityContextConfig struct {
	RunAsNonRoot             bool
	RunAsUser                *int64
	ReadOnlyRootFilesystem   bool
	AllowPrivilegeEscalation bool
}

// ServiceSpec représente les spécifications d'un service
type ServiceSpec struct {
	Name        string
	Namespace   string
	Selector    map[string]string
	Ports       []ServicePort
	ServiceType corev1.ServiceType
	Labels      map[string]string
}

// ServicePort définit un port de service
type ServicePort struct {
	Name       string
	Port       int32
	TargetPort int32
	Protocol   corev1.Protocol
}

// IngressSpec représente les spécifications d'un ingress
type IngressSpec struct {
	Name        string
	Namespace   string
	Host        string
	Paths       []IngressPath
	TLS         []IngressTLS
	Annotations map[string]string
	Labels      map[string]string
}

// IngressPath définit un chemin d'ingress
type IngressPath struct {
	Path        string
	PathType    networkingv1.PathType
	ServiceName string
	ServicePort int32
}

// IngressTLS définit la configuration TLS
type IngressTLS struct {
	Hosts      []string
	SecretName string
}

// PodMetrics représente les métriques d'un pod
type PodMetrics struct {
	Name        string
	Namespace   string
	CPUUsage    string
	MemoryUsage string
	Timestamp   time.Time
}

// DeploymentStatus représente l'état d'un déploiement
type DeploymentStatus struct {
	Name                string
	Namespace           string
	Replicas            int32
	ReadyReplicas       int32
	AvailableReplicas   int32
	UnavailableReplicas int32
	Conditions          []appsv1.DeploymentCondition
	ObservedGeneration  int64
}

// EventWatcher permet de surveiller les événements Kubernetes
type EventWatcher struct {
	client    *Client
	namespace string
	stopCh    chan struct{}
}

// NewClient crée une nouvelle instance du client Kubernetes
func NewClient(config Config, logger *logrus.Logger) (*Client, error) {
	var kubeConfig *rest.Config
	var err error

	if config.InCluster {
		kubeConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	} else {
		if config.KubeConfig == "" {
			if home := homedir.HomeDir(); home != "" {
				config.KubeConfig = filepath.Join(home, ".kube", "config")
			}
		}

		kubeConfig, err = clientcmd.BuildConfigFromFlags("", config.KubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from flags: %w", err)
		}
	}

	// Configuration du timeout
	if config.RequestTimeout > 0 {
		kubeConfig.Timeout = config.RequestTimeout
	}

	// Création du clientset
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	// Création du client de métriques
	metricsClient, err := metricsclient.NewForConfig(kubeConfig)
	if err != nil {
		logger.Warnf("Failed to create metrics client: %v", err)
		metricsClient = nil
	}

	client := &Client{
		clientset:     clientset,
		metricsClient: metricsClient,
		config:        kubeConfig,
		logger:        logger,
		namespace:     config.Namespace,
	}

	// Test de connexion
	if err := client.testConnection(); err != nil {
		return nil, fmt.Errorf("failed to connect to Kubernetes: %w", err)
	}

	logger.Info("Successfully connected to Kubernetes cluster")
	return client, nil
}

// testConnection teste la connexion au cluster Kubernetes
func (c *Client) testConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	return err
}

// CreateNamespace crée un namespace
func (c *Client) CreateNamespace(ctx context.Context, name string, labels map[string]string) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}

	_, err := c.clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s: %w", name, err)
	}

	c.logger.Infof("Namespace %s created successfully", name)
	return nil
}

// CreateDeployment crée un déploiement
func (c *Client) CreateDeployment(ctx context.Context, spec DeploymentSpec) (*appsv1.Deployment, error) {
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
				MatchLabels: spec.Labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: spec.Labels,
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
							Env: c.buildEnvVars(spec.Environment),
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(spec.Resources.Limits.CPU),
									corev1.ResourceMemory: resource.MustParse(spec.Resources.Limits.Memory),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(spec.Resources.Requests.CPU),
									corev1.ResourceMemory: resource.MustParse(spec.Resources.Requests.Memory),
								},
							},
							LivenessProbe:  c.buildProbe(spec.HealthCheck.LivenessProbe),
							ReadinessProbe: c.buildProbe(spec.HealthCheck.ReadinessProbe),
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             &spec.SecurityContext.RunAsNonRoot,
								RunAsUser:                spec.SecurityContext.RunAsUser,
								ReadOnlyRootFilesystem:   &spec.SecurityContext.ReadOnlyRootFilesystem,
								AllowPrivilegeEscalation: &spec.SecurityContext.AllowPrivilegeEscalation,
							},
						},
					},
				},
			},
		},
	}

	result, err := c.clientset.AppsV1().Deployments(spec.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment %s: %w", spec.Name, err)
	}

	c.logger.Infof("Deployment %s created successfully", spec.Name)
	return result, nil
}

// UpdateDeployment met à jour un déploiement
func (c *Client) UpdateDeployment(ctx context.Context, spec DeploymentSpec) (*appsv1.Deployment, error) {
	return c.updateDeploymentWithRetry(ctx, spec, 3)
}

// updateDeploymentWithRetry met à jour un déploiement avec retry
func (c *Client) updateDeploymentWithRetry(ctx context.Context, spec DeploymentSpec, maxRetries int) (*appsv1.Deployment, error) {
	var result *appsv1.Deployment

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment, err := c.clientset.AppsV1().Deployments(spec.Namespace).Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// Mise à jour des spécifications
		deployment.Spec.Replicas = &spec.Replicas
		deployment.Spec.Template.Spec.Containers[0].Image = spec.Image
		deployment.Spec.Template.Spec.Containers[0].Env = c.buildEnvVars(spec.Environment)
		deployment.Labels = spec.Labels
		deployment.Annotations = spec.Annotations

		result, err = c.clientset.AppsV1().Deployments(spec.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to update deployment %s: %w", spec.Name, err)
	}

	c.logger.Infof("Deployment %s updated successfully", spec.Name)
	return result, nil
}

// DeleteDeployment supprime un déploiement
func (c *Client) DeleteDeployment(ctx context.Context, name, namespace string) error {
	err := c.clientset.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment %s: %w", name, err)
	}

	c.logger.Infof("Deployment %s deleted successfully", name)
	return nil
}

// GetDeployment récupère un déploiement
func (c *Client) GetDeployment(ctx context.Context, name, namespace string) (*appsv1.Deployment, error) {
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment %s: %w", name, err)
	}

	return deployment, nil
}

// GetDeploymentStatus récupère le statut d'un déploiement
func (c *Client) GetDeploymentStatus(ctx context.Context, name, namespace string) (*DeploymentStatus, error) {
	deployment, err := c.GetDeployment(ctx, name, namespace)
	if err != nil {
		return nil, err
	}

	return &DeploymentStatus{
		Name:                deployment.Name,
		Namespace:           deployment.Namespace,
		Replicas:            deployment.Status.Replicas,
		ReadyReplicas:       deployment.Status.ReadyReplicas,
		AvailableReplicas:   deployment.Status.AvailableReplicas,
		UnavailableReplicas: deployment.Status.UnavailableReplicas,
		Conditions:          deployment.Status.Conditions,
		ObservedGeneration:  deployment.Status.ObservedGeneration,
	}, nil
}

// ScaleDeployment met à l'échelle un déploiement
func (c *Client) ScaleDeployment(ctx context.Context, name, namespace string, replicas int32) error {
	scale, err := c.clientset.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale for deployment %s: %w", name, err)
	}

	scale.Spec.Replicas = replicas
	_, err = c.clientset.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale deployment %s: %w", name, err)
	}

	c.logger.Infof("Deployment %s scaled to %d replicas", name, replicas)
	return nil
}

// CreateService crée un service
func (c *Client) CreateService(ctx context.Context, spec ServiceSpec) (*corev1.Service, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: spec.Selector,
			Type:     spec.ServiceType,
			Ports:    c.buildServicePorts(spec.Ports),
		},
	}

	result, err := c.clientset.CoreV1().Services(spec.Namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create service %s: %w", spec.Name, err)
	}

	c.logger.Infof("Service %s created successfully", spec.Name)
	return result, nil
}

// CreateIngress crée un ingress
func (c *Client) CreateIngress(ctx context.Context, spec IngressSpec) (*networkingv1.Ingress, error) {
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   spec.Namespace,
			Labels:      spec.Labels,
			Annotations: spec.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: spec.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: c.buildIngressPaths(spec.Paths),
						},
					},
				},
			},
			TLS: c.buildIngressTLS(spec.TLS),
		},
	}

	result, err := c.clientset.NetworkingV1().Ingresses(spec.Namespace).Create(ctx, ingress, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create ingress %s: %w", spec.Name, err)
	}

	c.logger.Infof("Ingress %s created successfully", spec.Name)
	return result, nil
}

// GetPods récupère les pods d'un déploiement
func (c *Client) GetPods(ctx context.Context, namespace string, labelSelector map[string]string) (*corev1.PodList, error) {
	selector := labels.SelectorFromSet(labelSelector)
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}

	return pods, nil
}

// GetPodLogs récupère les logs d'un pod
func (c *Client) GetPodLogs(ctx context.Context, namespace, podName string, lines int64) (string, error) {
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &lines,
	})

	logs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer logs.Close()

	buf := make([]byte, 2048)
	var result string
	for {
		n, err := logs.Read(buf)
		if n > 0 {
			result += string(buf[:n])
		}
		if err != nil {
			break
		}
	}

	return result, nil
}

// GetPodMetrics récupère les métriques des pods
func (c *Client) GetPodMetrics(ctx context.Context, namespace string, labelSelector map[string]string) ([]PodMetrics, error) {
	if c.metricsClient == nil {
		return nil, fmt.Errorf("metrics client not available")
	}

	selector := labels.SelectorFromSet(labelSelector)
	metrics, err := c.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	var result []PodMetrics
	for _, metric := range metrics.Items {
		if len(metric.Containers) > 0 {
			container := metric.Containers[0]
			result = append(result, PodMetrics{
				Name:        metric.Name,
				Namespace:   metric.Namespace,
				CPUUsage:    container.Usage.Cpu().String(),
				MemoryUsage: container.Usage.Memory().String(),
				Timestamp:   metric.Timestamp.Time,
			})
		}
	}

	return result, nil
}

// WaitForDeploymentReady attend qu'un déploiement soit prêt
func (c *Client) WaitForDeploymentReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := c.clientset.AppsV1().Deployments(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
	})
	if err != nil {
		return fmt.Errorf("failed to watch deployment: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watcher closed")
			}

			if event.Type == watch.Modified {
				deployment, ok := event.Object.(*appsv1.Deployment)
				if !ok {
					continue
				}

				if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
					c.logger.Infof("Deployment %s is ready", name)
					return nil
				}
			}
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deployment %s to be ready", name)
		}
	}
}

// CreateEventWatcher crée un watcher d'événements
func (c *Client) CreateEventWatcher(namespace string) *EventWatcher {
	return &EventWatcher{
		client:    c,
		namespace: namespace,
		stopCh:    make(chan struct{}),
	}
}

// Start démarre le watcher d'événements
func (ew *EventWatcher) Start(eventHandler func(event *corev1.Event)) error {
	listWatch := cache.NewListWatchFromClient(
		ew.client.clientset.CoreV1().RESTClient(),
		"events",
		ew.namespace,
		fields.Everything(),
	)

	_, controller := cache.NewInformer(
		listWatch,
		&corev1.Event{},
		time.Minute,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if event, ok := obj.(*corev1.Event); ok {
					eventHandler(event)
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				if event, ok := newObj.(*corev1.Event); ok {
					eventHandler(event)
				}
			},
		},
	)

	go controller.Run(ew.stopCh)
	return nil
}

// Stop arrête le watcher d'événements
func (ew *EventWatcher) Stop() {
	close(ew.stopCh)
}

// buildEnvVars construit les variables d'environnement
func (c *Client) buildEnvVars(env map[string]string) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for key, value := range env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}
	return envVars
}

// buildProbe construit une probe
func (c *Client) buildProbe(config ProbeConfig) *corev1.Probe {
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

// buildServicePorts construit les ports de service
func (c *Client) buildServicePorts(ports []ServicePort) []corev1.ServicePort {
	var servicePorts []corev1.ServicePort
	for _, port := range ports {
		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       port.Name,
			Port:       port.Port,
			TargetPort: intstr.FromInt(int(port.TargetPort)),
			Protocol:   port.Protocol,
		})
	}
	return servicePorts
}

// buildIngressPaths construit les chemins d'ingress
func (c *Client) buildIngressPaths(paths []IngressPath) []networkingv1.HTTPIngressPath {
	var ingressPaths []networkingv1.HTTPIngressPath
	for _, path := range paths {
		ingressPaths = append(ingressPaths, networkingv1.HTTPIngressPath{
			Path:     path.Path,
			PathType: &path.PathType,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: path.ServiceName,
					Port: networkingv1.ServiceBackendPort{
						Number: path.ServicePort,
					},
				},
			},
		})
	}
	return ingressPaths
}

// buildIngressTLS construit la configuration TLS
func (c *Client) buildIngressTLS(tlsConfigs []IngressTLS) []networkingv1.IngressTLS {
	var ingressTLS []networkingv1.IngressTLS
	for _, tls := range tlsConfigs {
		ingressTLS = append(ingressTLS, networkingv1.IngressTLS{
			Hosts:      tls.Hosts,
			SecretName: tls.SecretName,
		})
	}
	return ingressTLS
}

// Close ferme les connexions du client
func (c *Client) Close() error {
	c.logger.Info("Closing Kubernetes client")
	return nil
}

// GetClusterInfo récupère les informations du cluster
func (c *Client) GetClusterInfo(ctx context.Context) (map[string]interface{}, error) {
	version, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get server version: %w", err)
	}

	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	return map[string]interface{}{
		"version":   version,
		"nodeCount": len(nodes.Items),
		"nodes":     nodes.Items,
	}, nil
}

// CreateSecret crée un secret
func (c *Client) CreateSecret(ctx context.Context, name, namespace string, data map[string][]byte, secretType corev1.SecretType) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: secretType,
		Data: data,
	}

	result, err := c.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secret %s: %w", name, err)
	}

	c.logger.Infof("Secret %s created successfully", name)
	return result, nil
}

// CreateConfigMap crée une config map
func (c *Client) CreateConfigMap(ctx context.Context, name, namespace string, data map[string]string) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}

	result, err := c.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create config map %s: %w", name, err)
	}

	c.logger.Infof("ConfigMap %s created successfully", name)
	return result, nil
}
