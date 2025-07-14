package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"stackship/backend/database"
	"stackship/backend/services/docker"
	"stackship/backend/services/kubernetes"
	"stackship/backend/utils"
)

// MetricsCollector gère la collecte des métriques
type MetricsCollector struct {
	db        *gorm.DB
	dockerSvc *docker.Client
	k8sSvc    *kubernetes.Client
	logger    *logrus.Logger
	registry  *prometheus.Registry

	// Métriques HTTP
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpResponseSize    *prometheus.HistogramVec

	// Métriques d'application
	projectsTotal      prometheus.Gauge
	deploymentsTotal   prometheus.Gauge
	deploymentDuration *prometheus.HistogramVec
	deploymentStatus   *prometheus.GaugeVec

	// Métriques d'infrastructure
	dockerContainersTotal *prometheus.GaugeVec
	k8sPodsTotal          *prometheus.GaugeVec
	k8sNodesTotal         prometheus.Gauge

	// Métriques de base de données
	dbConnections   *prometheus.GaugeVec
	dbQueryDuration *prometheus.HistogramVec
	dbQueryErrors   *prometheus.CounterVec

	// Métriques WebSocket
	wsConnections prometheus.Gauge
	wsMessages    *prometheus.CounterVec

	// Métriques système
	systemCPUUsage    prometheus.Gauge
	systemMemoryUsage prometheus.Gauge
	systemDiskUsage   prometheus.Gauge
}

// NewMetricsCollector crée une nouvelle instance du collecteur de métriques
func NewMetricsCollector(db *gorm.DB, dockerSvc *docker.Client, k8sSvc *kubernetes.Client) *MetricsCollector {
	logger := utils.GetLogger()
	registry := prometheus.NewRegistry()

	mc := &MetricsCollector{
		db:        db,
		dockerSvc: dockerSvc,
		k8sSvc:    k8sSvc,
		logger:    logger,
		registry:  registry,
	}

	mc.initializeMetrics()
	mc.registerMetrics()

	return mc
}

// initializeMetrics initialise toutes les métriques
func (mc *MetricsCollector) initializeMetrics() {
	// Métriques HTTP
	mc.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stackship_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status_code"},
	)

	mc.httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stackship_http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	mc.httpResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stackship_http_response_size_bytes",
			Help:    "Size of HTTP responses in bytes",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"method", "endpoint"},
	)

	// Métriques d'application
	mc.projectsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_projects_total",
			Help: "Total number of projects",
		},
	)

	mc.deploymentsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_deployments_total",
			Help: "Total number of deployments",
		},
	)

	mc.deploymentDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stackship_deployment_duration_seconds",
			Help:    "Duration of deployments in seconds",
			Buckets: []float64{10, 30, 60, 300, 600, 1800, 3600},
		},
		[]string{"project", "environment", "status"},
	)

	mc.deploymentStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stackship_deployment_status",
			Help: "Current deployment status (1=success, 0=failure)",
		},
		[]string{"project", "environment", "deployment_id"},
	)

	// Métriques d'infrastructure
	mc.dockerContainersTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stackship_docker_containers_total",
			Help: "Total number of Docker containers",
		},
		[]string{"status"},
	)

	mc.k8sPodsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stackship_k8s_pods_total",
			Help: "Total number of Kubernetes pods",
		},
		[]string{"namespace", "status"},
	)

	mc.k8sNodesTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_k8s_nodes_total",
			Help: "Total number of Kubernetes nodes",
		},
	)

	// Métriques de base de données
	mc.dbConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stackship_db_connections",
			Help: "Number of database connections",
		},
		[]string{"status"},
	)

	mc.dbQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stackship_db_query_duration_seconds",
			Help:    "Duration of database queries in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
		},
		[]string{"table", "operation"},
	)

	mc.dbQueryErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stackship_db_query_errors_total",
			Help: "Total number of database query errors",
		},
		[]string{"table", "operation", "error_type"},
	)

	// Métriques WebSocket
	mc.wsConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_websocket_connections",
			Help: "Current number of WebSocket connections",
		},
	)

	mc.wsMessages = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stackship_websocket_messages_total",
			Help: "Total number of WebSocket messages",
		},
		[]string{"type", "direction"},
	)

	// Métriques système
	mc.systemCPUUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_system_cpu_usage_percent",
			Help: "System CPU usage percentage",
		},
	)

	mc.systemMemoryUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_system_memory_usage_percent",
			Help: "System memory usage percentage",
		},
	)

	mc.systemDiskUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stackship_system_disk_usage_percent",
			Help: "System disk usage percentage",
		},
	)
}

// registerMetrics enregistre toutes les métriques dans le registre
func (mc *MetricsCollector) registerMetrics() {
	// Métriques HTTP
	mc.registry.MustRegister(mc.httpRequestsTotal)
	mc.registry.MustRegister(mc.httpRequestDuration)
	mc.registry.MustRegister(mc.httpResponseSize)

	// Métriques d'application
	mc.registry.MustRegister(mc.projectsTotal)
	mc.registry.MustRegister(mc.deploymentsTotal)
	mc.registry.MustRegister(mc.deploymentDuration)
	mc.registry.MustRegister(mc.deploymentStatus)

	// Métriques d'infrastructure
	mc.registry.MustRegister(mc.dockerContainersTotal)
	mc.registry.MustRegister(mc.k8sPodsTotal)
	mc.registry.MustRegister(mc.k8sNodesTotal)

	// Métriques de base de données
	mc.registry.MustRegister(mc.dbConnections)
	mc.registry.MustRegister(mc.dbQueryDuration)
	mc.registry.MustRegister(mc.dbQueryErrors)

	// Métriques WebSocket
	mc.registry.MustRegister(mc.wsConnections)
	mc.registry.MustRegister(mc.wsMessages)

	// Métriques système
	mc.registry.MustRegister(mc.systemCPUUsage)
	mc.registry.MustRegister(mc.systemMemoryUsage)
	mc.registry.MustRegister(mc.systemDiskUsage)
}

// HTTPMiddleware crée un middleware pour capturer les métriques HTTP
func (mc *MetricsCollector) HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Traiter la requête
		c.Next()

		// Calculer les métriques
		duration := time.Since(start)
		statusCode := strconv.Itoa(c.Writer.Status())
		responseSize := float64(c.Writer.Size())

		// Mettre à jour les métriques
		mc.httpRequestsTotal.WithLabelValues(
			c.Request.Method,
			c.FullPath(),
			statusCode,
		).Inc()

		mc.httpRequestDuration.WithLabelValues(
			c.Request.Method,
			c.FullPath(),
		).Observe(duration.Seconds())

		if responseSize > 0 {
			mc.httpResponseSize.WithLabelValues(
				c.Request.Method,
				c.FullPath(),
			).Observe(responseSize)
		}
	}
}

// RecordDeployment enregistre les métriques d'un déploiement
func (mc *MetricsCollector) RecordDeployment(project, environment, deploymentID string, duration time.Duration, success bool) {
	status := "success"
	statusValue := 1.0

	if !success {
		status = "failure"
		statusValue = 0.0
	}

	mc.deploymentDuration.WithLabelValues(
		project,
		environment,
		status,
	).Observe(duration.Seconds())

	mc.deploymentStatus.WithLabelValues(
		project,
		environment,
		deploymentID,
	).Set(statusValue)
}

// RecordWebSocketConnection enregistre les connexions WebSocket
func (mc *MetricsCollector) RecordWebSocketConnection(delta float64) {
	mc.wsConnections.Add(delta)
}

// RecordWebSocketMessage enregistre un message WebSocket
func (mc *MetricsCollector) RecordWebSocketMessage(messageType, direction string) {
	mc.wsMessages.WithLabelValues(messageType, direction).Inc()
}

// RecordDatabaseQuery enregistre une requête de base de données
func (mc *MetricsCollector) RecordDatabaseQuery(table, operation string, duration time.Duration, err error) {
	mc.dbQueryDuration.WithLabelValues(table, operation).Observe(duration.Seconds())

	if err != nil {
		errorType := "unknown"
		if err == gorm.ErrRecordNotFound {
			errorType = "not_found"
		} else if err == context.DeadlineExceeded {
			errorType = "timeout"
		}

		mc.dbQueryErrors.WithLabelValues(table, operation, errorType).Inc()
	}
}

// updateApplicationMetrics met à jour les métriques d'application
func (mc *MetricsCollector) updateApplicationMetrics() {
	// Compter les projets
	var projectCount int64
	if err := mc.db.Model(&database.Project{}).Count(&projectCount).Error; err != nil {
		mc.logger.WithError(err).Error("Failed to count projects")
	} else {
		mc.projectsTotal.Set(float64(projectCount))
	}

	// Compter les déploiements
	var deploymentCount int64
	if err := mc.db.Model(&database.Deployment{}).Count(&deploymentCount).Error; err != nil {
		mc.logger.WithError(err).Error("Failed to count deployments")
	} else {
		mc.deploymentsTotal.Set(float64(deploymentCount))
	}
}

// updateInfrastructureMetrics met à jour les métriques d'infrastructure
func (mc *MetricsCollector) updateInfrastructureMetrics() {
	// Métriques Docker
	if mc.dockerSvc != nil {
		containers, err := mc.dockerSvc.ListContainers()
		if err != nil {
			mc.logger.WithError(err).Error("Failed to list Docker containers")
		} else {
			running := 0
			stopped := 0

			for _, container := range containers {
				if container.State == "running" {
					running++
				} else {
					stopped++
				}
			}

			mc.dockerContainersTotal.WithLabelValues("running").Set(float64(running))
			mc.dockerContainersTotal.WithLabelValues("stopped").Set(float64(stopped))
		}
	}

	// Métriques Kubernetes
	if mc.k8sSvc != nil {
		pods, err := mc.k8sSvc.ListPods("")
		if err != nil {
			mc.logger.WithError(err).Error("Failed to list Kubernetes pods")
		} else {
			podsByNamespace := make(map[string]map[string]int)

			for _, pod := range pods {
				if podsByNamespace[pod.Namespace] == nil {
					podsByNamespace[pod.Namespace] = make(map[string]int)
				}
				podsByNamespace[pod.Namespace][pod.Status.Phase]++
			}

			for namespace, statuses := range podsByNamespace {
				for status, count := range statuses {
					mc.k8sPodsTotal.WithLabelValues(namespace, status).Set(float64(count))
				}
			}
		}

		nodes, err := mc.k8sSvc.ListNodes()
		if err != nil {
			mc.logger.WithError(err).Error("Failed to list Kubernetes nodes")
		} else {
			mc.k8sNodesTotal.Set(float64(len(nodes)))
		}
	}
}

// updateDatabaseMetrics met à jour les métriques de base de données
func (mc *MetricsCollector) updateDatabaseMetrics() {
	sqlDB, err := mc.db.DB()
	if err != nil {
		mc.logger.WithError(err).Error("Failed to get database connection")
		return
	}

	stats := sqlDB.Stats()
	mc.dbConnections.WithLabelValues("open").Set(float64(stats.OpenConnections))
	mc.dbConnections.WithLabelValues("idle").Set(float64(stats.Idle))
	mc.dbConnections.WithLabelValues("in_use").Set(float64(stats.InUse))
}

// updateSystemMetrics met à jour les métriques système
func (mc *MetricsCollector) updateSystemMetrics() {
	// Ces métriques nécessiteraient une bibliothèque comme gopsutil
	// pour obtenir les informations système réelles
	// Pour l'instant, nous utilisons des valeurs fictives
	mc.systemCPUUsage.Set(50.0)
	mc.systemMemoryUsage.Set(60.0)
	mc.systemDiskUsage.Set(70.0)
}

// StartMetricsCollection démarre la collecte périodique des métriques
func (mc *MetricsCollector) StartMetricsCollection(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				mc.updateApplicationMetrics()
				mc.updateInfrastructureMetrics()
				mc.updateDatabaseMetrics()
				mc.updateSystemMetrics()
			}
		}
	}()
}

// GetMetricsHandler retourne un handler pour exposer les métriques
func (mc *MetricsCollector) GetMetricsHandler() http.Handler {
	return promhttp.HandlerFor(mc.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// GetMetrics retourne les métriques actuelles sous forme de map
func (mc *MetricsCollector) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"projects_total":        mc.getGaugeValue(mc.projectsTotal),
		"deployments_total":     mc.getGaugeValue(mc.deploymentsTotal),
		"websocket_connections": mc.getGaugeValue(mc.wsConnections),
		"system_cpu_usage":      mc.getGaugeValue(mc.systemCPUUsage),
		"system_memory_usage":   mc.getGaugeValue(mc.systemMemoryUsage),
		"system_disk_usage":     mc.getGaugeValue(mc.systemDiskUsage),
	}
}

// getGaugeValue aide à obtenir la valeur d'une gauge
func (mc *MetricsCollector) getGaugeValue(gauge prometheus.Gauge) float64 {
	metric := &prometheus.Metric{}
	if err := gauge.Write(metric); err != nil {
		return 0
	}
	return metric.GetGauge().GetValue()
}

// RegisterCustomMetric permet d'enregistrer des métriques personnalisées
func (mc *MetricsCollector) RegisterCustomMetric(name string, help string, metricType string, labels []string) error {
	switch metricType {
	case "counter":
		counter := prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: fmt.Sprintf("stackship_custom_%s", name),
				Help: help,
			},
			labels,
		)
		mc.registry.MustRegister(counter)
	case "gauge":
		gauge := prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("stackship_custom_%s", name),
				Help: help,
			},
			labels,
		)
		mc.registry.MustRegister(gauge)
	case "histogram":
		histogram := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: fmt.Sprintf("stackship_custom_%s", name),
				Help: help,
			},
			labels,
		)
		mc.registry.MustRegister(histogram)
	default:
		return fmt.Errorf("unsupported metric type: %s", metricType)
	}

	return nil
}

// Cleanup nettoie les ressources
func (mc *MetricsCollector) Cleanup() {
	mc.logger.Info("Cleaning up metrics collector")
	// Ici, on pourrait arrêter les goroutines, fermer les connexions, etc.
}
