package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Énumérations pour les statuts et types
type DeploymentStatus string
type ProjectStatus string
type UserRole string
type NotificationLevel string

const (
	// Statuts de déploiement
	DeploymentStatusPending     DeploymentStatus = "pending"
	DeploymentStatusBuilding    DeploymentStatus = "building"
	DeploymentStatusDeploying   DeploymentStatus = "deploying"
	DeploymentStatusRunning     DeploymentStatus = "running"
	DeploymentStatusFailed      DeploymentStatus = "failed"
	DeploymentStatusStopped     DeploymentStatus = "stopped"
	DeploymentStatusRollingBack DeploymentStatus = "rolling_back"

	// Statuts de projet
	ProjectStatusActive   ProjectStatus = "active"
	ProjectStatusInactive ProjectStatus = "inactive"
	ProjectStatusArchived ProjectStatus = "archived"

	// Rôles utilisateur
	UserRoleAdmin  UserRole = "admin"
	UserRoleMember UserRole = "member"
	UserRoleViewer UserRole = "viewer"
	UserRoleOwner  UserRole = "owner"

	// Niveaux de notification
	NotificationLevelInfo    NotificationLevel = "info"
	NotificationLevelWarning NotificationLevel = "warning"
	NotificationLevelError   NotificationLevel = "error"
	NotificationLevelSuccess NotificationLevel = "success"
)

// Types JSON personnalisés pour PostgreSQL
type JSONMap map[string]interface{}

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*j = make(map[string]interface{})
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot scan %T into JSONMap", value)
	}

	return json.Unmarshal(b, j)
}

// Modèle User
type User struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	Email        string     `db:"email" json:"email"`
	Username     string     `db:"username" json:"username"`
	FirstName    string     `db:"first_name" json:"first_name"`
	LastName     string     `db:"last_name" json:"last_name"`
	PasswordHash string     `db:"password_hash" json:"-"`
	Role         UserRole   `db:"role" json:"role"`
	IsActive     bool       `db:"is_active" json:"is_active"`
	LastLogin    *time.Time `db:"last_login" json:"last_login"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
	DeletedAt    *time.Time `db:"deleted_at" json:"deleted_at"`

	// Relations
	Projects        []Project        `json:"projects,omitempty"`
	TeamMemberships []TeamMembership `json:"team_memberships,omitempty"`
}

// Modèle Team
type Team struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	Description string     `db:"description" json:"description"`
	CreatedBy   uuid.UUID  `db:"created_by" json:"created_by"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updated_at"`
	DeletedAt   *time.Time `db:"deleted_at" json:"deleted_at"`

	// Relations
	Members  []TeamMembership `json:"members,omitempty"`
	Projects []Project        `json:"projects,omitempty"`
}

// Modèle TeamMembership
type TeamMembership struct {
	ID       uuid.UUID `db:"id" json:"id"`
	TeamID   uuid.UUID `db:"team_id" json:"team_id"`
	UserID   uuid.UUID `db:"user_id" json:"user_id"`
	Role     UserRole  `db:"role" json:"role"`
	JoinedAt time.Time `db:"joined_at" json:"joined_at"`

	// Relations
	User User `json:"user,omitempty"`
	Team Team `json:"team,omitempty"`
}

// Modèle Project
type Project struct {
	ID            uuid.UUID     `db:"id" json:"id"`
	Name          string        `db:"name" json:"name"`
	Description   string        `db:"description" json:"description"`
	Repository    string        `db:"repository" json:"repository"`
	Branch        string        `db:"branch" json:"branch"`
	Status        ProjectStatus `db:"status" json:"status"`
	TeamID        *uuid.UUID    `db:"team_id" json:"team_id"`
	CreatedBy     uuid.UUID     `db:"created_by" json:"created_by"`
	Configuration JSONMap       `db:"configuration" json:"configuration"`
	Environment   JSONMap       `db:"environment" json:"environment"`
	Secrets       JSONMap       `db:"secrets" json:"secrets"`
	CreatedAt     time.Time     `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time     `db:"updated_at" json:"updated_at"`
	DeletedAt     *time.Time    `db:"deleted_at" json:"deleted_at"`

	// Relations
	Creator     User         `json:"creator,omitempty"`
	Team        *Team        `json:"team,omitempty"`
	Deployments []Deployment `json:"deployments,omitempty"`
}

// Modèle Deployment
type Deployment struct {
	ID            uuid.UUID        `db:"id" json:"id"`
	ProjectID     uuid.UUID        `db:"project_id" json:"project_id"`
	Version       string           `db:"version" json:"version"`
	GitCommit     string           `db:"git_commit" json:"git_commit"`
	Status        DeploymentStatus `db:"status" json:"status"`
	Environment   string           `db:"environment" json:"environment"`
	DeployedBy    uuid.UUID        `db:"deployed_by" json:"deployed_by"`
	ImageTag      string           `db:"image_tag" json:"image_tag"`
	Configuration JSONMap          `db:"configuration" json:"configuration"`
	Resources     JSONMap          `db:"resources" json:"resources"`
	Replicas      int              `db:"replicas" json:"replicas"`
	CPU           string           `db:"cpu" json:"cpu"`
	Memory        string           `db:"memory" json:"memory"`
	StartedAt     *time.Time       `db:"started_at" json:"started_at"`
	CompletedAt   *time.Time       `db:"completed_at" json:"completed_at"`
	CreatedAt     time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time        `db:"updated_at" json:"updated_at"`

	// Relations
	Project      Project            `json:"project,omitempty"`
	DeployedUser User               `json:"deployed_user,omitempty"`
	Logs         []DeploymentLog    `json:"logs,omitempty"`
	Metrics      []DeploymentMetric `json:"metrics,omitempty"`
}

// Modèle DeploymentLog
type DeploymentLog struct {
	ID           uuid.UUID `db:"id" json:"id"`
	DeploymentID uuid.UUID `db:"deployment_id" json:"deployment_id"`
	Level        string    `db:"level" json:"level"`
	Message      string    `db:"message" json:"message"`
	Source       string    `db:"source" json:"source"`
	Timestamp    time.Time `db:"timestamp" json:"timestamp"`

	// Relations
	Deployment Deployment `json:"deployment,omitempty"`
}

// Modèle DeploymentMetric
type DeploymentMetric struct {
	ID           uuid.UUID `db:"id" json:"id"`
	DeploymentID uuid.UUID `db:"deployment_id" json:"deployment_id"`
	MetricName   string    `db:"metric_name" json:"metric_name"`
	Value        float64   `db:"value" json:"value"`
	Unit         string    `db:"unit" json:"unit"`
	Labels       JSONMap   `db:"labels" json:"labels"`
	Timestamp    time.Time `db:"timestamp" json:"timestamp"`

	// Relations
	Deployment Deployment `json:"deployment,omitempty"`
}

// Modèle Notification
type Notification struct {
	ID        uuid.UUID         `db:"id" json:"id"`
	UserID    uuid.UUID         `db:"user_id" json:"user_id"`
	Type      string            `db:"type" json:"type"`
	Title     string            `db:"title" json:"title"`
	Message   string            `db:"message" json:"message"`
	Level     NotificationLevel `db:"level" json:"level"`
	IsRead    bool              `db:"is_read" json:"is_read"`
	Data      JSONMap           `db:"data" json:"data"`
	CreatedAt time.Time         `db:"created_at" json:"created_at"`
	ReadAt    *time.Time        `db:"read_at" json:"read_at"`

	// Relations
	User User `json:"user,omitempty"`
}

// Modèle WebhookEvent
type WebhookEvent struct {
	ID        uuid.UUID `db:"id" json:"id"`
	ProjectID uuid.UUID `db:"project_id" json:"project_id"`
	Event     string    `db:"event" json:"event"`
	Payload   JSONMap   `db:"payload" json:"payload"`
	Processed bool      `db:"processed" json:"processed"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`

	// Relations
	Project Project `json:"project,omitempty"`
}

// Modèle SystemSettings
type SystemSettings struct {
	ID        uuid.UUID `db:"id" json:"id"`
	Key       string    `db:"key" json:"key"`
	Value     string    `db:"value" json:"value"`
	Type      string    `db:"type" json:"type"`
	IsSecret  bool      `db:"is_secret" json:"is_secret"`
	UpdatedBy uuid.UUID `db:"updated_by" json:"updated_by"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`

	// Relations
	UpdatedUser User `json:"updated_user,omitempty"`
}

// Repository interfaces
type UserRepository interface {
	Create(user *User) error
	GetByID(id uuid.UUID) (*User, error)
	GetByEmail(email string) (*User, error)
	GetByUsername(username string) (*User, error)
	Update(user *User) error
	Delete(id uuid.UUID) error
	List(limit, offset int) ([]User, error)
	Search(query string, limit, offset int) ([]User, error)
}

type ProjectRepository interface {
	Create(project *Project) error
	GetByID(id uuid.UUID) (*Project, error)
	GetByName(name string) (*Project, error)
	Update(project *Project) error
	Delete(id uuid.UUID) error
	List(limit, offset int) ([]Project, error)
	GetByUserID(userID uuid.UUID, limit, offset int) ([]Project, error)
	GetByTeamID(teamID uuid.UUID, limit, offset int) ([]Project, error)
}

type DeploymentRepository interface {
	Create(deployment *Deployment) error
	GetByID(id uuid.UUID) (*Deployment, error)
	Update(deployment *Deployment) error
	Delete(id uuid.UUID) error
	List(limit, offset int) ([]Deployment, error)
	GetByProjectID(projectID uuid.UUID, limit, offset int) ([]Deployment, error)
	GetByStatus(status DeploymentStatus, limit, offset int) ([]Deployment, error)
	GetActiveDeployments() ([]Deployment, error)
}

type TeamRepository interface {
	Create(team *Team) error
	GetByID(id uuid.UUID) (*Team, error)
	Update(team *Team) error
	Delete(id uuid.UUID) error
	List(limit, offset int) ([]Team, error)
	AddMember(teamID, userID uuid.UUID, role UserRole) error
	RemoveMember(teamID, userID uuid.UUID) error
	GetMembers(teamID uuid.UUID) ([]TeamMembership, error)
}

type NotificationRepository interface {
	Create(notification *Notification) error
	GetByID(id uuid.UUID) (*Notification, error)
	GetByUserID(userID uuid.UUID, limit, offset int) ([]Notification, error)
	MarkAsRead(id uuid.UUID) error
	MarkAllAsRead(userID uuid.UUID) error
	Delete(id uuid.UUID) error
	GetUnreadCount(userID uuid.UUID) (int, error)
}

// Implémentation des repositories
type repositories struct {
	db *sqlx.DB
}

func NewRepositories(db *sqlx.DB) *repositories {
	return &repositories{db: db}
}

// Méthodes utilitaires communes
func (r *repositories) generateID() uuid.UUID {
	return uuid.New()
}

func (r *repositories) now() time.Time {
	return time.Now().UTC()
}

// Fonction pour vérifier les contraintes de foreign key
func (r *repositories) checkForeignKey(tableName, column string, id uuid.UUID) error {
	var exists bool
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND deleted_at IS NULL)", tableName)
	err := r.db.QueryRow(query, id).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("foreign key violation: %s with id %s does not exist", column, id)
	}
	return nil
}

// Fonction pour la pagination
type PaginationResult struct {
	Items      interface{} `json:"items"`
	TotalCount int         `json:"total_count"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

func CalculatePagination(totalCount, page, pageSize int) PaginationResult {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	totalPages := (totalCount + pageSize - 1) / pageSize

	return PaginationResult{
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}
}

// Fonction pour les recherches full-text
func buildSearchQuery(baseQuery string, searchTerm string, searchColumns []string) string {
	if searchTerm == "" {
		return baseQuery
	}

	searchConditions := make([]string, len(searchColumns))
	for i, column := range searchColumns {
		searchConditions[i] = fmt.Sprintf("%s ILIKE '%%' || $1 || '%%'", column)
	}

	searchClause := " AND (" + fmt.Sprintf("%s", searchConditions[0])
	for i := 1; i < len(searchConditions); i++ {
		searchClause += " OR " + searchConditions[i]
	}
	searchClause += ")"

	return baseQuery + searchClause
}

// Fonction pour les filtres de date
func buildDateFilter(baseQuery string, startDate, endDate *time.Time, dateColumn string) string {
	if startDate != nil {
		baseQuery += fmt.Sprintf(" AND %s >= '%s'", dateColumn, startDate.Format(time.RFC3339))
	}
	if endDate != nil {
		baseQuery += fmt.Sprintf(" AND %s <= '%s'", dateColumn, endDate.Format(time.RFC3339))
	}
	return baseQuery
}
