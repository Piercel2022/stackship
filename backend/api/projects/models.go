package projects

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Project représente un projet dans l'application
type Project struct {
	ID          uuid.UUID     `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Name        string        `json:"name" gorm:"not null;index" validate:"required,min=3,max=100"`
	Description string        `json:"description" gorm:"type:text"`
	Repository  Repository    `json:"repository" gorm:"embedded"`
	Status      ProjectStatus `json:"status" gorm:"default:'active'"`
	Framework   string        `json:"framework" validate:"required"`
	Language    string        `json:"language" validate:"required"`

	// Relations
	OwnerID     uuid.UUID    `json:"owner_id" gorm:"type:uuid;not null;index"`
	TeamID      *uuid.UUID   `json:"team_id" gorm:"type:uuid;index"`
	Deployments []Deployment `json:"deployments,omitempty" gorm:"foreignKey:ProjectID"`

	// Configuration
	BuildConfig BuildConfig `json:"build_config" gorm:"embedded"`
	EnvVars     []EnvVar    `json:"env_vars,omitempty" gorm:"foreignKey:ProjectID"`

	// Métadonnées
	Tags      []string `json:"tags" gorm:"type:text[]"`
	IsPrivate bool     `json:"is_private" gorm:"default:false"`
	IsActive  bool     `json:"is_active" gorm:"default:true"`

	// Timestamps
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}

// Repository contient les informations du dépôt Git
type Repository struct {
	URL        string    `json:"url" validate:"required,url"`
	Branch     string    `json:"branch" gorm:"default:'main'"`
	Provider   string    `json:"provider"`           // github, gitlab, bitbucket
	Token      string    `json:"-" gorm:"type:text"` // Token d'accès (non sérialisé)
	WebhookID  string    `json:"webhook_id"`
	LastCommit string    `json:"last_commit"`
	LastSync   time.Time `json:"last_sync"`
}

// BuildConfig contient la configuration de build
type BuildConfig struct {
	Dockerfile   string            `json:"dockerfile" gorm:"default:'Dockerfile'"`
	BuildContext string            `json:"build_context" gorm:"default:'.'"`
	BuildArgs    map[string]string `json:"build_args" gorm:"type:jsonb"`
	Target       string            `json:"target"` // Multi-stage build target
	Registry     string            `json:"registry"`
	ImageTag     string            `json:"image_tag"`
}

// EnvVar représente une variable d'environnement
type EnvVar struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	ProjectID uuid.UUID `json:"project_id" gorm:"type:uuid;not null;index"`
	Key       string    `json:"key" gorm:"not null" validate:"required"`
	Value     string    `json:"value" gorm:"type:text"`
	IsSecret  bool      `json:"is_secret" gorm:"default:false"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Deployment représente un déploiement (relation avec le module deployments)
type Deployment struct {
	ID          uuid.UUID        `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	ProjectID   uuid.UUID        `json:"project_id" gorm:"type:uuid;not null;index"`
	Status      DeploymentStatus `json:"status"`
	Version     string           `json:"version"`
	Environment string           `json:"environment"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// ProjectStatus énumère les statuts possibles d'un projet
type ProjectStatus string

const (
	ProjectStatusActive   ProjectStatus = "active"
	ProjectStatusInactive ProjectStatus = "inactive"
	ProjectStatusArchived ProjectStatus = "archived"
	ProjectStatusDeleted  ProjectStatus = "deleted"
)

// DeploymentStatus énumère les statuts de déploiement
type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusSuccess   DeploymentStatus = "success"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	DeploymentStatusCancelled DeploymentStatus = "cancelled"
)

// ProjectCreateRequest structure pour créer un projet
type ProjectCreateRequest struct {
	Name        string          `json:"name" validate:"required,min=3,max=100"`
	Description string          `json:"description"`
	Repository  Repository      `json:"repository" validate:"required"`
	Framework   string          `json:"framework" validate:"required"`
	Language    string          `json:"language" validate:"required"`
	BuildConfig BuildConfig     `json:"build_config"`
	EnvVars     []EnvVarRequest `json:"env_vars"`
	Tags        []string        `json:"tags"`
	IsPrivate   bool            `json:"is_private"`
	TeamID      *uuid.UUID      `json:"team_id"`
}

// ProjectUpdateRequest structure pour mettre à jour un projet
type ProjectUpdateRequest struct {
	Name        *string        `json:"name" validate:"omitempty,min=3,max=100"`
	Description *string        `json:"description"`
	Repository  *Repository    `json:"repository"`
	Framework   *string        `json:"framework"`
	Language    *string        `json:"language"`
	BuildConfig *BuildConfig   `json:"build_config"`
	Tags        []string       `json:"tags"`
	IsPrivate   *bool          `json:"is_private"`
	IsActive    *bool          `json:"is_active"`
	Status      *ProjectStatus `json:"status"`
}

// EnvVarRequest structure pour les variables d'environnement
type EnvVarRequest struct {
	Key      string `json:"key" validate:"required"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// ProjectListResponse structure pour la liste des projets
type ProjectListResponse struct {
	Projects   []ProjectSummary `json:"projects"`
	Total      int64            `json:"total"`
	Page       int              `json:"page"`
	PerPage    int              `json:"per_page"`
	TotalPages int              `json:"total_pages"`
}

// ProjectSummary résumé d'un projet pour les listes
type ProjectSummary struct {
	ID              uuid.UUID     `json:"id"`
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	Status          ProjectStatus `json:"status"`
	Framework       string        `json:"framework"`
	Language        string        `json:"language"`
	Repository      Repository    `json:"repository"`
	Tags            []string      `json:"tags"`
	IsPrivate       bool          `json:"is_private"`
	IsActive        bool          `json:"is_active"`
	DeploymentCount int           `json:"deployment_count"`
	LastDeployment  *time.Time    `json:"last_deployment"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// ProjectStats statistiques d'un projet
type ProjectStats struct {
	TotalDeployments      int            `json:"total_deployments"`
	SuccessfulDeployments int            `json:"successful_deployments"`
	FailedDeployments     int            `json:"failed_deployments"`
	LastDeployment        *time.Time     `json:"last_deployment"`
	AverageDeployTime     *float64       `json:"average_deploy_time"`
	DeploymentsByStatus   map[string]int `json:"deployments_by_status"`
	DeploymentsByEnv      map[string]int `json:"deployments_by_env"`
}

// ProjectFilter critères de filtrage pour les projets
type ProjectFilter struct {
	Status        []ProjectStatus `json:"status"`
	Framework     []string        `json:"framework"`
	Language      []string        `json:"language"`
	Tags          []string        `json:"tags"`
	IsPrivate     *bool           `json:"is_private"`
	IsActive      *bool           `json:"is_active"`
	OwnerID       *uuid.UUID      `json:"owner_id"`
	TeamID        *uuid.UUID      `json:"team_id"`
	Search        string          `json:"search"`
	CreatedAfter  *time.Time      `json:"created_after"`
	CreatedBefore *time.Time      `json:"created_before"`
}

// ProjectSort options de tri
type ProjectSort struct {
	Field     string `json:"field" validate:"oneof=name created_at updated_at"`
	Direction string `json:"direction" validate:"oneof=asc desc"`
}

// BeforeCreate hook GORM pour initialiser les valeurs par défaut
func (p *Project) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Repository.Branch == "" {
		p.Repository.Branch = "main"
	}
	if p.BuildConfig.Dockerfile == "" {
		p.BuildConfig.Dockerfile = "Dockerfile"
	}
	if p.BuildConfig.BuildContext == "" {
		p.BuildConfig.BuildContext = "."
	}
	return nil
}

// ToSummary convertit un projet en résumé
func (p *Project) ToSummary() ProjectSummary {
	var lastDeployment *time.Time
	if len(p.Deployments) > 0 {
		lastDeployment = &p.Deployments[0].CreatedAt
	}

	return ProjectSummary{
		ID:              p.ID,
		Name:            p.Name,
		Description:     p.Description,
		Status:          p.Status,
		Framework:       p.Framework,
		Language:        p.Language,
		Repository:      p.Repository,
		Tags:            p.Tags,
		IsPrivate:       p.IsPrivate,
		IsActive:        p.IsActive,
		DeploymentCount: len(p.Deployments),
		LastDeployment:  lastDeployment,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

// IsOwner vérifie si l'utilisateur est propriétaire du projet
func (p *Project) IsOwner(userID uuid.UUID) bool {
	return p.OwnerID == userID
}

// HasTag vérifie si le projet a un tag spécifique
func (p *Project) HasTag(tag string) bool {
	for _, t := range p.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// AddTag ajoute un tag au projet
func (p *Project) AddTag(tag string) {
	if !p.HasTag(tag) {
		p.Tags = append(p.Tags, tag)
	}
}

// RemoveTag supprime un tag du projet
func (p *Project) RemoveTag(tag string) {
	for i, t := range p.Tags {
		if t == tag {
			p.Tags = append(p.Tags[:i], p.Tags[i+1:]...)
			break
		}
	}
}
