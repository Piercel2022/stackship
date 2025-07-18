package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/stackship/backend/config"
	"github.com/stackship/backend/utils"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

// ScalerService gère la mise à l'échelle des applications Kubernetes
type ScalerService struct {
	clientset     *kubernetes.Clientset
	metricsClient *versioned.Clientset
	config        *config.Config
	logger        *utils.Logger
}

// ScalingPolicy définit les politiques de mise à l'échelle
type ScalingPolicy struct {
	MinReplicas     int32                `json:"minReplicas"`
	MaxReplicas     int32                `json:"maxReplicas"`
	TargetCPU       int32                `json:"targetCPU"`
	TargetMemory    int32                `json:"targetMemory"`
	ScaleUpPeriod   time.Duration        `json:"scaleUpPeriod"`
	ScaleDownPeriod time.Duration        `json:"scaleDownPeriod"`
	CustomMetrics   []CustomMetricTarget `json:"customMetrics,omitempty"`
	Behaviors       *ScalingBehavior     `json:"behaviors,omitempty"`
}

// CustomMetricTarget définit les métriques personnalisées pour l'autoscaling
type CustomMetricTarget struct {
	Name         string `json:"name"`
	TargetValue  string `json:"targetValue"`
	MetricType   string `json:"metricType"` // "Pod", "Object", "External"
	ResourceName string `json:"resourceName,omitempty"`
}

// ScalingBehavior définit les comportements de mise à l'échelle
type ScalingBehavior struct {
	ScaleUp   *ScaleDirection `json:"scaleUp,omitempty"`
	ScaleDown *ScaleDirection `json:"scaleDown,omitempty"`
}

// ScaleDirection définit les paramètres de direction de mise à l'échelle
type ScaleDirection struct {
	StabilizationWindowSeconds int32         `json:"stabilizationWindowSeconds"`
	Policies                   []ScalePolicy `json:"policies"`
}

// ScalePolicy définit une politique de mise à l'échelle
type ScalePolicy struct {
	Type          string `json:"type"` // "Pods", "Percent"
	Value         int32  `json:"value"`
	PeriodSeconds int32  `json:"periodSeconds"`
}

// ScalingEvent représente un événement de mise à l'échelle
type ScalingEvent struct {
	DeploymentName string             `json:"deploymentName"`
	Namespace      string             `json:"namespace"`
	ProjectID      string             `json:"projectId"`
	FromReplicas   int32              `json:"fromReplicas"`
	ToReplicas     int32              `json:"toReplicas"`
	Reason         string             `json:"reason"`
	Timestamp      time.Time          `json:"timestamp"`
	MetricValues   map[string]float64 `json:"metricValues"`
}

// ScalingStatus représente le statut de mise à l'échelle
type ScalingStatus struct {
	DeploymentName  string             `json:"deploymentName"`
	Namespace       string             `json:"namespace"`
	CurrentReplicas int32              `json:"currentReplicas"`
	DesiredReplicas int32              `json:"desiredReplicas"`
	MinReplicas     int32              `json:"minReplicas"`
	MaxReplicas     int32              `json:"maxReplicas"`
	LastScaleTime   *time.Time         `json:"lastScaleTime,omitempty"`
	Conditions      []ScalingCondition `json:"conditions"`
	CurrentMetrics  []CurrentMetric    `json:"currentMetrics"`
	IsAutoscaled    bool               `json:"isAutoscaled"`
}

// ScalingCondition représente une condition de mise à l'échelle
type ScalingCondition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
}

// CurrentMetric représente une métrique actuelle
type CurrentMetric struct {
	Name         string  `json:"name"`
	CurrentValue float64 `json:"currentValue"`
	TargetValue  float64 `json:"targetValue"`
	Unit         string  `json:"unit"`
}

// NewScalerService crée une nouvelle instance du service de mise à l'échelle
func NewScalerService(clientset *kubernetes.Clientset, metricsClient *versioned.Clientset, config *config.Config, logger *utils.Logger) *ScalerService {
	return &ScalerService{
		clientset:     clientset,
		metricsClient: metricsClient,
		config:        config,
		logger:        logger,
	}
}

// ScaleDeployment effectue une mise à l'échelle manuelle d'un déploiement
func (s *ScalerService) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32, projectID string) error {
	s.logger.Info(fmt.Sprintf("Scaling deployment %s/%s to %d replicas", namespace, name, replicas))

	// Récupérer le déploiement actuel
	deployment, err := s.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	currentReplicas := *deployment.Spec.Replicas

	// Mettre à jour le nombre de replicas
	deployment.Spec.Replicas = &replicas
	_, err = s.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale deployment: %w", err)
	}

	// Enregistrer l'événement de mise à l'échelle
	event := ScalingEvent{
		DeploymentName: name,
		Namespace:      namespace,
		ProjectID:      projectID,
		FromReplicas:   currentReplicas,
		ToReplicas:     replicas,
		Reason:         "ManualScale",
		Timestamp:      time.Now(),
	}

	s.logScalingEvent(event)
	return nil
}

// EnableAutoScaling active l'autoscaling pour un déploiement
func (s *ScalerService) EnableAutoScaling(ctx context.Context, namespace, name string, policy ScalingPolicy, projectID string) error {
	s.logger.Info(fmt.Sprintf("Enabling autoscaling for deployment %s/%s", namespace, name))

	// Créer ou mettre à jour l'HPA
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-hpa",
			Namespace: namespace,
			Labels: map[string]string{
				"app":        name,
				"project-id": projectID,
				"managed-by": "stackship",
			},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       name,
			},
			MinReplicas: &policy.MinReplicas,
			MaxReplicas: policy.MaxReplicas,
			Metrics:     s.buildMetrics(policy),
		},
	}

	// Ajouter les comportements de mise à l'échelle si spécifiés
	if policy.Behaviors != nil {
		hpa.Spec.Behavior = s.buildScalingBehavior(policy.Behaviors)
	}

	// Créer ou mettre à jour l'HPA
	_, err := s.clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpa.Name, metav1.GetOptions{})
	if err != nil {
		// L'HPA n'existe pas, le créer
		_, err = s.clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Create(ctx, hpa, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create HPA: %w", err)
		}
	} else {
		// L'HPA existe, le mettre à jour
		_, err = s.clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Update(ctx, hpa, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update HPA: %w", err)
		}
	}

	s.logger.Info(fmt.Sprintf("Autoscaling enabled for deployment %s/%s", namespace, name))
	return nil
}

// DisableAutoScaling désactive l'autoscaling pour un déploiement
func (s *ScalerService) DisableAutoScaling(ctx context.Context, namespace, name string) error {
	s.logger.Info(fmt.Sprintf("Disabling autoscaling for deployment %s/%s", namespace, name))

	hpaName := name + "-hpa"
	err := s.clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Delete(ctx, hpaName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete HPA: %w", err)
	}

	s.logger.Info(fmt.Sprintf("Autoscaling disabled for deployment %s/%s", namespace, name))
	return nil
}

// GetScalingStatus récupère le statut de mise à l'échelle d'un déploiement
func (s *ScalerService) GetScalingStatus(ctx context.Context, namespace, name string) (*ScalingStatus, error) {
	// Récupérer le déploiement
	deployment, err := s.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	status := &ScalingStatus{
		DeploymentName:  name,
		Namespace:       namespace,
		CurrentReplicas: deployment.Status.Replicas,
		DesiredReplicas: *deployment.Spec.Replicas,
		MinReplicas:     1,
		MaxReplicas:     *deployment.Spec.Replicas,
		IsAutoscaled:    false,
	}

	// Vérifier si l'autoscaling est activé
	hpaName := name + "-hpa"
	hpa, err := s.clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, hpaName, metav1.GetOptions{})
	if err == nil {
		status.IsAutoscaled = true
		status.MinReplicas = *hpa.Spec.MinReplicas
		status.MaxReplicas = hpa.Spec.MaxReplicas
		status.DesiredReplicas = hpa.Status.DesiredReplicas

		// Ajouter les conditions
		for _, condition := range hpa.Status.Conditions {
			status.Conditions = append(status.Conditions, ScalingCondition{
				Type:               string(condition.Type),
				Status:             string(condition.Status),
				LastTransitionTime: condition.LastTransitionTime.Time,
				Reason:             condition.Reason,
				Message:            condition.Message,
			})
		}

		// Ajouter les métriques actuelles
		for _, metric := range hpa.Status.CurrentMetrics {
			currentMetric := CurrentMetric{
				Name: s.getMetricName(metric),
			}

			if metric.Resource != nil {
				currentMetric.CurrentValue = float64(metric.Resource.Current.AverageUtilization)
				currentMetric.Unit = "%"
			}

			status.CurrentMetrics = append(status.CurrentMetrics, currentMetric)
		}

		// Récupérer l'heure du dernier redimensionnement
		if hpa.Status.LastScaleTime != nil {
			status.LastScaleTime = &hpa.Status.LastScaleTime.Time
		}
	}

	return status, nil
}

// GetScalingHistory récupère l'historique de mise à l'échelle
func (s *ScalerService) GetScalingHistory(ctx context.Context, namespace, name string, limit int) ([]ScalingEvent, error) {
	// Dans un environnement réel, cela pourrait être récupéré depuis une base de données
	// ou un système de métriques comme Prometheus
	events := make([]ScalingEvent, 0)

	// Récupérer les événements Kubernetes liés à la mise à l'échelle
	eventList, err := s.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", name),
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	for _, event := range eventList.Items {
		if event.Reason == "ScalingReplicaSet" || event.Reason == "SuccessfulRescale" {
			scalingEvent := ScalingEvent{
				DeploymentName: name,
				Namespace:      namespace,
				Reason:         event.Reason,
				Timestamp:      event.FirstTimestamp.Time,
			}
			events = append(events, scalingEvent)
		}
	}

	return events, nil
}

// PredictScaling prédit le besoin de mise à l'échelle basé sur les métriques
func (s *ScalerService) PredictScaling(ctx context.Context, namespace, name string) (*ScalingPrediction, error) {
	// Récupérer les métriques actuelles
	podMetrics, err := s.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	prediction := &ScalingPrediction{
		DeploymentName: name,
		Namespace:      namespace,
		Timestamp:      time.Now(),
	}

	// Analyser les métriques et prédire les besoins
	totalCPU := resource.Quantity{}
	totalMemory := resource.Quantity{}
	podCount := len(podMetrics.Items)

	for _, pod := range podMetrics.Items {
		for _, container := range pod.Containers {
			totalCPU.Add(container.Usage["cpu"])
			totalMemory.Add(container.Usage["memory"])
		}
	}

	if podCount > 0 {
		avgCPU := totalCPU.MilliValue() / int64(podCount)
		avgMemory := totalMemory.Value() / int64(podCount)

		prediction.CurrentAvgCPU = float64(avgCPU) / 1000                // Convert to cores
		prediction.CurrentAvgMemory = float64(avgMemory) / (1024 * 1024) // Convert to MB

		// Logique de prédiction simple
		if prediction.CurrentAvgCPU > 0.8 {
			prediction.RecommendedAction = "SCALE_UP"
			prediction.RecommendedReplicas = int32(podCount) + 1
			prediction.Confidence = 0.85
		} else if prediction.CurrentAvgCPU < 0.2 && podCount > 1 {
			prediction.RecommendedAction = "SCALE_DOWN"
			prediction.RecommendedReplicas = int32(podCount) - 1
			prediction.Confidence = 0.75
		} else {
			prediction.RecommendedAction = "NO_CHANGE"
			prediction.RecommendedReplicas = int32(podCount)
			prediction.Confidence = 0.95
		}
	}

	return prediction, nil
}

// ScalingPrediction représente une prédiction de mise à l'échelle
type ScalingPrediction struct {
	DeploymentName      string    `json:"deploymentName"`
	Namespace           string    `json:"namespace"`
	Timestamp           time.Time `json:"timestamp"`
	CurrentAvgCPU       float64   `json:"currentAvgCpu"`
	CurrentAvgMemory    float64   `json:"currentAvgMemory"`
	PredictedLoad       float64   `json:"predictedLoad"`
	RecommendedAction   string    `json:"recommendedAction"`
	RecommendedReplicas int32     `json:"recommendedReplicas"`
	Confidence          float64   `json:"confidence"`
	ReasoningFactors    []string  `json:"reasoningFactors"`
}

// buildMetrics construit les métriques pour l'HPA
func (s *ScalerService) buildMetrics(policy ScalingPolicy) []autoscalingv2.MetricSpec {
	var metrics []autoscalingv2.MetricSpec

	// Métrique CPU
	if policy.TargetCPU > 0 {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &policy.TargetCPU,
				},
			},
		})
	}

	// Métrique mémoire
	if policy.TargetMemory > 0 {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &policy.TargetMemory,
				},
			},
		})
	}

	// Métriques personnalisées
	for _, customMetric := range policy.CustomMetrics {
		targetValue := resource.MustParse(customMetric.TargetValue)

		switch customMetric.MetricType {
		case "Pod":
			metrics = append(metrics, autoscalingv2.MetricSpec{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{
						Name: customMetric.Name,
					},
					Target: autoscalingv2.MetricTarget{
						Type:         autoscalingv2.AverageValueMetricType,
						AverageValue: &targetValue,
					},
				},
			})
		case "External":
			metrics = append(metrics, autoscalingv2.MetricSpec{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricSource{
					Metric: autoscalingv2.MetricIdentifier{
						Name: customMetric.Name,
					},
					Target: autoscalingv2.MetricTarget{
						Type:  autoscalingv2.ValueMetricType,
						Value: &targetValue,
					},
				},
			})
		}
	}

	return metrics
}

// buildScalingBehavior construit le comportement de mise à l'échelle
func (s *ScalerService) buildScalingBehavior(behaviors *ScalingBehavior) *autoscalingv2.HorizontalPodAutoscalerBehavior {
	behavior := &autoscalingv2.HorizontalPodAutoscalerBehavior{}

	if behaviors.ScaleUp != nil {
		behavior.ScaleUp = &autoscalingv2.HPAScalingRules{
			StabilizationWindowSeconds: &behaviors.ScaleUp.StabilizationWindowSeconds,
			Policies:                   s.buildScalePolicies(behaviors.ScaleUp.Policies),
		}
	}

	if behaviors.ScaleDown != nil {
		behavior.ScaleDown = &autoscalingv2.HPAScalingRules{
			StabilizationWindowSeconds: &behaviors.ScaleDown.StabilizationWindowSeconds,
			Policies:                   s.buildScalePolicies(behaviors.ScaleDown.Policies),
		}
	}

	return behavior
}

// buildScalePolicies construit les politiques de mise à l'échelle
func (s *ScalerService) buildScalePolicies(policies []ScalePolicy) []autoscalingv2.HPAScalingPolicy {
	var scalePolicies []autoscalingv2.HPAScalingPolicy

	for _, policy := range policies {
		var policyType autoscalingv2.HPAScalingPolicyType
		switch policy.Type {
		case "Pods":
			policyType = autoscalingv2.PodsScalingPolicy
		case "Percent":
			policyType = autoscalingv2.PercentScalingPolicy
		}

		scalePolicies = append(scalePolicies, autoscalingv2.HPAScalingPolicy{
			Type:          policyType,
			Value:         policy.Value,
			PeriodSeconds: policy.PeriodSeconds,
		})
	}

	return scalePolicies
}

// getMetricName extrait le nom d'une métrique
func (s *ScalerService) getMetricName(metric autoscalingv2.MetricStatus) string {
	switch metric.Type {
	case autoscalingv2.ResourceMetricSourceType:
		return string(metric.Resource.Name)
	case autoscalingv2.PodsMetricSourceType:
		return metric.Pods.Metric.Name
	case autoscalingv2.ExternalMetricSourceType:
		return metric.External.Metric.Name
	default:
		return "unknown"
	}
}

// logScalingEvent enregistre un événement de mise à l'échelle
func (s *ScalerService) logScalingEvent(event ScalingEvent) {
	s.logger.Info(fmt.Sprintf("Scaling event: %s/%s scaled from %d to %d replicas (reason: %s)",
		event.Namespace, event.DeploymentName, event.FromReplicas, event.ToReplicas, event.Reason))
}

// ValidateScalingPolicy valide une politique de mise à l'échelle
func (s *ScalerService) ValidateScalingPolicy(policy ScalingPolicy) error {
	if policy.MinReplicas < 1 {
		return fmt.Errorf("minReplicas must be at least 1")
	}

	if policy.MaxReplicas < policy.MinReplicas {
		return fmt.Errorf("maxReplicas must be greater than or equal to minReplicas")
	}

	if policy.TargetCPU < 1 || policy.TargetCPU > 100 {
		return fmt.Errorf("targetCPU must be between 1 and 100")
	}

	if policy.TargetMemory < 1 || policy.TargetMemory > 100 {
		return fmt.Errorf("targetMemory must be between 1 and 100")
	}

	return nil
}

// GetRecommendedScalingPolicy recommande une politique de mise à l'échelle basée sur l'historique
func (s *ScalerService) GetRecommendedScalingPolicy(ctx context.Context, namespace, name string) (*ScalingPolicy, error) {
	// Analyser l'historique de performance et recommander une politique
	// Cette implémentation est simplifiée
	return &ScalingPolicy{
		MinReplicas:     1,
		MaxReplicas:     10,
		TargetCPU:       70,
		TargetMemory:    80,
		ScaleUpPeriod:   2 * time.Minute,
		ScaleDownPeriod: 5 * time.Minute,
	}, nil
}
