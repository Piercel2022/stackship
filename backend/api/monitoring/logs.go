package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// LogLevel représente le niveau de log
type LogLevel string

const (
	DEBUG LogLevel = "DEBUG"
	INFO  LogLevel = "INFO"
	WARN  LogLevel = "WARN"
	ERROR LogLevel = "ERROR"
	FATAL LogLevel = "FATAL"
)

// LogEntry représente une entrée de log
type LogEntry struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	Timestamp  time.Time `json:"timestamp" gorm:"index"`
	Level      LogLevel  `json:"level" gorm:"index"`
	Message    string    `json:"message"`
	Source     string    `json:"source" gorm:"index"` // api, deployment, websocket, etc.
	ProjectID  *uint     `json:"project_id,omitempty" gorm:"index"`
	UserID     *uint     `json:"user_id,omitempty" gorm:"index"`
	RequestID  string    `json:"request_id,omitempty" gorm:"index"`
	Component  string    `json:"component,omitempty" gorm:"index"`
	Action     string    `json:"action,omitempty"`
	Duration   *int64    `json:"duration,omitempty"` // en millisecondes
	StatusCode *int      `json:"status_code,omitempty"`
	ClientIP   string    `json:"client_ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	Error      string    `json:"error,omitempty"`
	Metadata   string    `json:"metadata,omitempty"` // JSON pour données additionnelles
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// LogsService gère la collecte et la récupération des logs
type LogsService struct {
	db     *gorm.DB
	logger *zap.Logger
	hub    *websocket.Hub // Pour diffusion en temps réel
}

// NewLogsService crée une nouvelle instance du service de logs
func NewLogsService(db *gorm.DB, logger *zap.Logger, hub *websocket.Hub) *LogsService {
	return &LogsService{
		db:     db,
		logger: logger,
		hub:    hub,
	}
}

// LogsHandler contient les handlers pour les logs
type LogsHandler struct {
	service *LogsService
	logger  *zap.Logger
}

// NewLogsHandler crée un nouveau handler pour les logs
func NewLogsHandler(service *LogsService, logger *zap.Logger) *LogsHandler {
	return &LogsHandler{
		service: service,
		logger:  logger,
	}
}

// LogFilter représente les filtres pour la recherche de logs
type LogFilter struct {
	Level     []LogLevel `json:"level,omitempty"`
	Source    []string   `json:"source,omitempty"`
	ProjectID *uint      `json:"project_id,omitempty"`
	UserID    *uint      `json:"user_id,omitempty"`
	Component []string   `json:"component,omitempty"`
	StartTime *time.Time `json:"start_time,omitempty"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	Search    string     `json:"search,omitempty"`
	Limit     int        `json:"limit,omitempty"`
	Offset    int        `json:"offset,omitempty"`
	SortBy    string     `json:"sort_by,omitempty"`    // timestamp, level, source
	SortOrder string     `json:"sort_order,omitempty"` // asc, desc
}

// LogResponse représente la réponse pour les logs
type LogResponse struct {
	Logs       []LogEntry `json:"logs"`
	Total      int64      `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	TotalPages int        `json:"total_pages"`
}

// LogStats représente les statistiques des logs
type LogStats struct {
	TotalLogs    int64            `json:"total_logs"`
	LevelCounts  map[LogLevel]int `json:"level_counts"`
	SourceCounts map[string]int   `json:"source_counts"`
	RecentErrors []LogEntry       `json:"recent_errors"`
	Trends       []LogTrend       `json:"trends"`
}

// LogTrend représente la tendance des logs par période
type LogTrend struct {
	Period string           `json:"period"`
	Counts map[LogLevel]int `json:"counts"`
	Date   time.Time        `json:"date"`
}

// CreateLog crée une nouvelle entrée de log
func (s *LogsService) CreateLog(ctx context.Context, entry LogEntry) error {
	entry.CreatedAt = time.Now()
	entry.UpdatedAt = time.Now()

	if err := s.db.Create(&entry).Error; err != nil {
		s.logger.Error("Erreur lors de la création du log", zap.Error(err))
		return err
	}

	// Diffusion en temps réel via WebSocket
	s.broadcastLog(entry)

	return nil
}

// GetLogs récupère les logs avec filtres et pagination
func (s *LogsService) GetLogs(ctx context.Context, filter LogFilter) (*LogResponse, error) {
	var logs []LogEntry
	var total int64

	query := s.db.Model(&LogEntry{})

	// Application des filtres
	if len(filter.Level) > 0 {
		query = query.Where("level IN ?", filter.Level)
	}
	if len(filter.Source) > 0 {
		query = query.Where("source IN ?", filter.Source)
	}
	if filter.ProjectID != nil {
		query = query.Where("project_id = ?", *filter.ProjectID)
	}
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if len(filter.Component) > 0 {
		query = query.Where("component IN ?", filter.Component)
	}
	if filter.StartTime != nil {
		query = query.Where("timestamp >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("timestamp <= ?", *filter.EndTime)
	}
	if filter.Search != "" {
		query = query.Where("message ILIKE ? OR error ILIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}

	// Comptage total
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	// Tri
	sortBy := "timestamp"
	if filter.SortBy != "" {
		sortBy = filter.SortBy
	}
	sortOrder := "DESC"
	if filter.SortOrder != "" {
		sortOrder = filter.SortOrder
	}
	query = query.Order(fmt.Sprintf("%s %s", sortBy, sortOrder))

	// Pagination
	limit := 100
	if filter.Limit > 0 {
		limit = filter.Limit
	}
	offset := 0
	if filter.Offset > 0 {
		offset = filter.Offset
	}
	query = query.Limit(limit).Offset(offset)

	if err := query.Find(&logs).Error; err != nil {
		return nil, err
	}

	page := (offset / limit) + 1
	totalPages := int((total + int64(limit) - 1) / int64(limit))

	return &LogResponse{
		Logs:       logs,
		Total:      total,
		Page:       page,
		PageSize:   limit,
		TotalPages: totalPages,
	}, nil
}

// GetLogStats récupère les statistiques des logs
func (s *LogsService) GetLogStats(ctx context.Context, projectID *uint, duration time.Duration) (*LogStats, error) {
	startTime := time.Now().Add(-duration)

	var stats LogStats
	stats.LevelCounts = make(map[LogLevel]int)
	stats.SourceCounts = make(map[string]int)

	query := s.db.Model(&LogEntry{}).Where("timestamp >= ?", startTime)
	if projectID != nil {
		query = query.Where("project_id = ?", *projectID)
	}

	// Comptage total
	if err := query.Count(&stats.TotalLogs).Error; err != nil {
		return nil, err
	}

	// Comptage par niveau
	var levelCounts []struct {
		Level LogLevel
		Count int
	}
	if err := query.Select("level, COUNT(*) as count").Group("level").Find(&levelCounts).Error; err != nil {
		return nil, err
	}
	for _, lc := range levelCounts {
		stats.LevelCounts[lc.Level] = lc.Count
	}

	// Comptage par source
	var sourceCounts []struct {
		Source string
		Count  int
	}
	if err := query.Select("source, COUNT(*) as count").Group("source").Find(&sourceCounts).Error; err != nil {
		return nil, err
	}
	for _, sc := range sourceCounts {
		stats.SourceCounts[sc.Source] = sc.Count
	}

	// Erreurs récentes
	if err := query.Where("level IN ?", []LogLevel{ERROR, FATAL}).
		Order("timestamp DESC").
		Limit(10).
		Find(&stats.RecentErrors).Error; err != nil {
		return nil, err
	}

	// Tendances (par heure pour les dernières 24h)
	stats.Trends = s.getLogTrends(ctx, projectID, startTime)

	return &stats, nil
}

// getLogTrends calcule les tendances des logs
func (s *LogsService) getLogTrends(ctx context.Context, projectID *uint, startTime time.Time) []LogTrend {
	var trends []LogTrend

	// Grouper par heure
	for i := 0; i < 24; i++ {
		hourStart := startTime.Add(time.Duration(i) * time.Hour)
		hourEnd := hourStart.Add(time.Hour)

		query := s.db.Model(&LogEntry{}).
			Where("timestamp >= ? AND timestamp < ?", hourStart, hourEnd)

		if projectID != nil {
			query = query.Where("project_id = ?", *projectID)
		}

		var levelCounts []struct {
			Level LogLevel
			Count int
		}

		if err := query.Select("level, COUNT(*) as count").Group("level").Find(&levelCounts).Error; err != nil {
			continue
		}

		counts := make(map[LogLevel]int)
		for _, lc := range levelCounts {
			counts[lc.Level] = lc.Count
		}

		trends = append(trends, LogTrend{
			Period: fmt.Sprintf("%02d:00", hourStart.Hour()),
			Counts: counts,
			Date:   hourStart,
		})
	}

	return trends
}

// broadcastLog diffuse le log via WebSocket
func (s *LogsService) broadcastLog(entry LogEntry) {
	if s.hub != nil {
		message := map[string]interface{}{
			"type": "log_entry",
			"data": entry,
		}

		data, err := json.Marshal(message)
		if err == nil {
			s.hub.Broadcast <- data
		}
	}
}

// CleanupOldLogs supprime les logs anciens
func (s *LogsService) CleanupOldLogs(ctx context.Context, retention time.Duration) error {
	cutoffTime := time.Now().Add(-retention)

	result := s.db.Where("timestamp < ?", cutoffTime).Delete(&LogEntry{})
	if result.Error != nil {
		return result.Error
	}

	s.logger.Info("Logs anciens supprimés", zap.Int64("count", result.RowsAffected))
	return nil
}

// ExportLogs exporte les logs au format JSON
func (s *LogsService) ExportLogs(ctx context.Context, filter LogFilter) ([]byte, error) {
	response, err := s.GetLogs(ctx, filter)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(response.Logs, "", "  ")
}

// --- Handlers HTTP ---

// GetLogs handler pour récupérer les logs
func (h *LogsHandler) GetLogs(c *gin.Context) {
	var filter LogFilter

	// Parsing des paramètres de requête
	if levels := c.QueryArray("level"); len(levels) > 0 {
		for _, level := range levels {
			filter.Level = append(filter.Level, LogLevel(level))
		}
	}

	if sources := c.QueryArray("source"); len(sources) > 0 {
		filter.Source = sources
	}

	if projectID := c.Query("project_id"); projectID != "" {
		if pid, err := strconv.ParseUint(projectID, 10, 32); err == nil {
			id := uint(pid)
			filter.ProjectID = &id
		}
	}

	if userID := c.Query("user_id"); userID != "" {
		if uid, err := strconv.ParseUint(userID, 10, 32); err == nil {
			id := uint(uid)
			filter.UserID = &id
		}
	}

	if components := c.QueryArray("component"); len(components) > 0 {
		filter.Component = components
	}

	if startTime := c.Query("start_time"); startTime != "" {
		if t, err := time.Parse(time.RFC3339, startTime); err == nil {
			filter.StartTime = &t
		}
	}

	if endTime := c.Query("end_time"); endTime != "" {
		if t, err := time.Parse(time.RFC3339, endTime); err == nil {
			filter.EndTime = &t
		}
	}

	filter.Search = c.Query("search")

	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			filter.Limit = l
		}
	}

	if offset := c.Query("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil {
			filter.Offset = o
		}
	}

	filter.SortBy = c.DefaultQuery("sort_by", "timestamp")
	filter.SortOrder = c.DefaultQuery("sort_order", "DESC")

	response, err := h.service.GetLogs(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("Erreur lors de la récupération des logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erreur serveur"})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetLogStats handler pour récupérer les statistiques des logs
func (h *LogsHandler) GetLogStats(c *gin.Context) {
	var projectID *uint
	if pid := c.Query("project_id"); pid != "" {
		if id, err := strconv.ParseUint(pid, 10, 32); err == nil {
			uid := uint(id)
			projectID = &uid
		}
	}

	duration := 24 * time.Hour
	if d := c.Query("duration"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			duration = parsed
		}
	}

	stats, err := h.service.GetLogStats(c.Request.Context(), projectID, duration)
	if err != nil {
		h.logger.Error("Erreur lors de la récupération des statistiques", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erreur serveur"})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// ExportLogs handler pour exporter les logs
func (h *LogsHandler) ExportLogs(c *gin.Context) {
	var filter LogFilter
	if err := c.ShouldBindJSON(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Données invalides"})
		return
	}

	data, err := h.service.ExportLogs(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("Erreur lors de l'export des logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erreur serveur"})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=logs.json")
	c.Data(http.StatusOK, "application/json", data)
}

// CreateLog handler pour créer un log (utilisé par d'autres services)
func (h *LogsHandler) CreateLog(c *gin.Context) {
	var entry LogEntry
	if err := c.ShouldBindJSON(&entry); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Données invalides"})
		return
	}

	if err := h.service.CreateLog(c.Request.Context(), entry); err != nil {
		h.logger.Error("Erreur lors de la création du log", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erreur serveur"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Log créé avec succès"})
}

// GetLogsByProject handler pour récupérer les logs d'un projet
func (h *LogsHandler) GetLogsByProject(c *gin.Context) {
	projectID, err := strconv.ParseUint(c.Param("project_id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de projet invalide"})
		return
	}

	filter := LogFilter{
		ProjectID: func() *uint { id := uint(projectID); return &id }(),
		Limit:     100,
		SortBy:    "timestamp",
		SortOrder: "DESC",
	}

	// Appliquer les autres filtres de la requête
	if level := c.Query("level"); level != "" {
		filter.Level = []LogLevel{LogLevel(level)}
	}

	if source := c.Query("source"); source != "" {
		filter.Source = []string{source}
	}

	response, err := h.service.GetLogs(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("Erreur lors de la récupération des logs du projet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erreur serveur"})
		return
	}

	c.JSON(http.StatusOK, response)
}

// SetupLogsRoutes configure les routes pour les logs
func SetupLogsRoutes(router *gin.RouterGroup, handler *LogsHandler, authMiddleware gin.HandlerFunc) {
	logs := router.Group("/logs")
	logs.Use(authMiddleware)

	logs.GET("", handler.GetLogs)
	logs.GET("/stats", handler.GetLogStats)
	logs.POST("", handler.CreateLog)
	logs.POST("/export", handler.ExportLogs)
	logs.GET("/projects/:project_id", handler.GetLogsByProject)
}

// LoggerMiddleware middleware pour logger les requêtes HTTP
func LoggerMiddleware(logsService *LogsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Traitement de la requête
		c.Next()

		// Calcul de la durée
		duration := time.Since(start)

		// Création du log
		entry := LogEntry{
			Timestamp:  start,
			Level:      INFO,
			Message:    fmt.Sprintf("%s %s", c.Request.Method, c.Request.URL.Path),
			Source:     "api",
			Component:  "http",
			Action:     c.Request.Method,
			Duration:   func() *int64 { d := duration.Milliseconds(); return &d }(),
			StatusCode: &c.Writer.Status(),
			ClientIP:   c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
		}

		// Ajout de l'ID utilisateur si disponible
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(uint); ok {
				entry.UserID = &id
			}
		}

		// Ajout de l'ID de projet si disponible dans l'URL
		if projectID := c.Param("project_id"); projectID != "" {
			if id, err := strconv.ParseUint(projectID, 10, 32); err == nil {
				pid := uint(id)
				entry.ProjectID = &pid
			}
		}

		// Ajout de l'ID de requête si disponible
		if requestID := c.GetHeader("X-Request-ID"); requestID != "" {
			entry.RequestID = requestID
		}

		// Ajout de l'erreur si le statut est >= 400
		if c.Writer.Status() >= 400 {
			entry.Level = ERROR
			if len(c.Errors) > 0 {
				entry.Error = c.Errors.String()
			}
		}

		// Sauvegarde du log
		go logsService.CreateLog(context.Background(), entry)
	}
}
