package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stackship/backend/database"
	"github.com/stackship/backend/services/docker"
	"github.com/stackship/backend/services/git"
	"github.com/stackship/backend/services/kubernetes"
	"github.com/stackship/backend/services/notification"
	"github.com/stackship/backend/api/websocket"
	"github.com/stackship/backend/utils"
)

// PipelineStage represents different stages of deployment pipeline
type PipelineStage string

const (
	StageClone       PipelineStage = "clone"
	StageBuild       PipelineStage = "build"
	StageTest        PipelineStage = "test"
	StagePush        PipelineStage = "push"
	StageDeploy      PipelineStage = "deploy"
	StageHealthCheck PipelineStage = "health_check"
	StageComplete    PipelineStage = "complete"
	StageFailed      PipelineStage = "failed"
)

// PipelineStatus represents the status of a pipeline stage
type PipelineStatus string

const (
	StatusPending    PipelineStatus = "pending"
	StatusRunning    PipelineStatus = "running"
	StatusSuccess    PipelineStatus = "success"
	StatusFailed     PipelineStatus = "failed"
	StatusSkipped    PipelineStatus = "skipped"
	StatusCancelled  PipelineStatus = "cancelled"
)

// PipelineConfig holds configuration for deployment pipeline
type PipelineConfig struct {
	ID               string                 `json:"id" db:"id"`
	ProjectID        string                 `json:"project_id" db:"project_id"`
	Name             string                 `json:"name" db:"name"`
	Description      string                 `json:"description" db:"description"`
	GitRepository    string                 `json:"git_repository" db:"git_repository"`
	GitBranch        string                 `json:"git_branch" db:"git_branch"`
	BuildCommand     string                 `json:"build_command" db:"build_command"`
	TestCommand      string                 `json:"test_command" db:"test_command"`
	DockerfilePath   string                 `json:"dockerfile_path" db:"dockerfile_path"`
	KubernetesConfig map[string]interface{} `json:"kubernetes_config" db:"kubernetes_config"`
	Environment      string                 `json:"environment" db:"environment"`
	AutoDeploy       bool                   `json:"auto_deploy" db:"auto_deploy"`
	Notifications    []string               `json:"notifications" db:"notifications"`
	CreatedAt        time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at" db:"updated_at"`
}

// PipelineExecution represents a single execution of a pipeline
type PipelineExecution struct {
	ID           string                    `json:"id" db:"id"`
	PipelineID   string                    `json:"pipeline_id" db:"pipeline_id"`
	ProjectID    string                    `json:"project_id" db:"project_id"`
	UserID       string                    `json:"user_id" db:"user_id"`
	CommitHash   string                    `json:"commit_hash" db:"commit_hash"`
	CommitMessage string                   `json:"commit_message" db:"commit_message"`
	Status       PipelineStatus            `json:"status" db:"status"`
	CurrentStage PipelineStage             `json:"current_stage" db:"current_stage"`
	Stages       []PipelineStageExecution  `json:"stages"`
	StartedAt    time.Time                 `json:"started_at" db:"started_at"`
	CompletedAt  *time.Time                `json:"completed_at" db:"completed_at"`
	Duration     int64                     `json:"duration" db:"duration"`
	Logs         []PipelineLog             `json:"logs"`
	Artifacts    []PipelineArtifact        `json:"artifacts"`
	CreatedAt    time.Time                 `json:"created_at" db:"created_at"`
}

// PipelineStageExecution represents execution of a single stage
type PipelineStageExecution struct {
	Stage       PipelineStage  `json:"stage"`
	Status      PipelineStatus `json:"status"`
	StartedAt   *time.Time     `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at"`
	Duration    int64          `json:"duration"`
	Output      string         `json:"output"`
	Error       string         `json:"error"`
}

// PipelineLog represents a log entry from pipeline execution
type PipelineLog struct {
	ID           string        `json:"id" db:"id"`
	ExecutionID  string        `json:"execution_id" db:"execution_id"`
	Stage        PipelineStage `json:"stage" db:"stage"`
	Level        string        `json:"level" db:"level"`
	Message      string        `json:"message" db:"message"`
	Timestamp    time.Time     `json:"timestamp" db:"timestamp"`
}

// PipelineArtifact represents artifacts produced by pipeline
type PipelineArtifact struct {
	ID          string    `json:"id" db:"id"`
	ExecutionID string    `json:"execution_id" db:"execution_id"`
	Name        string    `json:"name" db:"name"`
	Type        string    `json:"type" db:"type"`
	URL         string    `json:"url" db:"url"`
	Size        int64     `json:"size" db:"size"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// PipelineService handles pipeline operations
type PipelineService struct {
	db              *database.DB
	dockerService   *docker.Service
	k8sService      *kubernetes.Service
	gitService      *git.Service
	notifyService   *notification.Service
	wsHub           *websocket.Hub
	logger          *utils.Logger
	executions      map[string]*PipelineExecution
	executionsMux   sync.RWMutex
}

// NewPipelineService creates a new pipeline service
func NewPipelineService(
	db *database.DB,
	dockerService *docker.Service,
	k8sService *kubernetes.Service,
	gitService *git.Service,
	notifyService *notification.Service,
	wsHub *websocket.Hub,
	logger *utils.Logger,
) *PipelineService {
	return &PipelineService{
		db:            db,
		dockerService: dockerService,
		k8sService:    k8sService,
		gitService:    gitService,
		notifyService: notifyService,
		wsHub:         wsHub,
		logger:        logger,
		executions:    make(map[string]*PipelineExecution),
	}
}

// CreatePipeline creates a new deployment pipeline
func (ps *PipelineService) CreatePipeline(ctx context.Context, config *PipelineConfig) (*PipelineConfig, error) {
	config.ID = utils.GenerateID()
	config.CreatedAt = time.Now()
	config.UpdatedAt = time.Now()

	query := `
		INSERT INTO pipeline_configs (
			id, project_id, name, description, git_repository, git_branch,
			build_command, test_command, dockerfile_path, kubernetes_config,
			environment, auto_deploy, notifications, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`

	configJSON, _ := json.Marshal(config.KubernetesConfig)
	notificationsJSON, _ := json.Marshal(config.Notifications)

	_, err := ps.db.ExecContext(ctx, query,
		config.ID, config.ProjectID, config.Name, config.Description,
		config.GitRepository, config.GitBranch, config.BuildCommand,
		config.TestCommand, config.DockerfilePath, configJSON,
		config.Environment, config.AutoDeploy, notificationsJSON,
		config.CreatedAt, config.UpdatedAt,
	)

	if err != nil {
		ps.logger.Error("Failed to create pipeline", "error", err)
		return nil, err
	}

	ps.logger.Info("Pipeline created successfully", "pipeline_id", config.ID)
	return config, nil
}

// GetPipeline retrieves a pipeline by ID
func (ps *PipelineService) GetPipeline(ctx context.Context, pipelineID string) (*PipelineConfig, error) {
	var config PipelineConfig
	var configJSON, notificationsJSON []byte

	query := `
		SELECT id, project_id, name, description, git_repository, git_branch,
			   build_command, test_command, dockerfile_path, kubernetes_config,
			   environment, auto_deploy, notifications, created_at, updated_at
		FROM pipeline_configs WHERE id = $1
	`

	err := ps.db.GetContext(ctx, &config, query, pipelineID)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	if len(configJSON) > 0 {
		json.Unmarshal(configJSON, &config.KubernetesConfig)
	}
	if len(notificationsJSON) > 0 {
		json.Unmarshal(notificationsJSON, &config.Notifications)
	}

	return &config, nil
}

// ExecutePipeline starts a new pipeline execution
func (ps *PipelineService) ExecutePipeline(ctx context.Context, pipelineID, userID string) (*PipelineExecution, error) {
	config, err := ps.GetPipeline(ctx, pipelineID)
	if err != nil {
		return nil, err
	}

	execution := &PipelineExecution{
		ID:           utils.GenerateID(),
		PipelineID:   pipelineID,
		ProjectID:    config.ProjectID,
		UserID:       userID,
		Status:       StatusPending,
		CurrentStage: StageClone,
		Stages:       ps.initializeStages(),
		StartedAt:    time.Now(),
		CreatedAt:    time.Now(),
		Logs:         []PipelineLog{},
		Artifacts:    []PipelineArtifact{},
	}

	// Store execution in database
	err = ps.saveExecution(ctx, execution)
	if err != nil {
		return nil, err
	}

	// Store in memory for real-time updates
	ps.executionsMux.Lock()
	ps.executions[execution.ID] = execution
	ps.executionsMux.Unlock()

	// Start pipeline execution in background
	go ps.runPipeline(ctx, execution, config)

	return execution, nil
}

// runPipeline executes the pipeline stages
func (ps *PipelineService) runPipeline(ctx context.Context, execution *PipelineExecution, config *PipelineConfig) {
	ps.updateExecutionStatus(execution, StatusRunning)
	ps.broadcastExecutionUpdate(execution)

	defer func() {
		if r := recover(); r != nil {
			ps.logger.Error("Pipeline execution panic", "execution_id", execution.ID, "error", r)
			ps.updateExecutionStatus(execution, StatusFailed)
			ps.broadcastExecutionUpdate(execution)
		}
	}()

	// Execute stages sequentially
	stages := []PipelineStage{
		StageClone, StageBuild, StageTest, StagePush, StageDeploy, StageHealthCheck,
	}

	for _, stage := range stages {
		if execution.Status == StatusFailed || execution.Status == StatusCancelled {
			break
		}

		ps.executeStage(ctx, execution, config, stage)
	}

	// Mark as complete if all stages succeeded
	if execution.Status == StatusRunning {
		ps.updateExecutionStatus(execution, StatusSuccess)
		execution.CurrentStage = StageComplete
	}

	// Calculate duration
	execution.CompletedAt = &time.Time{}
	*execution.CompletedAt = time.Now()
	execution.Duration = execution.CompletedAt.Sub(execution.StartedAt).Milliseconds()

	// Save final state
	ps.saveExecution(ctx, execution)
	ps.broadcastExecutionUpdate(execution)

	// Send notifications
	ps.sendNotifications(execution, config)

	// Clean up memory
	ps.executionsMux.Lock()
	delete(ps.executions, execution.ID)
	ps.executionsMux.Unlock()
}

// executeStage executes a single pipeline stage
func (ps *PipelineService) executeStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stage PipelineStage) {
	execution.CurrentStage = stage
	stageExecution := ps.findStageExecution(execution, stage)
	
	startTime := time.Now()
	stageExecution.StartedAt = &startTime
	stageExecution.Status = StatusRunning

	ps.addLog(execution, stage, "info", fmt.Sprintf("Starting stage: %s", stage))
	ps.broadcastExecutionUpdate(execution)

	var err error
	switch stage {
	case StageClone:
		err = ps.executeCloneStage(ctx, execution, config, stageExecution)
	case StageBuild:
		err = ps.executeBuildStage(ctx, execution, config, stageExecution)
	case StageTest:
		err = ps.executeTestStage(ctx, execution, config, stageExecution)
	case StagePush:
		err = ps.executePushStage(ctx, execution, config, stageExecution)
	case StageDeploy:
		err = ps.executeDeployStage(ctx, execution, config, stageExecution)
	case StageHealthCheck:
		err = ps.executeHealthCheckStage(ctx, execution, config, stageExecution)
	}

	// Update stage completion
	completedAt := time.Now()
	stageExecution.CompletedAt = &completedAt
	stageExecution.Duration = completedAt.Sub(*stageExecution.StartedAt).Milliseconds()

	if err != nil {
		stageExecution.Status = StatusFailed
		stageExecution.Error = err.Error()
		ps.updateExecutionStatus(execution, StatusFailed)
		ps.addLog(execution, stage, "error", fmt.Sprintf("Stage failed: %s", err.Error()))
	} else {
		stageExecution.Status = StatusSuccess
		ps.addLog(execution, stage, "info", fmt.Sprintf("Stage completed successfully: %s", stage))
	}

	ps.broadcastExecutionUpdate(execution)
}

// executeCloneStage clones the git repository
func (ps *PipelineService) executeCloneStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	commitInfo, err := ps.gitService.CloneRepository(ctx, config.GitRepository, config.GitBranch)
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	execution.CommitHash = commitInfo.Hash
	execution.CommitMessage = commitInfo.Message
	stageExecution.Output = fmt.Sprintf("Cloned repository %s at commit %s", config.GitRepository, commitInfo.Hash)

	return nil
}

// executeBuildStage builds the application
func (ps *PipelineService) executeBuildStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	imageName := fmt.Sprintf("%s:%s", config.Name, execution.CommitHash[:8])
	
	buildResult, err := ps.dockerService.BuildImage(ctx, &docker.BuildRequest{
		Name:           imageName,
		DockerfilePath: config.DockerfilePath,
		Context:        fmt.Sprintf("/tmp/builds/%s", execution.ID),
		BuildArgs:      map[string]string{},
	})

	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	stageExecution.Output = fmt.Sprintf("Built image %s (ID: %s)", imageName, buildResult.ImageID)
	
	// Store artifact
	ps.addArtifact(execution, "docker-image", imageName, buildResult.ImageID, buildResult.Size)

	return nil
}

// executeTestStage runs tests
func (ps *PipelineService) executeTestStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	if config.TestCommand == "" {
		stageExecution.Status = StatusSkipped
		stageExecution.Output = "No test command configured"
		return nil
	}

	// Run tests in container
	testResult, err := ps.dockerService.RunContainer(ctx, &docker.RunRequest{
		Image:   fmt.Sprintf("%s:%s", config.Name, execution.CommitHash[:8]),
		Command: []string{"sh", "-c", config.TestCommand},
		Env:     map[string]string{},
	})

	if err != nil {
		return fmt.Errorf("tests failed: %w", err)
	}

	stageExecution.Output = testResult.Output
	return nil
}

// executePushStage pushes the image to registry
func (ps *PipelineService) executePushStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	imageName := fmt.Sprintf("%s:%s", config.Name, execution.CommitHash[:8])
	
	err := ps.dockerService.PushImage(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to push image: %w", err)
	}

	stageExecution.Output = fmt.Sprintf("Pushed image %s to registry", imageName)
	return nil
}

// executeDeployStage deploys to Kubernetes
func (ps *PipelineService) executeDeployStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	imageName := fmt.Sprintf("%s:%s", config.Name, execution.CommitHash[:8])
	
	deployResult, err := ps.k8sService.Deploy(ctx, &kubernetes.DeployRequest{
		Name:      config.Name,
		Namespace: config.Environment,
		Image:     imageName,
		Config:    config.KubernetesConfig,
	})

	if err != nil {
		return fmt.Errorf("deployment failed: %w", err)
	}

	stageExecution.Output = fmt.Sprintf("Deployed to %s namespace: %s", config.Environment, deployResult.Status)
	return nil
}

// executeHealthCheckStage performs health check
func (ps *PipelineService) executeHealthCheckStage(ctx context.Context, execution *PipelineExecution, config *PipelineConfig, stageExecution *PipelineStageExecution) error {
	healthResult, err := ps.k8sService.HealthCheck(ctx, config.Name, config.Environment)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	stageExecution.Output = fmt.Sprintf("Health check passed: %s", healthResult.Status)
	return nil
}

// CancelExecution cancels a running pipeline execution
func (ps *PipelineService) CancelExecution(ctx context.Context, executionID string) error {
	ps.executionsMux.Lock()
	execution, exists := ps.executions[executionID]
	ps.executionsMux.Unlock()

	if !exists {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	ps.updateExecutionStatus(execution, StatusCancelled)
	ps.addLog(execution, execution.CurrentStage, "info", "Execution cancelled by user")
	ps.broadcastExecutionUpdate(execution)

	return nil
}

// GetExecution retrieves a pipeline execution
func (ps *PipelineService) GetExecution(ctx context.Context, executionID string) (*PipelineExecution, error) {
	// Check memory first
	ps.executionsMux.RLock()
	execution, exists := ps.executions[executionID]
	ps.executionsMux.RUnlock()

	if exists {
		return execution, nil
	}

	// Load from database
	return ps.loadExecution(ctx, executionID)
}

// GetExecutions retrieves pipeline executions for a project
func (ps *PipelineService) GetExecutions(ctx context.Context, projectID string, limit, offset int) ([]*PipelineExecution, error) {
	query := `
		SELECT id, pipeline_id, project_id, user_id, commit_hash, commit_message,
			   status, current_stage, started_at, completed_at, duration, created_at
		FROM pipeline_executions 
		WHERE project_id = $1 
		ORDER BY created_at DESC 
		LIMIT $2 OFFSET $3
	`

	rows, err := ps.db.QueryContext(ctx, query, projectID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var executions []*PipelineExecution
	for rows.Next() {
		var execution PipelineExecution
		err := rows.Scan(
			&execution.ID, &execution.PipelineID, &execution.ProjectID,
			&execution.UserID, &execution.CommitHash, &execution.CommitMessage,
			&execution.Status, &execution.CurrentStage, &execution.StartedAt,
			&execution.CompletedAt, &execution.Duration, &execution.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		executions = append(executions, &execution)
	}

	return executions, nil
}

// Helper methods

func (ps *PipelineService) initializeStages() []PipelineStageExecution {
	stages := []PipelineStage{
		StageClone, StageBuild, StageTest, StagePush, StageDeploy, StageHealthCheck,
	}

	var stageExecutions []PipelineStageExecution
	for _, stage := range stages {
		stageExecutions = append(stageExecutions, PipelineStageExecution{
			Stage:  stage,
			Status: StatusPending,
		})
	}

	return stageExecutions
}

func (ps *PipelineService) findStageExecution(execution *PipelineExecution, stage PipelineStage) *PipelineStageExecution {
	for i := range execution.Stages {
		if execution.Stages[i].Stage == stage {
			return &execution.Stages[i]
		}
	}
	return nil
}

func (ps *PipelineService) updateExecutionStatus(execution *PipelineExecution, status PipelineStatus) {
	execution.Status = status
}

func (ps *PipelineService) addLog(execution *PipelineExecution, stage PipelineStage, level, message string) {
	logEntry := PipelineLog{
		ID:          utils.GenerateID(),
		ExecutionID: execution.ID,
		Stage:       stage,
		Level:       level,
		Message:     message,
		Timestamp:   time.Now(),
	}

	execution.Logs = append(execution.Logs, logEntry)
}

func (ps *PipelineService) addArtifact(execution *PipelineExecution, artifactType, name, url string, size int64) {
	artifact := PipelineArtifact{
		ID:          utils.GenerateID(),
		ExecutionID: execution.ID,
		Name:        name,
		Type:        artifactType,
		URL:         url,
		Size:        size,
		CreatedAt:   time.Now(),
	}

	execution.Artifacts = append(execution.Artifacts, artifact)
}

func (ps *PipelineService) broadcastExecutionUpdate(execution *PipelineExecution) {
	if ps.wsHub != nil {
		ps.wsHub.Broadcast <- &websocket.Message{
			Type:      "pipeline_update",
			ProjectID: execution.ProjectID,
			Data:      execution,
		}
	}
}

func (ps *PipelineService) saveExecution(ctx context.Context, execution *PipelineExecution) error {
	query := `
		INSERT INTO pipeline_executions (
			id, pipeline_id, project_id, user_id, commit_hash, commit_message,
			status, current_stage, started_at, completed_at, duration, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			current_stage = EXCLUDED.current_stage,
			completed_at = EXCLUDED.completed_at,
			duration = EXCLUDED.duration
	`

	_, err := ps.db.ExecContext(ctx, query,
		execution.ID, execution.PipelineID, execution.ProjectID,
		execution.UserID, execution.CommitHash, execution.CommitMessage,
		execution.Status, execution.CurrentStage, execution.StartedAt,
		execution.CompletedAt, execution.Duration, execution.CreatedAt,
	)

	return err
}

func (ps *PipelineService) loadExecution(ctx context.Context, executionID string) (*PipelineExecution, error) {
	var execution PipelineExecution
	query := `
		SELECT id, pipeline_id, project_id, user_id, commit_hash, commit_message,
			   status, current_stage, started_at, completed_at, duration, created_at
		FROM pipeline_executions WHERE id = $1
	`

	err := ps.db.GetContext(ctx, &execution, query, executionID)
	if err != nil {
		return nil, err
	}

	// Load logs and artifacts
	execution.Logs, _ = ps.loadLogs(ctx, executionID)
	execution.Artifacts, _ = ps.loadArtifacts(ctx, executionID)

	return &execution, nil
}

func (ps *PipelineService) loadLogs(ctx context.Context, executionID string) ([]PipelineLog, error) {
	query := `
		SELECT id, execution_id, stage, level, message, timestamp
		FROM pipeline_logs WHERE execution_id = $1 ORDER BY timestamp
	`

	var logs []PipelineLog
	err := ps.db.SelectContext(ctx, &logs, query, executionID)
	return logs, err
}

func (ps *PipelineService) loadArtifacts(ctx context.Context, executionID string) ([]PipelineArtifact, error) {
	query := `
		SELECT id, execution_id, name, type, url, size, created_at
		FROM pipeline_artifacts WHERE execution_id = $1 ORDER BY created_at
	`

	var artifacts []PipelineArtifact
	err := ps.db.SelectContext(ctx, &artifacts, query, executionID)
	return artifacts, err
}

func (ps *PipelineService) sendNotifications(execution *PipelineExecution, config *PipelineConfig) {
	if ps.notifyService == nil {
		return
	}

	message := fmt.Sprintf("Pipeline %s for project %s %s", 
		config.Name, config.ProjectID, execution.Status)

	for _, channel := range config.Notifications {
		go func(ch string) {
			switch ch {
			case "email":
				ps.notifyService.SendEmail(context.Background(), &notification.EmailRequest{
					Subject: "Pipeline Execution Update",
					Body:    message,
				})
			case "slack":
				ps.notifyService.SendSlack(context.Background(), &notification.SlackRequest{
					Message: message,
				})
			case "discord":
				ps.notifyService.SendDiscord(context.Background(), &notification.DiscordRequest{
					Message: message,
				})
			}
		}(channel)
	}
}