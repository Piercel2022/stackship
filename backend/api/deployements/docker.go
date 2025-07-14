package deployments

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"stackship/backend/services/docker"
	"stackship/backend/utils"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// DockerDeploymentService gère les déploiements Docker
type DockerDeploymentService struct {
	client    *client.Client
	dockerSvc *docker.Service
	logger    *utils.Logger
}

// NewDockerDeploymentService crée un nouveau service de déploiement Docker
func NewDockerDeploymentService(dockerSvc *docker.Service, logger *utils.Logger) (*DockerDeploymentService, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &DockerDeploymentService{
		client:    cli,
		dockerSvc: dockerSvc,
		logger:    logger,
	}, nil
}

// DockerDeploymentConfig représente la configuration d'un déploiement Docker
type DockerDeploymentConfig struct {
	Image          string              `json:"image"`
	Tag            string              `json:"tag"`
	Ports          []PortMapping       `json:"ports"`
	Environment    map[string]string   `json:"environment"`
	Volumes        []VolumeMapping     `json:"volumes"`
	Networks       []string            `json:"networks"`
	Resources      ResourceLimits      `json:"resources"`
	HealthCheck    *HealthCheckConfig  `json:"health_check,omitempty"`
	RestartPolicy  string              `json:"restart_policy"`
	Labels         map[string]string   `json:"labels"`
	ExposedPorts   map[string]struct{} `json:"exposed_ports"`
	WorkingDir     string              `json:"working_dir"`
	Command        []string            `json:"command,omitempty"`
	Entrypoint     []string            `json:"entrypoint,omitempty"`
	User           string              `json:"user,omitempty"`
	NetworkMode    string              `json:"network_mode"`
	SecurityOpts   []string            `json:"security_opts,omitempty"`
	ReadOnlyRootfs bool                `json:"read_only_rootfs"`
	Tmpfs          map[string]string   `json:"tmpfs,omitempty"`
	Ulimits        []UlimitConfig      `json:"ulimits,omitempty"`
	DNS            []string            `json:"dns,omitempty"`
	DNSSearch      []string            `json:"dns_search,omitempty"`
	ExtraHosts     []string            `json:"extra_hosts,omitempty"`
	LogDriver      string              `json:"log_driver"`
	LogOpts        map[string]string   `json:"log_opts,omitempty"`
}

// PortMapping représente un mapping de port
type PortMapping struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
}

// VolumeMapping représente un mapping de volume
type VolumeMapping struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
}

// ResourceLimits représente les limites de ressources
type ResourceLimits struct {
	Memory     int64   `json:"memory"`      // en bytes
	CPUQuota   int64   `json:"cpu_quota"`   // en microsecondes
	CPUPeriod  int64   `json:"cpu_period"`  // en microsecondes
	CPUShares  int64   `json:"cpu_shares"`  // poids relatif
	CPUCount   int64   `json:"cpu_count"`   // nombre de CPUs
	CPUPercent float64 `json:"cpu_percent"` // pourcentage de CPU
}

// HealthCheckConfig représente la configuration de health check
type HealthCheckConfig struct {
	Test        []string      `json:"test"`
	Interval    time.Duration `json:"interval"`
	Timeout     time.Duration `json:"timeout"`
	Retries     int           `json:"retries"`
	StartPeriod time.Duration `json:"start_period"`
}

// UlimitConfig représente une limite ulimit
type UlimitConfig struct {
	Name string `json:"name"`
	Hard int64  `json:"hard"`
	Soft int64  `json:"soft"`
}

// DockerDeploymentResult représente le résultat d'un déploiement Docker
type DockerDeploymentResult struct {
	ContainerID   string            `json:"container_id"`
	ContainerName string            `json:"container_name"`
	ImageID       string            `json:"image_id"`
	Ports         []PortMapping     `json:"ports"`
	Status        string            `json:"status"`
	StartedAt     time.Time         `json:"started_at"`
	URLs          []string          `json:"urls"`
	Logs          []string          `json:"logs"`
	Labels        map[string]string `json:"labels"`
	NetworkMode   string            `json:"network_mode"`
	IPAddress     string            `json:"ip_address"`
}

// DeployContainer déploie un conteneur Docker
func (s *DockerDeploymentService) DeployContainer(ctx context.Context, deploymentID string, config DockerDeploymentConfig) (*DockerDeploymentResult, error) {
	s.logger.Info("Starting Docker container deployment", "deployment_id", deploymentID, "image", config.Image)

	// Préparation de la configuration du conteneur
	containerConfig := &container.Config{
		Image:        fmt.Sprintf("%s:%s", config.Image, config.Tag),
		Env:          s.buildEnvironmentVariables(config.Environment),
		ExposedPorts: s.buildExposedPorts(config.Ports),
		Labels:       s.buildLabels(config.Labels, deploymentID),
		WorkingDir:   config.WorkingDir,
		Cmd:          config.Command,
		Entrypoint:   config.Entrypoint,
		User:         config.User,
	}

	// Configuration du health check
	if config.HealthCheck != nil {
		containerConfig.Healthcheck = &container.HealthConfig{
			Test:        config.HealthCheck.Test,
			Interval:    config.HealthCheck.Interval,
			Timeout:     config.HealthCheck.Timeout,
			Retries:     config.HealthCheck.Retries,
			StartPeriod: config.HealthCheck.StartPeriod,
		}
	}

	// Configuration de l'hôte
	hostConfig := &container.HostConfig{
		PortBindings:   s.buildPortBindings(config.Ports),
		Binds:          s.buildVolumeMounts(config.Volumes),
		RestartPolicy:  s.buildRestartPolicy(config.RestartPolicy),
		Resources:      s.buildResourceConfig(config.Resources),
		NetworkMode:    container.NetworkMode(config.NetworkMode),
		SecurityOpt:    config.SecurityOpts,
		ReadonlyRootfs: config.ReadOnlyRootfs,
		Tmpfs:          config.Tmpfs,
		Ulimits:        s.buildUlimits(config.Ulimits),
		DNS:            config.DNS,
		DNSSearch:      config.DNSSearch,
		ExtraHosts:     config.ExtraHosts,
		LogConfig:      s.buildLogConfig(config.LogDriver, config.LogOpts),
	}

	// Configuration du réseau
	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: s.buildNetworkEndpoints(config.Networks),
	}

	// Nom du conteneur
	containerName := fmt.Sprintf("stackship-deployment-%s", deploymentID)

	// Création du conteneur
	resp, err := s.client.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, containerName)
	if err != nil {
		s.logger.Error("Failed to create container", "deployment_id", deploymentID, "error", err)
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Démarrage du conteneur
	if err := s.client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		s.logger.Error("Failed to start container", "deployment_id", deploymentID, "container_id", resp.ID, "error", err)
		// Tentative de suppression du conteneur en cas d'échec
		s.client.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Attente que le conteneur soit en cours d'exécution
	if err := s.waitForContainer(ctx, resp.ID, 30*time.Second); err != nil {
		s.logger.Error("Container failed to start properly", "deployment_id", deploymentID, "container_id", resp.ID, "error", err)
		return nil, fmt.Errorf("container failed to start: %w", err)
	}

	// Récupération des informations du conteneur
	containerInfo, err := s.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		s.logger.Error("Failed to inspect container", "deployment_id", deploymentID, "container_id", resp.ID, "error", err)
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Construction du résultat
	result := &DockerDeploymentResult{
		ContainerID:   resp.ID,
		ContainerName: containerName,
		ImageID:       containerInfo.Image,
		Ports:         config.Ports,
		Status:        containerInfo.State.Status,
		StartedAt:     containerInfo.State.StartedAt,
		URLs:          s.buildContainerURLs(containerInfo, config.Ports),
		Labels:        containerInfo.Config.Labels,
		NetworkMode:   string(containerInfo.HostConfig.NetworkMode),
		IPAddress:     s.getContainerIPAddress(containerInfo),
	}

	// Récupération des logs initiaux
	logs, err := s.GetContainerLogs(ctx, resp.ID, 100)
	if err != nil {
		s.logger.Warn("Failed to get initial container logs", "deployment_id", deploymentID, "container_id", resp.ID, "error", err)
	} else {
		result.Logs = logs
	}

	s.logger.Info("Container deployed successfully", "deployment_id", deploymentID, "container_id", resp.ID)
	return result, nil
}

// StopContainer arrête un conteneur
func (s *DockerDeploymentService) StopContainer(ctx context.Context, containerID string) error {
	s.logger.Info("Stopping container", "container_id", containerID)

	timeout := 30 * time.Second
	if err := s.client.ContainerStop(ctx, containerID, &timeout); err != nil {
		s.logger.Error("Failed to stop container", "container_id", containerID, "error", err)
		return fmt.Errorf("failed to stop container: %w", err)
	}

	s.logger.Info("Container stopped successfully", "container_id", containerID)
	return nil
}

// RemoveContainer supprime un conteneur
func (s *DockerDeploymentService) RemoveContainer(ctx context.Context, containerID string) error {
	s.logger.Info("Removing container", "container_id", containerID)

	// Arrêt du conteneur s'il est en cours d'exécution
	if err := s.StopContainer(ctx, containerID); err != nil {
		s.logger.Warn("Failed to stop container before removal", "container_id", containerID, "error", err)
	}

	// Suppression du conteneur
	if err := s.client.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		s.logger.Error("Failed to remove container", "container_id", containerID, "error", err)
		return fmt.Errorf("failed to remove container: %w", err)
	}

	s.logger.Info("Container removed successfully", "container_id", containerID)
	return nil
}

// RestartContainer redémarre un conteneur
func (s *DockerDeploymentService) RestartContainer(ctx context.Context, containerID string) error {
	s.logger.Info("Restarting container", "container_id", containerID)

	timeout := 30 * time.Second
	if err := s.client.ContainerRestart(ctx, containerID, &timeout); err != nil {
		s.logger.Error("Failed to restart container", "container_id", containerID, "error", err)
		return fmt.Errorf("failed to restart container: %w", err)
	}

	s.logger.Info("Container restarted successfully", "container_id", containerID)
	return nil
}

// GetContainerStatus récupère le statut d'un conteneur
func (s *DockerDeploymentService) GetContainerStatus(ctx context.Context, containerID string) (*DockerDeploymentResult, error) {
	containerInfo, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Récupération des ports mappés
	ports := make([]PortMapping, 0)
	for containerPort, hostBindings := range containerInfo.NetworkSettings.Ports {
		if len(hostBindings) > 0 {
			port := strings.Split(string(containerPort), "/")
			protocol := "tcp"
			if len(port) > 1 {
				protocol = port[1]
			}

			containerPortInt := 0
			fmt.Sscanf(port[0], "%d", &containerPortInt)

			hostPortInt := 0
			fmt.Sscanf(hostBindings[0].HostPort, "%d", &hostPortInt)

			ports = append(ports, PortMapping{
				ContainerPort: containerPortInt,
				HostPort:      hostPortInt,
				Protocol:      protocol,
			})
		}
	}

	result := &DockerDeploymentResult{
		ContainerID:   containerInfo.ID,
		ContainerName: containerInfo.Name,
		ImageID:       containerInfo.Image,
		Ports:         ports,
		Status:        containerInfo.State.Status,
		StartedAt:     containerInfo.State.StartedAt,
		URLs:          s.buildContainerURLs(containerInfo, ports),
		Labels:        containerInfo.Config.Labels,
		NetworkMode:   string(containerInfo.HostConfig.NetworkMode),
		IPAddress:     s.getContainerIPAddress(containerInfo),
	}

	// Récupération des logs récents
	logs, err := s.GetContainerLogs(ctx, containerID, 100)
	if err != nil {
		s.logger.Warn("Failed to get container logs", "container_id", containerID, "error", err)
	} else {
		result.Logs = logs
	}

	return result, nil
}

// GetContainerLogs récupère les logs d'un conteneur
func (s *DockerDeploymentService) GetContainerLogs(ctx context.Context, containerID string, tail int) ([]string, error) {
	options := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
		Timestamps: true,
	}

	reader, err := s.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}
	defer reader.Close()

	logs := make([]string, 0)
	buf := make([]byte, 8192)

	for {
		n, err := reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read logs: %w", err)
		}

		// Docker logs incluent des headers de 8 bytes, on les ignore
		if n > 8 {
			logLine := string(buf[8:n])
			lines := strings.Split(strings.TrimSpace(logLine), "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					logs = append(logs, line)
				}
			}
		}
	}

	return logs, nil
}

// PullImage tire une image Docker
func (s *DockerDeploymentService) PullImage(ctx context.Context, image string) error {
	s.logger.Info("Pulling Docker image", "image", image)

	reader, err := s.client.ImagePull(ctx, image, types.ImagePullOptions{})
	if err != nil {
		s.logger.Error("Failed to pull image", "image", image, "error", err)
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Lecture de la sortie du pull (optionnel, pour le logging)
	_, err = io.ReadAll(reader)
	if err != nil {
		s.logger.Warn("Failed to read pull output", "image", image, "error", err)
	}

	s.logger.Info("Image pulled successfully", "image", image)
	return nil
}

// ListContainers liste les conteneurs
func (s *DockerDeploymentService) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	options := types.ContainerListOptions{
		All: all,
	}

	containers, err := s.client.ContainerList(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	return containers, nil
}

// buildEnvironmentVariables construit les variables d'environnement
func (s *DockerDeploymentService) buildEnvironmentVariables(env map[string]string) []string {
	envVars := make([]string, 0, len(env))
	for key, value := range env {
		envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
	}
	return envVars
}

// buildExposedPorts construit les ports exposés
func (s *DockerDeploymentService) buildExposedPorts(ports []PortMapping) nat.PortSet {
	exposedPorts := make(nat.PortSet)
	for _, port := range ports {
		portStr := fmt.Sprintf("%d/%s", port.ContainerPort, port.Protocol)
		exposedPorts[nat.Port(portStr)] = struct{}{}
	}
	return exposedPorts
}

// buildPortBindings construit les bindings de ports
func (s *DockerDeploymentService) buildPortBindings(ports []PortMapping) nat.PortMap {
	portBindings := make(nat.PortMap)
	for _, port := range ports {
		containerPort := nat.Port(fmt.Sprintf("%d/%s", port.ContainerPort, port.Protocol))
		hostBinding := nat.PortBinding{
			HostIP:   "0.0.0.0",
			HostPort: fmt.Sprintf("%d", port.HostPort),
		}
		portBindings[containerPort] = []nat.PortBinding{hostBinding}
	}
	return portBindings
}

// buildVolumeMounts construit les montages de volumes
func (s *DockerDeploymentService) buildVolumeMounts(volumes []VolumeMapping) []string {
	binds := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		bind := fmt.Sprintf("%s:%s", volume.HostPath, volume.ContainerPath)
		if volume.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}
	return binds
}

// buildRestartPolicy construit la politique de redémarrage
func (s *DockerDeploymentService) buildRestartPolicy(policy string) container.RestartPolicy {
	switch policy {
	case "no":
		return container.RestartPolicy{Name: "no"}
	case "always":
		return container.RestartPolicy{Name: "always"}
	case "unless-stopped":
		return container.RestartPolicy{Name: "unless-stopped"}
	case "on-failure":
		return container.RestartPolicy{Name: "on-failure", MaximumRetryCount: 3}
	default:
		return container.RestartPolicy{Name: "no"}
	}
}

// buildResourceConfig construit la configuration des ressources
func (s *DockerDeploymentService) buildResourceConfig(resources ResourceLimits) container.Resources {
	return container.Resources{
		Memory:    resources.Memory,
		CPUQuota:  resources.CPUQuota,
		CPUPeriod: resources.CPUPeriod,
		CPUShares: resources.CPUShares,
		NanoCPUs:  int64(resources.CPUPercent * 1e9 / 100),
	}
}

// buildUlimits construit les limites ulimit
func (s *DockerDeploymentService) buildUlimits(ulimits []UlimitConfig) []*container.Ulimit {
	limits := make([]*container.Ulimit, 0, len(ulimits))
	for _, ulimit := range ulimits {
		limits = append(limits, &container.Ulimit{
			Name: ulimit.Name,
			Hard: ulimit.Hard,
			Soft: ulimit.Soft,
		})
	}
	return limits
}

// buildLogConfig construit la configuration de logging
func (s *DockerDeploymentService) buildLogConfig(driver string, opts map[string]string) container.LogConfig {
	if driver == "" {
		driver = "json-file"
	}

	logConfig := container.LogConfig{
		Type:   driver,
		Config: make(map[string]string),
	}

	// Configuration par défaut pour json-file
	if driver == "json-file" {
		logConfig.Config["max-size"] = "10m"
		logConfig.Config["max-file"] = "3"
	}

	// Ajout des options personnalisées
	for key, value := range opts {
		logConfig.Config[key] = value
	}

	return logConfig
}

// buildLabels construit les labels du conteneur
func (s *DockerDeploymentService) buildLabels(labels map[string]string, deploymentID string) map[string]string {
	containerLabels := make(map[string]string)

	// Labels par défaut
	containerLabels["stackship.deployment.id"] = deploymentID
	containerLabels["stackship.managed"] = "true"
	containerLabels["stackship.created"] = time.Now().Format(time.RFC3339)

	// Ajout des labels personnalisés
	for key, value := range labels {
		containerLabels[key] = value
	}

	return containerLabels
}

// buildNetworkEndpoints construit les endpoints réseau
func (s *DockerDeploymentService) buildNetworkEndpoints(networks []string) map[string]*network.EndpointSettings {
	endpoints := make(map[string]*network.EndpointSettings)
	for _, networkName := range networks {
		endpoints[networkName] = &network.EndpointSettings{}
	}
	return endpoints
}

// buildContainerURLs construit les URLs d'accès au conteneur
func (s *DockerDeploymentService) buildContainerURLs(containerInfo types.ContainerJSON, ports []PortMapping) []string {
	urls := make([]string, 0)

	for _, port := range ports {
		if port.HostPort > 0 {
			url := fmt.Sprintf("http://localhost:%d", port.HostPort)
			urls = append(urls, url)
		}
	}

	return urls
}

// getContainerIPAddress récupère l'adresse IP du conteneur
func (s *DockerDeploymentService) getContainerIPAddress(containerInfo types.ContainerJSON) string {
	if containerInfo.NetworkSettings.IPAddress != "" {
		return containerInfo.NetworkSettings.IPAddress
	}

	// Cherche dans les réseaux personnalisés
	for _, network := range containerInfo.NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress
		}
	}

	return ""
}

// waitForContainer attend que le conteneur soit en cours d'exécution
func (s *DockerDeploymentService) waitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for container to start")
		case <-ticker.C:
			containerInfo, err := s.client.ContainerInspect(ctx, containerID)
			if err != nil {
				return fmt.Errorf("failed to inspect container: %w", err)
			}

			if containerInfo.State.Running {
				return nil
			}

			if containerInfo.State.ExitCode != 0 {
				return fmt.Errorf("container exited with code %d", containerInfo.State.ExitCode)
			}
		}
	}
}

// Close ferme les connexions
func (s *DockerDeploymentService) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
