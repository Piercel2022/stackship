package monitoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HealthStatus représente l'état de santé d'un composant
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusUnhealthy HealthStatus = "unhealthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnknown   HealthStatus = "unknown"
)

// ComponentHealth représente l'état de santé d'un composant
type ComponentHealth struct {
	Name         string                 `json:"name"`
	Status       HealthStatus           `json:"status"`
	Message      string                 `json:"message"`
	ResponseTime time.Duration          `json:"response_time"`
	Timestamp    time.Time              `json:"timestamp"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// HealthResponse représente la réponse globale de santé
type HealthResponse struct {
	Status     HealthStatus                `json:"status"`
	Version    string                      `json:"version"`
	Timestamp  time.Time                   `json:"timestamp"`
	Uptime     time.Duration               `json:"uptime"`
	Components map[string]ComponentHealth  `json:"components"`
	System     SystemInfo                  `json:"system"`
	Checks     []HealthCheck               `json:"checks"`
}

// SystemInfo contient les informations système
type SystemInfo struct {
	GoVersion    string  `json:"go_version"`
	NumCPU       int     `json:"num_cpu"`
	NumGoroutine int     `json:"num_goroutine"`
	MemoryUsage  int64   `json:"memory_usage"`
	MemoryLimit  int64   `json:"memory_limit"`
	CPUUsage     float64 `json:"cpu_usage"`
	DiskUsage    int64   `json:"disk_usage"`
	DiskTotal    int64   `json:"disk_total"`
}

// HealthCheck représente un contrôle de santé
type HealthCheck struct {
	Name      string                 `json:"name"`
	Status    HealthStatus           `json:"status"`
	Message   string                 `json:"message"`
	Duration  time.Duration          `json:"duration"`
	Timestamp time.Time              `json:"timestamp"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// HealthChecker interface pour les vérifications de santé
type HealthChecker interface {
	Check(ctx context.Context) HealthCheck
	Name() string
}

// HealthService gère les vérifications de santé
type HealthService struct {
	db        *gorm.DB
	redis     *redis.Client
	logger    *zap.Logger
	checkers  map[string]HealthChecker
	startTime time.Time
	version   string
	mu        sync.RWMutex
}

// NewHealthService crée une nouvelle instance du service de santé
func NewHealthService(db *gorm.DB, redis *redis.Client, logger *zap.Logger, version string) *HealthService {
	service := &HealthService{
		db:        db,
		redis:     redis,
		logger:    logger,
		checkers:  make(map[string]HealthChecker),
		startTime: time.Now(),
		version:   version,
	}

	// Ajout des vérifications par défaut
	service.RegisterChecker(&DatabaseChecker{db: db})
	service.RegisterChecker(&RedisChecker{redis: redis})
	service.RegisterChecker(&MemoryChecker{})
	service.RegisterChecker(&DiskChecker{})

	return service
}

// RegisterChecker enregistre une nouvelle vérification
func (s *HealthService) RegisterChecker(checker HealthChecker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers[checker.Name()] = checker
}

// CheckHealth effectue toutes les vérifications de santé
func (s *HealthService) CheckHealth(ctx context.Context) HealthResponse {
	start := time.Now()

	response := HealthResponse{
		Version:    s.version,
		Timestamp:  start,
		Uptime:     time.Since(s.startTime),
		Components: make(map[string]ComponentHealth),
		System:     s.getSystemInfo(),
		Checks:     make([]HealthCheck, 0),
	}

	// Exécution des vérifications en parallèle
	var wg sync.WaitGroup
	checkResults := make(chan HealthCheck, len(s.checkers))

	s.mu.RLock()
	for _, checker := range s.checkers {
		wg.Add(1)
		go func(checker HealthChecker) {
			defer wg.Done()
			checkResults <- checker.Check(ctx)
		}(checker)
	}
	s.mu.RUnlock()

	go func() {
		wg.Wait()
		close(checkResults)
	}()

	// Collecte des résultats
	overallStatus := StatusHealthy
	for check := range checkResults {
		response.Checks = append(response.Checks, check)

		// Mise à jour du statut global
		switch check.Status {
		case StatusUnhealthy:
			overallStatus = StatusUnhealthy
		case StatusDegraded:
			if overallStatus == StatusHealthy {
				overallStatus = StatusDegraded
			}
		}

		// Ajout aux composants
		response.Components[check.Name] = ComponentHealth{
			Name:         check.Name,
			Status:       check.Status,
			Message:      check.Message,
			ResponseTime: check.Duration,
			Timestamp:    check.Timestamp,
			Metadata:     check.Details,
		}
	}

	response.Status = overallStatus
	return response
}

// getSystemInfo collecte les informations système
func (s *HealthService) getSystemInfo() SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Obtenir les informations de disque
	diskUsage, diskTotal := getDiskUsage()

	return SystemInfo{
		GoVersion:    runtime.Version(),
		NumCPU:       runtime.NumCPU(),
		NumGoroutine: runtime.NumGoroutine(),
		MemoryUsage:  int64(memStats.Alloc),
		MemoryLimit:  int64(memStats.Sys),
		CPUUsage:     0.0, // À implémenter avec un monitoring plus avancé
		DiskUsage:    diskUsage,
		DiskTotal:    diskTotal,
	}
}

// getDiskUsage obtient l'utilisation du disque
func getDiskUsage() (int64, int64) {
	var stat syscall.Statfs_t
	wd, err := os.Getwd()
	if err != nil {
		return 0, 0
	}

	err = syscall.Statfs(wd, &stat)
	if err != nil {
		return 0, 0
	}

	total := int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bavail) * int64(stat.Bsize)
	used := total - free

	return used, total
}

// DatabaseChecker vérifie la santé de la base de données
type DatabaseChecker struct {
	db *gorm.DB
}

func (c *DatabaseChecker) Name() string {
	return "database"
}

func (c *DatabaseChecker) Check(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Details:   make(map[string]interface{}),
	}

	// Test de connexion
	sqlDB, err := c.db.DB()
	if err != nil {
		check.Status = StatusUnhealthy
		check.Message = fmt.Sprintf("Impossible d'obtenir la connexion DB: %v", err)
		check.Duration = time.Since(start)
		return check
	}

	// Test de ping
	if err := sqlDB.PingContext(ctx); err != nil {
		check.Status = StatusUnhealthy
		check.Message = fmt.Sprintf("Échec du ping DB: %v", err)
		check.Duration = time.Since(start)
		return check
	}

	// Obtenir les statistiques de la DB
	stats := sqlDB.Stats()
	check.Details["open_connections"] = stats.OpenConnections
	check.Details["in_use"] = stats.InUse
	check.Details["idle"] = stats.Idle
	check.Details["max_open_connections"] = stats.MaxOpenConnections

	// Test de requête simple
	var result int
	if err := sqlDB.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		check.Status = StatusDegraded
		check.Message = fmt.Sprintf("Erreur de requête test: %v", err)
	} else {
		check.Status = StatusHealthy
		check.Message = "Base de données fonctionnelle"
	}

	// Vérifier les performances
	check.Duration = time.Since(start)
	if check.Duration > 5*time.Second {
		check.Status = StatusDegraded
		check.Message = "Temps de réponse de la DB élevé"
	}

	return check
}

// RedisChecker vérifie la santé de Redis
type RedisChecker struct {
	redis *redis.Client
}

func (c *RedisChecker) Name() string {
	return "redis"
}

func (c *RedisChecker) Check(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Details:   make(map[string]interface{}),
	}

	// Test de ping
	pong, err := c.redis.Ping(ctx).Result()
	if err != nil {
		check.Status = StatusUnhealthy
		check.Message = fmt.Sprintf("Échec du ping Redis: %v", err)
		check.Duration = time.Since(start)
		return check
	}

	if pong != "PONG" {
		check.Status = StatusUnhealthy
		check.Message = "Réponse Redis invalide"
		check.Duration = time.Since(start)
		return check
	}

	// Obtenir les informations Redis
	info, err := c.redis.Info(ctx).Result()
	if err == nil {
		check.Details["info"] = info
	}

	// Test de set/get
	testKey := "health_check_test"
	testValue := "test_value"
	
	if err := c.redis.Set(ctx, testKey, testValue, time.Minute).Err(); err != nil {
		check.Status = StatusDegraded
		check.Message = fmt.Sprintf("Erreur SET Redis: %v", err)
	} else {
		val, err := c.redis.Get(ctx, testKey).Result()
		if err != nil || val != testValue {
			check.Status = StatusDegraded
			check.Message = "Erreur GET Redis"
		} else {
			c.redis.Del(ctx, testKey) // Nettoyer
			check.Status = StatusHealthy
			check.Message = "Redis fonctionnel"
		}
	}

	check.Duration = time.Since(start)
	return check
}

// MemoryChecker vérifie l'utilisation mémoire
type MemoryChecker struct{}

func (c *MemoryChecker) Name() string {
	return "memory"
}

func (c *MemoryChecker) Check(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Details:   make(map[string]interface{}),
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	check.Details["alloc"] = memStats.Alloc
	check.Details["sys"] = memStats.Sys
	check.Details["heap_alloc"] = memStats.HeapAlloc
	check.Details["heap_sys"] = memStats.HeapSys
	check.Details["gc_runs"] = memStats.NumGC

	// Calculer le pourcentage d'utilisation
	usagePercent := float64(memStats.Alloc) / float64(memStats.Sys) * 100
	check.Details["usage_percent"] = usagePercent

	// Déterminer le statut basé sur l'utilisation
	if usagePercent > 90 {
		check.Status = StatusUnhealthy
		check.Message = fmt.Sprintf("Utilisation mémoire critique: %.1f%%", usagePercent)
	} else if usagePercent > 75 {
		check.Status = StatusDegraded
		check.Message = fmt.Sprintf("Utilisation mémoire élevée: %.1f%%", usagePercent)
	} else {
		check.Status = StatusHealthy
		check.Message = fmt.Sprintf("Utilisation mémoire normale: %.1f%%", usagePercent)
	}

	check.Duration = time.Since(start)
	return check
}

// DiskChecker vérifie l'utilisation du disque
type DiskChecker struct{}

func (c *DiskChecker) Name() string {
	return "disk"
}

func (c *DiskChecker) Check(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Details:   make(map[string]interface{}),
	}

	usage, total := getDiskUsage()
	
	if total == 0 {
		check.Status = StatusUnknown
		check.Message = "Impossible d'obtenir les informations de disque"
		check.Duration = time.Since(start)
		return check
	}

	usagePercent := float64(usage) / float64(total) * 100
	
	check.Details["usage_bytes"] = usage
	check.Details["total_bytes"] = total
	check.Details["usage_percent"] = usagePercent
	check.Details["free_bytes"] = total - usage

	// Déterminer le statut
	if usagePercent > 95 {
		check.Status = StatusUnhealthy
		check.Message = fmt.Sprintf("Espace disque critique: %.1f%%", usagePercent)
	} else if usagePercent > 85 {
		check.Status = StatusDegraded
		check.Message = fmt.Sprintf("Espace disque faible: %.1f%%", usagePercent)
	} else {
		check.Status = StatusHealthy
		check.Message = fmt.Sprintf("Espace disque normal: %.1f%%", usagePercent)
	}

	check.Duration = time.Since(start)
	return check
}

// HealthHandler gère les endpoints de santé
type HealthHandler struct {
	service *HealthService
	logger  *zap.Logger
}

// NewHealthHandler crée un nouveau handler de santé
func NewHealthHandler(service *HealthService, logger *zap.Logger) *HealthHandler {
	return &HealthHandler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes enregistre les routes de santé
func (h *HealthHandler) RegisterRoutes(router *gin.Engine) {
	health := router.Group("/health")
	{
		health.GET("/", h.GetHealth)
		health.GET("/live", h.GetLiveness)
		health.GET("/ready", h.GetReadiness)
		health.GET("/metrics", h.GetMetrics)
	}
}

// GetHealth retourne l'état de santé complet
func (h *HealthHandler) GetHealth(c *gin.Context) {
	ctx := c.Request.Context()
	
	health := h.service.CheckHealth(ctx)
	
	// Définir le code de statut HTTP basé sur la santé
	statusCode := http.StatusOK
	switch health.Status {
	case StatusDegraded:
		statusCode = http.StatusOK // 200 mais avec avertissement
	case StatusUnhealthy:
		statusCode = http.StatusServiceUnavailable // 503
	case StatusUnknown:
		statusCode = http.StatusInternalServerError // 500
	}
	
	c.JSON(statusCode, health)
}

// GetLiveness endpoint pour Kubernetes liveness probe
func (h *HealthHandler) GetLiveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "alive",
		"timestamp": time.Now(),
	})
}

// GetReadiness endpoint pour Kubernetes readiness probe
func (h *HealthHandler) GetReadiness(c *gin.Context) {
	ctx := c.Request.Context()
	
	health := h.service.CheckHealth(ctx)
	
	// Prêt seulement si tous les composants critiques sont sains
	ready := true
	for _, component := range health.Components {
		if component.Name == "database" || component.Name == "redis" {
			if component.Status == StatusUnhealthy {
				ready = false
				break
			}
		}
	}
	
	statusCode := http.StatusOK
	status := "ready"
	
	if !ready {
		statusCode = http.StatusServiceUnavailable
		status = "not ready"
	}
	
	c.JSON(statusCode, gin.H{
		"status": status,
		"timestamp": time.Now(),
		"components": health.Components,
	})
}

// GetMetrics retourne les métriques système
func (h *HealthHandler) GetMetrics(c *gin.Context) {
	ctx := c.Request.Context()
	
	health := h.service.CheckHealth(ctx)
	
	c.JSON(http.StatusOK, gin.H{
		"system": health.System,
		"uptime": health.Uptime,
		"timestamp": health.Timestamp,
	})
}

// Middleware pour le monitoring des requêtes
func (h *HealthHandler) MonitoringMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		
		// Traiter la requête
		c.Next()
		
		// Logger les métriques
		duration := time.Since(start)
		h.logger.Info("Request processed",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", duration),
			zap.String("user_agent", c.Request.UserAgent()),
		)
	}
}
