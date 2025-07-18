package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/sirupsen/logrus"
)

// BuildOptions contient les options pour la construction d'une image
type BuildOptions struct {
	ProjectID    string            `json:"project_id"`
	DeploymentID string            `json:"deployment_id"`
	ImageName    string            `json:"image_name"`
	ImageTag     string            `json:"image_tag"`
	Context      string            `json:"context"`
	Dockerfile   string            `json:"dockerfile"`
	BuildArgs    map[string]string `json:"build_args"`
	Labels       map[string]string `json:"labels"`
	Target       string            `json:"target"`
	NoCache      bool              `json:"no_cache"`
	PullParent   bool              `json:"pull_parent"`
	Squash       bool              `json:"squash"`
	Platform     string            `json:"platform"`
	NetworkMode  string            `json:"network_mode"`
	CacheFrom    []string          `json:"cache_from"`
	Secrets      map[string]string `json:"secrets"`
	SSH          map[string]string `json:"ssh"`
}

// BuildResult contient les résultats de la construction
type BuildResult struct {
	ImageID      string            `json:"image_id"`
	ImageName    string            `json:"image_name"`
	ImageTag     string            `json:"image_tag"`
	Size         int64             `json:"size"`
	BuildTime    time.Duration     `json:"build_time"`
	Layers       []string          `json:"layers"`
	Labels       map[string]string `json:"labels"`
	Success      bool              `json:"success"`
	ErrorMessage string            `json:"error_message,omitempty"`
	BuildLogs    []string          `json:"build_logs"`
	Metrics      *BuildMetrics     `json:"metrics"`
}

// BuildMetrics contient les métriques de construction
type BuildMetrics struct {
	StartTime       time.Time     `json:"start_time"`
	EndTime         time.Time     `json:"end_time"`
	Duration        time.Duration `json:"duration"`
	CacheHits       int           `json:"cache_hits"`
	CacheMisses     int           `json:"cache_misses"`
	LayersBuilt     int           `json:"layers_built"`
	BytesDownloaded int64         `json:"bytes_downloaded"`
	BytesUploaded   int64         `json:"bytes_uploaded"`
	CPUUsage        float64       `json:"cpu_usage"`
	MemoryUsage     int64         `json:"memory_usage"`
}

// BuildProgress représente le progrès de la construction
type BuildProgress struct {
	Step       int       `json:"step"`
	TotalSteps int       `json:"total_steps"`
	Status     string    `json:"status"`
	Progress   float64   `json:"progress"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
	StreamType string    `json:"stream_type"`
}

// BuildContext contient le contexte de construction
type BuildContext struct {
	ProjectID    string
	DeploymentID string
	UserID       string
	WorkspaceID  string
	BuildID      string
	Logger       *logrus.Logger
	Cancel       context.CancelFunc
}

// Builder interface pour la construction d'images Docker
type Builder interface {
	Build(ctx context.Context, options *BuildOptions) (*BuildResult, error)
	BuildWithProgress(ctx context.Context, options *BuildOptions, progressChan chan<- *BuildProgress) (*BuildResult, error)
	CancelBuild(buildID string) error
	GetBuildHistory(projectID string, limit int) ([]*BuildResult, error)
	GetBuildLogs(buildID string) ([]string, error)
	CleanupBuild(buildID string) error
	ValidateDockerfile(dockerfile string) error
	EstimateBuildTime(options *BuildOptions) (time.Duration, error)
	GetBuildMetrics(buildID string) (*BuildMetrics, error)
}

// DockerBuilder implémente l'interface Builder
type DockerBuilder struct {
	client       *client.Client
	registry     *Registry
	logger       *logrus.Logger
	config       *BuilderConfig
	activeBuilds map[string]*BuildContext
	buildHistory map[string]*BuildResult
	mutex        sync.RWMutex
	metrics      *BuildMetrics
}

// BuilderConfig contient la configuration du builder
type BuilderConfig struct {
	MaxConcurrentBuilds int           `json:"max_concurrent_builds"`
	BuildTimeout        time.Duration `json:"build_timeout"`
	RetryAttempts       int           `json:"retry_attempts"`
	RetryDelay          time.Duration `json:"retry_delay"`
	CleanupEnabled      bool          `json:"cleanup_enabled"`
	CleanupInterval     time.Duration `json:"cleanup_interval"`
	CacheEnabled        bool          `json:"cache_enabled"`
	CacheSize           int64         `json:"cache_size"`
	LogLevel            string        `json:"log_level"`
	MetricsEnabled      bool          `json:"metrics_enabled"`
	SecurityScanEnabled bool          `json:"security_scan_enabled"`
	MultiStageEnabled   bool          `json:"multi_stage_enabled"`
	BuildKitEnabled     bool          `json:"buildkit_enabled"`
}

// NewDockerBuilder crée une nouvelle instance de DockerBuilder
func NewDockerBuilder(dockerClient *client.Client, registry *Registry, config *BuilderConfig) *DockerBuilder {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

	if config.LogLevel != "" {
		if level, err := logrus.ParseLevel(config.LogLevel); err == nil {
			logger.SetLevel(level)
		}
	}

	builder := &DockerBuilder{
		client:       dockerClient,
		registry:     registry,
		logger:       logger,
		config:       config,
		activeBuilds: make(map[string]*BuildContext),
		buildHistory: make(map[string]*BuildResult),
		metrics:      &BuildMetrics{},
	}

	// Démarrer le nettoyage automatique
	if config.CleanupEnabled {
		go builder.startCleanupRoutine()
	}

	return builder
}

// Build construit une image Docker
func (b *DockerBuilder) Build(ctx context.Context, options *BuildOptions) (*BuildResult, error) {
	buildID := fmt.Sprintf("%s-%s-%d", options.ProjectID, options.DeploymentID, time.Now().Unix())

	b.logger.WithFields(logrus.Fields{
		"build_id":      buildID,
		"project_id":    options.ProjectID,
		"deployment_id": options.DeploymentID,
		"image_name":    options.ImageName,
		"image_tag":     options.ImageTag,
	}).Info("Starting Docker build")

	// Créer le contexte de construction
	buildCtx, cancel := context.WithTimeout(ctx, b.config.BuildTimeout)
	defer cancel()

	buildContext := &BuildContext{
		ProjectID:    options.ProjectID,
		DeploymentID: options.DeploymentID,
		BuildID:      buildID,
		Logger:       b.logger,
		Cancel:       cancel,
	}

	// Enregistrer la construction active
	b.mutex.Lock()
	b.activeBuilds[buildID] = buildContext
	b.mutex.Unlock()

	// Nettoyer à la fin
	defer func() {
		b.mutex.Lock()
		delete(b.activeBuilds, buildID)
		b.mutex.Unlock()
	}()

	// Valider le Dockerfile
	if err := b.ValidateDockerfile(options.Dockerfile); err != nil {
		return nil, fmt.Errorf("dockerfile validation failed: %v", err)
	}

	// Préparer le contexte de construction
	buildContext, err := b.prepareBuildContext(options)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare build context: %v", err)
	}
	defer buildContext.Close()

	// Construire l'image
	result, err := b.buildImage(buildCtx, options, buildContext)
	if err != nil {
		b.logger.WithFields(logrus.Fields{
			"build_id": buildID,
			"error":    err.Error(),
		}).Error("Build failed")

		result = &BuildResult{
			ImageName:    options.ImageName,
			ImageTag:     options.ImageTag,
			Success:      false,
			ErrorMessage: err.Error(),
			BuildTime:    time.Since(b.metrics.StartTime),
		}
	}

	// Enregistrer dans l'historique
	b.mutex.Lock()
	b.buildHistory[buildID] = result
	b.mutex.Unlock()

	return result, err
}

// BuildWithProgress construit une image avec suivi du progrès
func (b *DockerBuilder) BuildWithProgress(ctx context.Context, options *BuildOptions, progressChan chan<- *BuildProgress) (*BuildResult, error) {
	buildID := fmt.Sprintf("%s-%s-%d", options.ProjectID, options.DeploymentID, time.Now().Unix())

	// Créer le contexte de construction
	buildCtx, cancel := context.WithTimeout(ctx, b.config.BuildTimeout)
	defer cancel()

	buildContext := &BuildContext{
		ProjectID:    options.ProjectID,
		DeploymentID: options.DeploymentID,
		BuildID:      buildID,
		Logger:       b.logger,
		Cancel:       cancel,
	}

	// Enregistrer la construction active
	b.mutex.Lock()
	b.activeBuilds[buildID] = buildContext
	b.mutex.Unlock()

	defer func() {
		b.mutex.Lock()
		delete(b.activeBuilds, buildID)
		b.mutex.Unlock()
		close(progressChan)
	}()

	// Valider le Dockerfile
	if err := b.ValidateDockerfile(options.Dockerfile); err != nil {
		return nil, fmt.Errorf("dockerfile validation failed: %v", err)
	}

	// Préparer le contexte de construction
	buildContextReader, err := b.prepareBuildContext(options)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare build context: %v", err)
	}
	defer buildContextReader.Close()

	// Construire l'image avec suivi du progrès
	result, err := b.buildImageWithProgress(buildCtx, options, buildContextReader, progressChan)
	if err != nil {
		result = &BuildResult{
			ImageName:    options.ImageName,
			ImageTag:     options.ImageTag,
			Success:      false,
			ErrorMessage: err.Error(),
			BuildTime:    time.Since(b.metrics.StartTime),
		}
	}

	// Enregistrer dans l'historique
	b.mutex.Lock()
	b.buildHistory[buildID] = result
	b.mutex.Unlock()

	return result, err
}

// prepareBuildContext prépare le contexte de construction
func (b *DockerBuilder) prepareBuildContext(options *BuildOptions) (io.ReadCloser, error) {
	// Vérifier si le contexte existe
	if _, err := os.Stat(options.Context); os.IsNotExist(err) {
		return nil, fmt.Errorf("build context directory does not exist: %s", options.Context)
	}

	// Créer l'archive tar du contexte
	buildContext, err := archive.TarWithOptions(options.Context, &archive.TarOptions{
		Compression: archive.Gzip,
		ExcludePatterns: []string{
			".git",
			".gitignore",
			"node_modules",
			"*.tmp",
			"*.log",
			".DS_Store",
			"Thumbs.db",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create build context archive: %v", err)
	}

	return buildContext, nil
}

// buildImage construit l'image Docker
func (b *DockerBuilder) buildImage(ctx context.Context, options *BuildOptions, buildContext io.ReadCloser) (*BuildResult, error) {
	b.metrics.StartTime = time.Now()

	// Préparer les options de construction
	buildOpts := types.ImageBuildOptions{
		Dockerfile:  options.Dockerfile,
		Tags:        []string{fmt.Sprintf("%s:%s", options.ImageName, options.ImageTag)},
		BuildArgs:   options.BuildArgs,
		Labels:      options.Labels,
		Target:      options.Target,
		NoCache:     options.NoCache,
		PullParent:  options.PullParent,
		Squash:      options.Squash,
		Platform:    options.Platform,
		NetworkMode: options.NetworkMode,
		CacheFrom:   options.CacheFrom,
		Remove:      true,
		ForceRemove: true,
		Isolation:   container.IsolationDefault,
	}

	// Construire l'image
	response, err := b.client.ImageBuild(ctx, buildContext, buildOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %v", err)
	}
	defer response.Body.Close()

	// Traiter la réponse
	var buildLogs []string
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		buildLogs = append(buildLogs, line)

		// Parser les messages JSON
		var msg jsonmessage.JSONMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			if msg.Error != nil {
				return nil, fmt.Errorf("build error: %v", msg.Error)
			}
			if msg.Stream != "" {
				b.logger.Debug(strings.TrimSpace(msg.Stream))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading build output: %v", err)
	}

	b.metrics.EndTime = time.Now()
	b.metrics.Duration = b.metrics.EndTime.Sub(b.metrics.StartTime)

	// Obtenir les informations de l'image
	imageInfo, err := b.getImageInfo(fmt.Sprintf("%s:%s", options.ImageName, options.ImageTag))
	if err != nil {
		return nil, fmt.Errorf("failed to get image info: %v", err)
	}

	return &BuildResult{
		ImageID:   imageInfo.ID,
		ImageName: options.ImageName,
		ImageTag:  options.ImageTag,
		Size:      imageInfo.Size,
		BuildTime: b.metrics.Duration,
		Layers:    imageInfo.RootFS.Layers,
		Labels:    imageInfo.Config.Labels,
		Success:   true,
		BuildLogs: buildLogs,
		Metrics:   b.metrics,
	}, nil
}

// buildImageWithProgress construit l'image avec suivi du progrès
func (b *DockerBuilder) buildImageWithProgress(ctx context.Context, options *BuildOptions, buildContext io.ReadCloser, progressChan chan<- *BuildProgress) (*BuildResult, error) {
	b.metrics.StartTime = time.Now()

	// Préparer les options de construction
	buildOpts := types.ImageBuildOptions{
		Dockerfile:  options.Dockerfile,
		Tags:        []string{fmt.Sprintf("%s:%s", options.ImageName, options.ImageTag)},
		BuildArgs:   options.BuildArgs,
		Labels:      options.Labels,
		Target:      options.Target,
		NoCache:     options.NoCache,
		PullParent:  options.PullParent,
		Squash:      options.Squash,
		Platform:    options.Platform,
		NetworkMode: options.NetworkMode,
		CacheFrom:   options.CacheFrom,
		Remove:      true,
		ForceRemove: true,
		Isolation:   container.IsolationDefault,
	}

	// Construire l'image
	response, err := b.client.ImageBuild(ctx, buildContext, buildOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %v", err)
	}
	defer response.Body.Close()

	// Traiter la réponse avec suivi du progrès
	var buildLogs []string
	step := 0
	totalSteps := 0

	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		buildLogs = append(buildLogs, line)

		// Parser les messages JSON
		var msg jsonmessage.JSONMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			if msg.Error != nil {
				return nil, fmt.Errorf("build error: %v", msg.Error)
			}

			if msg.Stream != "" {
				streamText := strings.TrimSpace(msg.Stream)
				b.logger.Debug(streamText)

				// Détecter les étapes
				if strings.HasPrefix(streamText, "Step ") {
					step++
					if totalSteps == 0 {
						// Estimer le nombre total d'étapes
						totalSteps = b.estimateTotalSteps(options.Dockerfile)
					}
				}

				// Envoyer le progrès
				progress := &BuildProgress{
					Step:       step,
					TotalSteps: totalSteps,
					Status:     "building",
					Progress:   float64(step) / float64(totalSteps) * 100,
					Message:    streamText,
					Timestamp:  time.Now(),
					StreamType: "stdout",
				}

				select {
				case progressChan <- progress:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading build output: %v", err)
	}

	b.metrics.EndTime = time.Now()
	b.metrics.Duration = b.metrics.EndTime.Sub(b.metrics.StartTime)

	// Obtenir les informations de l'image
	imageInfo, err := b.getImageInfo(fmt.Sprintf("%s:%s", options.ImageName, options.ImageTag))
	if err != nil {
		return nil, fmt.Errorf("failed to get image info: %v", err)
	}

	// Envoyer le progrès final
	finalProgress := &BuildProgress{
		Step:       totalSteps,
		TotalSteps: totalSteps,
		Status:     "completed",
		Progress:   100,
		Message:    "Build completed successfully",
		Timestamp:  time.Now(),
		StreamType: "stdout",
	}

	select {
	case progressChan <- finalProgress:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &BuildResult{
		ImageID:   imageInfo.ID,
		ImageName: options.ImageName,
		ImageTag:  options.ImageTag,
		Size:      imageInfo.Size,
		BuildTime: b.metrics.Duration,
		Layers:    imageInfo.RootFS.Layers,
		Labels:    imageInfo.Config.Labels,
		Success:   true,
		BuildLogs: buildLogs,
		Metrics:   b.metrics,
	}, nil
}

// getImageInfo récupère les informations d'une image
func (b *DockerBuilder) getImageInfo(imageRef string) (*types.ImageInspect, error) {
	inspect, _, err := b.client.ImageInspectWithRaw(context.Background(), imageRef)
	if err != nil {
		return nil, err
	}
	return &inspect, nil
}

// estimateTotalSteps estime le nombre total d'étapes dans un Dockerfile
func (b *DockerBuilder) estimateTotalSteps(dockerfile string) int {
	// Lecture du Dockerfile et comptage des instructions
	file, err := os.Open(dockerfile)
	if err != nil {
		return 10 // Valeur par défaut
	}
	defer file.Close()

	steps := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			// Vérifier si c'est une instruction Docker
			instructions := []string{"FROM", "RUN", "CMD", "LABEL", "EXPOSE", "ENV", "ADD", "COPY", "ENTRYPOINT", "VOLUME", "USER", "WORKDIR", "ARG", "ONBUILD", "STOPSIGNAL", "HEALTHCHECK", "SHELL"}
			for _, instr := range instructions {
				if strings.HasPrefix(strings.ToUpper(line), instr) {
					steps++
					break
				}
			}
		}
	}

	if steps == 0 {
		return 10 // Valeur par défaut
	}
	return steps
}

// CancelBuild annule une construction en cours
func (b *DockerBuilder) CancelBuild(buildID string) error {
	b.mutex.RLock()
	buildContext, exists := b.activeBuilds[buildID]
	b.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("build %s not found", buildID)
	}

	buildContext.Cancel()

	b.logger.WithField("build_id", buildID).Info("Build cancelled")
	return nil
}

// GetBuildHistory récupère l'historique des constructions
func (b *DockerBuilder) GetBuildHistory(projectID string, limit int) ([]*BuildResult, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	var results []*BuildResult
	count := 0

	for _, result := range b.buildHistory {
		if projectID == "" || strings.Contains(result.ImageName, projectID) {
			results = append(results, result)
			count++
			if limit > 0 && count >= limit {
				break
			}
		}
	}

	return results, nil
}

// GetBuildLogs récupère les logs d'une construction
func (b *DockerBuilder) GetBuildLogs(buildID string) ([]string, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	result, exists := b.buildHistory[buildID]
	if !exists {
		return nil, fmt.Errorf("build %s not found", buildID)
	}

	return result.BuildLogs, nil
}

// CleanupBuild nettoie les ressources d'une construction
func (b *DockerBuilder) CleanupBuild(buildID string) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// Supprimer de l'historique
	delete(b.buildHistory, buildID)

	b.logger.WithField("build_id", buildID).Info("Build cleaned up")
	return nil
}

// ValidateDockerfile valide un Dockerfile
func (b *DockerBuilder) ValidateDockerfile(dockerfile string) error {
	// Vérifier si le fichier existe
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return fmt.Errorf("dockerfile does not exist: %s", dockerfile)
	}

	// Lire le contenu du Dockerfile
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		return fmt.Errorf("failed to read dockerfile: %v", err)
	}

	// Vérifications basiques
	contentStr := string(content)
	if !strings.Contains(strings.ToUpper(contentStr), "FROM") {
		return fmt.Errorf("dockerfile must contain at least one FROM instruction")
	}

	// Vérifier les instructions dangereuses
	dangerousInstructions := []string{
		"--privileged",
		"--cap-add=SYS_ADMIN",
		"--security-opt apparmor:unconfined",
	}

	for _, dangerous := range dangerousInstructions {
		if strings.Contains(contentStr, dangerous) {
			return fmt.Errorf("dockerfile contains dangerous instruction: %s", dangerous)
		}
	}

	return nil
}

// EstimateBuildTime estime le temps de construction
func (b *DockerBuilder) EstimateBuildTime(options *BuildOptions) (time.Duration, error) {
	// Analyse basique du Dockerfile pour estimer le temps
	baseTime := 30 * time.Second

	// Facteurs qui augmentent le temps
	if options.NoCache {
		baseTime *= 2
	}

	if options.PullParent {
		baseTime += 60 * time.Second
	}

	if len(options.BuildArgs) > 0 {
		baseTime += time.Duration(len(options.BuildArgs)) * 10 * time.Second
	}

	return baseTime, nil
}

// GetBuildMetrics récupère les métriques d'une construction
func (b *DockerBuilder) GetBuildMetrics(buildID string) (*BuildMetrics, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	result, exists := b.buildHistory[buildID]
	if !exists {
		return nil, fmt.Errorf("build %s not found", buildID)
	}

	return result.Metrics, nil
}

// startCleanupRoutine démarre la routine de nettoyage automatique
func (b *DockerBuilder) startCleanupRoutine() {
	ticker := time.NewTicker(b.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.performCleanup()
		}
	}
}

// performCleanup effectue le nettoyage des ressources
func (b *DockerBuilder) performCleanup() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	now := time.Now()

	// Nettoyer l'historique des constructions anciennes
	for buildID, result := range b.buildHistory {
		if now.Sub(result.Metrics.EndTime) > 24*time.Hour {
			delete(b.buildHistory, buildID)
		}
	}

	// Nettoyer les images Docker non utilisées
	if b.config.CleanupEnabled {
		b.client.ImagesPrune(context.Background(), types.ImagesPruneOptions{
			DanglingOnly: true,
		})
	}

	b.logger.Info("Cleanup completed")
}

// GetActiveBuilds retourne les constructions actives
func (b *DockerBuilder) GetActiveBuilds() map[string]*BuildContext {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	// Créer une copie pour éviter les modifications concurrentes
	activeBuilds := make(map[string]*BuildContext)
	for k, v := range b.activeBuilds {
		activeBuilds[k] = v
	}

	return activeBuilds
}

// GetStats retourne les statistiques du builder
func (b *DockerBuilder) GetStats() map[string]interface{} {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return map[string]interface{}{
		"active_builds":         len(b.activeBuilds),
		"total_builds":          len(b.buildHistory),
		"max_concurrent":        b.config.MaxConcurrentBuilds,
		"cache_enabled":         b.config.CacheEnabled,
		"cleanup_enabled":       b.config.CleanupEnabled,
		"buildkit_enabled":      b.config.BuildKitEnabled,
		"security_scan_enabled": b.config.SecurityScanEnabled,
	}
}
