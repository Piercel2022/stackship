package projects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"stackship/backend/services/docker"
	"stackship/backend/services/git"
	"stackship/backend/services/notification"
	"stackship/backend/utils"
)

// Service interface pour la gestion des projets
type Service interface {
	// CRUD Operations
	Create(ctx context.Context, req *ProjectCreateRequest, userID uuid.UUID) (*Project, error)
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*Project, error)
	Update(ctx context.Context, id uuid.UUID, req *ProjectUpdateRequest, userID uuid.UUID) (*Project, error)
	Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// List Operations
	List(ctx context.Context, filter *ProjectFilter, sort *ProjectSort, page, perPage int, userID uuid.UUID) (*ProjectListResponse, error)
	ListByTeam(ctx context.Context, teamID uuid.UUID, userID uuid.UUID) ([]Project, error)
	ListByOwner(ctx context.Context, ownerID uuid.UUID, userID uuid.UUID) ([]Project, error)

	// Repository Operations
	SyncRepository(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	SetupWebhook(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	RemoveWebhook(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// Environment Variables
	UpdateEnvVars(ctx context.Context, id uuid.UUID, envVars []EnvVarRequest, userID uuid.UUID) error
	GetEnvVars(ctx context.Context, id uuid.UUID, userID uuid.UUID) ([]EnvVar, error)

	// Build Operations
	ValidateBuildConfig(ctx context.Context, config *BuildConfig) error
	TestBuild(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// Statistics
	GetStats(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*ProjectStats, error)
	GetDashboardData(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)

	// Archive/Restore
	Archive(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	Restore(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// Search
	Search(ctx context.Context, query string, userID uuid.UUID) ([]Project, error)
}

// ProjectService implémentation du service
type ProjectService struct {
	db            *gorm.DB
	logger        *slog.Logger
	gitService    git.Service
	dockerService docker.Service
	notifService  notification.Service
	validator     *utils.Validator
}

// NewProjectService crée une nouvelle instance du service
func NewProjectService(
	db *gorm.DB,
	logger *slog.Logger,
	gitService git.Service,
	dockerService docker.Service,
	notifService notification.Service,
	validator *utils.Validator,
) Service {
	return &ProjectService{
		db:            db,
		logger:        logger,
		gitService:    gitService,
		dockerService: dockerService,
		notifService:  notifService,
		validator:     validator,
	}
}

// Create crée un nouveau projet
func (s *ProjectService) Create(ctx context.Context, req *ProjectCreateRequest, userID uuid.UUID) (*Project, error) {
	// Validation
	if err := s.validator.Struct(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Vérification que le nom est unique pour l'utilisateur
	var existingProject Project
	if err := s.db.Where("name = ? AND owner_id = ? AND deleted_at IS NULL", req.Name, userID).First(&existingProject).Error; err == nil {
		return nil, errors.New("project with this name already exists")
	}

	// Validation du repository
	if err := s.validateRepository(ctx, &req.Repository); err != nil {
		return nil, fmt.Errorf("repository validation failed: %w", err)
	}

	// Création du projet
	project := &Project{
		ID:          uuid.New(),
		Name:        req.Name,
		Description: req.Description,
		Repository:  req.Repository,
		Framework:   req.Framework,
		Language:    req.Language,
		BuildConfig: req.BuildConfig,
		Tags:        req.Tags,
		IsPrivate:   req.IsPrivate,
		IsActive:    true,
		Status:      ProjectStatusActive,
		OwnerID:     userID,
		TeamID:      req.TeamID,
	}

	// Transaction pour créer le projet et ses variables d'environnement
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Création du projet
		if err := tx.Create(project).Error; err != nil {
			return err
		}

		// Création des variables d'environnement
		if len(req.EnvVars) > 0 {
			envVars := make([]EnvVar, len(req.EnvVars))
			for i, envVar := range req.EnvVars {
				envVars[i] = EnvVar{
					ID:        uuid.New(),
					ProjectID: project.ID,
					Key:       envVar.Key,
					Value:     envVar.Value,
					IsSecret:  envVar.IsSecret,
				}
			}
			if err := tx.Create(&envVars).Error; err != nil {
				return err
			}
			project.EnvVars = envVars
		}

		return nil
	})

	if err != nil {
		s.logger.Error("Failed to create project", "error", err, "user_id", userID)
		return nil, err
	}

	// Configuration asynchrone du webhook
	go func() {
		if err := s.SetupWebhook(context.Background(), project.ID, userID); err != nil {
			s.logger.Error("Failed to setup webhook", "error", err, "project_id", project.ID)
		}
	}()

	// Notification
	s.notifService.SendNotification(ctx, &notification.Notification{
		Type:    notification.TypeProjectCreated,
		UserID:  userID,
		Title:   "Project Created",
		Message: fmt.Sprintf("Project '%s' has been created successfully", project.Name),
		Data:    map[string]interface{}{"project_id": project.ID.String()},
	})

	s.logger.Info("Project created successfully", "project_id", project.ID, "user_id", userID)
	return project, nil
}

// GetByID récupère un projet par son ID
func (s *ProjectService) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*Project, error) {
	var project Project

	query := s.db.WithContext(ctx).
		Preload("EnvVars").
		Preload("Deployments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC").Limit(10)
		})

	// Vérification des permissions
	query = query.Where("id = ? AND (owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?))",
		id, userID, userID)

	if err := query.First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("project not found")
		}
		return nil, err
	}

	return &project, nil
}

// Update met à jour un projet
func (s *ProjectService) Update(ctx context.Context, id uuid.UUID, req *ProjectUpdateRequest, userID uuid.UUID) (*Project, error) {
	// Validation
	if err := s.validator.Struct(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Récupération du projet existant
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return nil, errors.New("permission denied")
	}

	// Mise à jour des champs
	updates := make(map[string]interface{})

	if req.Name != nil {
		// Vérification d'unicité du nom
		var existingProject Project
		if err := s.db.Where("name = ? AND owner_id = ? AND id != ? AND deleted_at IS NULL", *req.Name, userID, id).First(&existingProject).Error; err == nil {
			return nil, errors.New("project with this name already exists")
		}
		updates["name"] = *req.Name
	}

	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if req.Repository != nil {
		if err := s.validateRepository(ctx, req.Repository); err != nil {
			return nil, fmt.Errorf("repository validation failed: %w", err)
		}
		updates["repository"] = *req.Repository
	}

	if req.Framework != nil {
		updates["framework"] = *req.Framework
	}

	if req.Language != nil {
		updates["language"] = *req.Language
	}

	if req.BuildConfig != nil {
		updates["build_config"] = *req.BuildConfig
	}

	if req.Tags != nil {
		updates["tags"] = req.Tags
	}

	if req.IsPrivate != nil {
		updates["is_private"] = *req.IsPrivate
	}

	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}

	if req.Status != nil {
		updates["status"] = *req.Status
	}

	// Mise à jour en base
	if err := s.db.Model(project).Updates(updates).Error; err != nil {
		return nil, err
	}

	// Rechargement du projet
	updatedProject, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	s.logger.Info("Project updated successfully", "project_id", id, "user_id", userID)
	return updatedProject, nil
}

// Delete supprime un projet (soft delete)
func (s *ProjectService) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return errors.New("permission denied")
	}

	// Suppression du webhook
	if err := s.RemoveWebhook(ctx, id, userID); err != nil {
		s.logger.Warn("Failed to remove webhook", "error", err, "project_id", id)
	}

	// Soft delete
	if err := s.db.Delete(project).Error; err != nil {
		return err
	}

	s.logger.Info("Project deleted successfully", "project_id", id, "user_id", userID)
	return nil
}

// List liste les projets avec filtres et pagination
func (s *ProjectService) List(ctx context.Context, filter *ProjectFilter, sort *ProjectSort, page, perPage int, userID uuid.UUID) (*ProjectListResponse, error) {
	var projects []Project
	var total int64

	// Construction de la requête
	query := s.db.WithContext(ctx).Model(&Project{})

	// Filtres de permissions
	query = query.Where("owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)", userID, userID)

	// Application des filtres
	if filter != nil {
		query = s.applyFilters(query, filter)
	}

	// Comptage total
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	// Application du tri
	if sort != nil {
		query = query.Order(fmt.Sprintf("%s %s", sort.Field, sort.Direction))
	} else {
		query = query.Order("created_at DESC")
	}

	// Pagination
	offset := (page - 1) * perPage
	query = query.Offset(offset).Limit(perPage)

	// Récupération des projets
	if err := query.Preload("Deployments", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC").Limit(1)
	}).Find(&projects).Error; err != nil {
		return nil, err
	}

	// Conversion en résumés
	summaries := make([]ProjectSummary, len(projects))
	for i, project := range projects {
		summaries[i] = project.ToSummary()
	}

	totalPages := int((total + int64(perPage) - 1) / int64(perPage))

	return &ProjectListResponse{
		Projects:   summaries,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}

// ListByTeam liste les projets d'une équipe
func (s *ProjectService) ListByTeam(ctx context.Context, teamID uuid.UUID, userID uuid.UUID) ([]Project, error) {
	var projects []Project

	query := s.db.WithContext(ctx).
		Where("team_id = ? AND team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)", teamID, userID).
		Order("created_at DESC")

	if err := query.Find(&projects).Error; err != nil {
		return nil, err
	}

	return projects, nil
}

// ListByOwner liste les projets d'un propriétaire
func (s *ProjectService) ListByOwner(ctx context.Context, ownerID uuid.UUID, userID uuid.UUID) ([]Project, error) {
	var projects []Project

	// Vérification des permissions - seul le propriétaire peut voir ses projets
	if ownerID != userID {
		return nil, errors.New("permission denied")
	}

	query := s.db.WithContext(ctx).
		Where("owner_id = ?", ownerID).
		Order("created_at DESC")

	if err := query.Find(&projects).Error; err != nil {
		return nil, err
	}

	return projects, nil
}

// SyncRepository synchronise le projet avec le repository
func (s *ProjectService) SyncRepository(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Synchronisation avec le service Git
	repoInfo, err := s.gitService.GetRepositoryInfo(ctx, project.Repository.URL, project.Repository.Token)
	if err != nil {
		return fmt.Errorf("failed to sync repository: %w", err)
	}

	// Mise à jour des informations du repository
	updates := map[string]interface{}{
		"repository": Repository{
			URL:        project.Repository.URL,
			Branch:     project.Repository.Branch,
			Provider:   project.Repository.Provider,
			Token:      project.Repository.Token,
			WebhookID:  project.Repository.WebhookID,
			LastCommit: repoInfo.LastCommit,
			LastSync:   time.Now(),
		},
	}

	if err := s.db.Model(project).Updates(updates).Error; err != nil {
		return err
	}

	s.logger.Info("Repository synchronized successfully", "project_id", id, "user_id", userID)
	return nil
}

// SetupWebhook configure un webhook pour le projet
func (s *ProjectService) SetupWebhook(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Configuration du webhook via le service Git
	webhookID, err := s.gitService.CreateWebhook(ctx, project.Repository.URL, project.Repository.Token, fmt.Sprintf("/api/webhooks/git/%s", id))
	if err != nil {
		return fmt.Errorf("failed to setup webhook: %w", err)
	}

	// Mise à jour du webhook ID
	if err := s.db.Model(project).Update("repository.webhook_id", webhookID).Error; err != nil {
		return err
	}

	s.logger.Info("Webhook setup successfully", "project_id", id, "webhook_id", webhookID)
	return nil
}

// RemoveWebhook supprime le webhook du projet
func (s *ProjectService) RemoveWebhook(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	if project.Repository.WebhookID != "" {
		if err := s.gitService.RemoveWebhook(ctx, project.Repository.URL, project.Repository.Token, project.Repository.WebhookID); err != nil {
			return fmt.Errorf("failed to remove webhook: %w", err)
		}

		// Suppression du webhook ID
		if err := s.db.Model(project).Update("repository.webhook_id", "").Error; err != nil {
			return err
		}
	}

	s.logger.Info("Webhook removed successfully", "project_id", id)
	return nil
}

// UpdateEnvVars met à jour les variables d'environnement
func (s *ProjectService) UpdateEnvVars(ctx context.Context, id uuid.UUID, envVars []EnvVarRequest, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return errors.New("permission denied")
	}

	// Transaction pour mettre à jour les variables
	return s.db.Transaction(func(tx *gorm.DB) error {
		// Suppression des anciennes variables
		if err := tx.Where("project_id = ?", id).Delete(&EnvVar{}).Error; err != nil {
			return err
		}

		// Création des nouvelles variables
		if len(envVars) > 0 {
			newEnvVars := make([]EnvVar, len(envVars))
			for i, envVar := range envVars {
				newEnvVars[i] = EnvVar{
					ID:        uuid.New(),
					ProjectID: id,
					Key:       envVar.Key,
					Value:     envVar.Value,
					IsSecret:  envVar.IsSecret,
				}
			}
			if err := tx.Create(&newEnvVars).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// GetEnvVars récupère les variables d'environnement
func (s *ProjectService) GetEnvVars(ctx context.Context, id uuid.UUID, userID uuid.UUID) ([]EnvVar, error) {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return nil, errors.New("permission denied")
	}

	var envVars []EnvVar
	if err := s.db.Where("project_id = ?", id).Find(&envVars).Error; err != nil {
		return nil, err
	}

	return envVars, nil
}

// ValidateBuildConfig valide la configuration de build
func (s *ProjectService) ValidateBuildConfig(ctx context.Context, config *BuildConfig) error {
	if config == nil {
		return errors.New("build config is required")
	}

	// Validation du Dockerfile
	if config.DockerfilePath != "" {
		if !strings.HasSuffix(config.DockerfilePath, "Dockerfile") {
			return errors.New("invalid dockerfile path")
		}
	}

	// Validation des commandes
	if len(config.BuildCommands) == 0 {
		return errors.New("at least one build command is required")
	}

	// Validation du port
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}

	return nil
}

// TestBuild teste la configuration de build
func (s *ProjectService) TestBuild(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return errors.New("permission denied")
	}

	// Test de build via le service Docker
	if err := s.dockerService.TestBuild(ctx, project.Repository.URL, project.BuildConfig); err != nil {
		return fmt.Errorf("build test failed: %w", err)
	}

	s.logger.Info("Build test successful", "project_id", id, "user_id", userID)
	return nil
}

// GetStats récupère les statistiques du projet
func (s *ProjectService) GetStats(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*ProjectStats, error) {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	stats := &ProjectStats{
		ProjectID: id,
	}

	// Comptage des déploiements
	if err := s.db.Model(&Deployment{}).Where("project_id = ?", id).Count(&stats.TotalDeployments).Error; err != nil {
		return nil, err
	}

	// Comptage des déploiements réussis
	if err := s.db.Model(&Deployment{}).Where("project_id = ? AND status = ?", id, "success").Count(&stats.SuccessfulDeployments).Error; err != nil {
		return nil, err
	}

	// Comptage des déploiements échoués
	if err := s.db.Model(&Deployment{}).Where("project_id = ? AND status = ?", id, "failed").Count(&stats.FailedDeployments).Error; err != nil {
		return nil, err
	}

	// Dernier déploiement
	var lastDeployment Deployment
	if err := s.db.Where("project_id = ?", id).Order("created_at DESC").First(&lastDeployment).Error; err == nil {
		stats.LastDeployment = &lastDeployment.CreatedAt
	}

	// Calcul du taux de réussite
	if stats.TotalDeployments > 0 {
		stats.SuccessRate = float64(stats.SuccessfulDeployments) / float64(stats.TotalDeployments) * 100
	}

	return stats, nil
}

// GetDashboardData récupère les données du dashboard
func (s *ProjectService) GetDashboardData(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	data := make(map[string]interface{})

	// Nombre total de projets
	var totalProjects int64
	if err := s.db.Model(&Project{}).Where("owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)", userID, userID).Count(&totalProjects).Error; err != nil {
		return nil, err
	}
	data["total_projects"] = totalProjects

	// Projets actifs
	var activeProjects int64
	if err := s.db.Model(&Project{}).Where("(owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)) AND is_active = true", userID, userID).Count(&activeProjects).Error; err != nil {
		return nil, err
	}
	data["active_projects"] = activeProjects

	// Projets récents (derniers 30 jours)
	var recentProjects int64
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	if err := s.db.Model(&Project{}).Where("(owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)) AND created_at > ?", userID, userID, thirtyDaysAgo).Count(&recentProjects).Error; err != nil {
		return nil, err
	}
	data["recent_projects"] = recentProjects

	// Projets les plus récents
	var latestProjects []Project
	if err := s.db.Where("owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)", userID, userID).Order("created_at DESC").Limit(5).Find(&latestProjects).Error; err != nil {
		return nil, err
	}

	summaries := make([]ProjectSummary, len(latestProjects))
	for i, project := range latestProjects {
		summaries[i] = project.ToSummary()
	}
	data["latest_projects"] = summaries

	return data, nil
}

// Archive archive un projet
func (s *ProjectService) Archive(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return errors.New("permission denied")
	}

	// Mise à jour du statut
	if err := s.db.Model(project).Updates(map[string]interface{}{
		"status":     ProjectStatusArchived,
		"is_active":  false,
		"updated_at": time.Now(),
	}).Error; err != nil {
		return err
	}

	s.logger.Info("Project archived successfully", "project_id", id, "user_id", userID)
	return nil
}

// Restore restaure un projet archivé
func (s *ProjectService) Restore(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	project, err := s.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	// Vérification des permissions
	if !project.IsOwner(userID) {
		return errors.New("permission denied")
	}

	// Mise à jour du statut
	if err := s.db.Model(project).Updates(map[string]interface{}{
		"status":     ProjectStatusActive,
		"is_active":  true,
		"updated_at": time.Now(),
	}).Error; err != nil {
		return err
	}

	s.logger.Info("Project restored successfully", "project_id", id, "user_id", userID)
	return nil
}

// Search recherche des projets
func (s *ProjectService) Search(ctx context.Context, query string, userID uuid.UUID) ([]Project, error) {
	var projects []Project

	searchQuery := "%" + strings.ToLower(query) + "%"

	dbQuery := s.db.WithContext(ctx).
		Where("(owner_id = ? OR team_id IN (SELECT team_id FROM team_members WHERE user_id = ?))", userID, userID).
		Where("(LOWER(name) LIKE ? OR LOWER(description) LIKE ? OR LOWER(tags) LIKE ?)", searchQuery, searchQuery, searchQuery).
		Order("created_at DESC").
		Limit(50)

	if err := dbQuery.Find(&projects).Error; err != nil {
		return nil, err
	}

	return projects, nil
}

// applyFilters applique les filtres à la requête
func (s *ProjectService) applyFilters(query *gorm.DB, filter *ProjectFilter) *gorm.DB {
	if filter.Name != "" {
		query = query.Where("name ILIKE ?", "%"+filter.Name+"%")
	}

	if filter.Framework != "" {
		query = query.Where("framework = ?", filter.Framework)
	}

	if filter.Language != "" {
		query = query.Where("language = ?", filter.Language)
	}

	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}

	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}

	if filter.IsPrivate != nil {
		query = query.Where("is_private = ?", *filter.IsPrivate)
	}

	if filter.TeamID != nil {
		query = query.Where("team_id = ?", *filter.TeamID)
	}

	if filter.Tags != nil && len(filter.Tags) > 0 {
		for _, tag := range filter.Tags {
			query = query.Where("tags LIKE ?", "%"+tag+"%")
		}
	}

	if !filter.CreatedAfter.IsZero() {
		query = query.Where("created_at > ?", filter.CreatedAfter)
	}

	if !filter.CreatedBefore.IsZero() {
		query = query.Where("created_at < ?", filter.CreatedBefore)
	}

	return query
}

// validateRepository valide les informations du repository
func (s *ProjectService) validateRepository(ctx context.Context, repo *Repository) error {
	if repo == nil {
		return errors.New("repository is required")
	}

	if repo.URL == "" {
		return errors.New("repository URL is required")
	}

	if repo.Provider == "" {
		return errors.New("repository provider is required")
	}

	if repo.Branch == "" {
		repo.Branch = "main" // Branche par défaut
	}

	// Validation de l'URL
	if !strings.HasPrefix(repo.URL, "https://") && !strings.HasPrefix(repo.URL, "http://") {
		return errors.New("repository URL must be a valid HTTP/HTTPS URL")
	}

	// Validation du provider
	validProviders := []string{"github", "gitlab", "bitbucket"}
	isValidProvider := false
	for _, provider := range validProviders {
		if strings.ToLower(repo.Provider) == provider {
			isValidProvider = true
			break
		}
	}
	if !isValidProvider {
		return errors.New("repository provider must be one of: github, gitlab, bitbucket")
	}

	// Validation de l'accès au repository via le service Git
	if err := s.gitService.ValidateRepository(ctx, repo.URL, repo.Token); err != nil {
		return fmt.Errorf("repository validation failed: %w", err)
	}

	return nil
}
