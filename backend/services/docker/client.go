package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/sirupsen/logrus"
)

// Client représente le client Docker pour StackShip
type Client struct {
	cli    *client.Client
	logger *logrus.Logger
}

// ContainerInfo contient les informations d'un conteneur
type ContainerInfo struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Status   string            `json:"status"`
	State    string            `json:"state"`
	Created  time.Time         `json:"created"`
	Ports    []PortBinding     `json:"ports"`
	Labels   map[string]string `json:"labels"`
	Networks []string          `json:"networks"`
}

// PortBinding représente un mapping de port
type PortBinding struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
}

// ContainerConfig contient la configuration pour créer un conteneur
type ContainerConfig struct {
	Image         string            `json:"image"`
	Name          string            `json:"name"`
	Ports         []PortBinding     `json:"ports"`
	Environment   map[string]string `json:"environment"`
	Labels        map[string]string `json:"labels"`
	Networks      []string          `json:"networks"`
	RestartPolicy string            `json:"restart_policy"`
	Resources     ResourceLimits    `json:"resources"`
	WorkingDir    string            `json:"working_dir"`
	Command       []string          `json:"command"`
	Entrypoint    []string          `json:"entrypoint"`
}

// ResourceLimits définit les limites de ressources
type ResourceLimits struct {
	CPUShares  int64 `json:"cpu_shares"`
	Memory     int64 `json:"memory"`
	MemorySwap int64 `json:"memory_swap"`
}

// LogOptions configure les options de récupération des logs
type LogOptions struct {
	Follow     bool      `json:"follow"`
	Timestamps bool      `json:"timestamps"`
	Tail       string    `json:"tail"`
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
}

// NewClient crée une nouvelle instance du client Docker
func NewClient(logger *logrus.Logger) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	// Vérifier la connexion au daemon Docker
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ping docker daemon: %w", err)
	}

	return &Client{
		cli:    cli,
		logger: logger,
	}, nil
}

// Close ferme la connexion au client Docker
func (c *Client) Close() error {
	return c.cli.Close()
}

// ListContainers liste tous les conteneurs (actifs et arrêtés)
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerInfo, error) {
	options := types.ContainerListOptions{
		All: all,
	}

	containers, err := c.cli.ContainerList(ctx, options)
	if err != nil {
		c.logger.WithError(err).Error("Failed to list containers")
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var containerInfos []ContainerInfo
	for _, container := range containers {
		info := ContainerInfo{
			ID:      container.ID,
			Image:   container.Image,
			Status:  container.Status,
			State:   container.State,
			Created: time.Unix(container.Created, 0),
			Labels:  container.Labels,
		}

		// Extraire le nom (supprimer le préfixe "/")
		if len(container.Names) > 0 {
			info.Name = strings.TrimPrefix(container.Names[0], "/")
		}

		// Mapper les ports
		for _, port := range container.Ports {
			portBinding := PortBinding{
				ContainerPort: int(port.PrivatePort),
				HostPort:      int(port.PublicPort),
				Protocol:      port.Type,
			}
			info.Ports = append(info.Ports, portBinding)
		}

		// Extraire les réseaux
		for networkName := range container.NetworkSettings.Networks {
			info.Networks = append(info.Networks, networkName)
		}

		containerInfos = append(containerInfos, info)
	}

	return containerInfos, nil
}

// GetContainer récupère les informations détaillées d'un conteneur
func (c *Client) GetContainer(ctx context.Context, containerID string) (*ContainerInfo, error) {
	container, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to inspect container")
		return nil, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	info := &ContainerInfo{
		ID:      container.ID,
		Name:    strings.TrimPrefix(container.Name, "/"),
		Image:   container.Config.Image,
		Status:  container.State.Status,
		State:   container.State.Status,
		Created: container.Created,
		Labels:  container.Config.Labels,
	}

	// Mapper les ports
	if container.NetworkSettings.Ports != nil {
		for containerPort, hostBindings := range container.NetworkSettings.Ports {
			port := strings.Split(string(containerPort), "/")
			if len(port) != 2 {
				continue
			}

			for _, binding := range hostBindings {
				if binding.HostPort != "" {
					portBinding := PortBinding{
						ContainerPort: parsePort(port[0]),
						HostPort:      parsePort(binding.HostPort),
						Protocol:      port[1],
					}
					info.Ports = append(info.Ports, portBinding)
				}
			}
		}
	}

	// Extraire les réseaux
	if container.NetworkSettings.Networks != nil {
		for networkName := range container.NetworkSettings.Networks {
			info.Networks = append(info.Networks, networkName)
		}
	}

	return info, nil
}

// CreateContainer crée un nouveau conteneur selon la configuration
func (c *Client) CreateContainer(ctx context.Context, config ContainerConfig) (string, error) {
	// Préparer la configuration du conteneur
	containerConfig := &container.Config{
		Image:        config.Image,
		Labels:       config.Labels,
		WorkingDir:   config.WorkingDir,
		Cmd:          config.Command,
		Entrypoint:   config.Entrypoint,
		ExposedPorts: make(nat.PortSet),
		Env:          mapToEnvSlice(config.Environment),
	}

	// Configurer les ports exposés
	portBindings := make(nat.PortMap)
	for _, port := range config.Ports {
		containerPort := nat.Port(fmt.Sprintf("%d/%s", port.ContainerPort, port.Protocol))
		containerConfig.ExposedPorts[containerPort] = struct{}{}

		if port.HostPort > 0 {
			portBindings[containerPort] = []nat.PortBinding{
				{
					HostPort: fmt.Sprintf("%d", port.HostPort),
				},
			}
		}
	}

	// Configuration de l'hôte
	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		Resources: container.Resources{
			CPUShares: config.Resources.CPUShares,
			Memory:    config.Resources.Memory,
		},
	}

	// Politique de redémarrage
	switch config.RestartPolicy {
	case "always":
		hostConfig.RestartPolicy = container.RestartPolicy{Name: "always"}
	case "unless-stopped":
		hostConfig.RestartPolicy = container.RestartPolicy{Name: "unless-stopped"}
	case "on-failure":
		hostConfig.RestartPolicy = container.RestartPolicy{Name: "on-failure"}
	default:
		hostConfig.RestartPolicy = container.RestartPolicy{Name: "no"}
	}

	// Configuration réseau
	networkConfig := &network.NetworkingConfig{}
	if len(config.Networks) > 0 {
		networkConfig.EndpointsConfig = make(map[string]*network.EndpointSettings)
		for _, networkName := range config.Networks {
			networkConfig.EndpointsConfig[networkName] = &network.EndpointSettings{}
		}
	}

	// Créer le conteneur
	resp, err := c.cli.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		networkConfig,
		nil,
		config.Name,
	)
	if err != nil {
		c.logger.WithError(err).WithField("container_name", config.Name).Error("Failed to create container")
		return "", fmt.Errorf("failed to create container %s: %w", config.Name, err)
	}

	c.logger.WithFields(logrus.Fields{
		"container_id":   resp.ID,
		"container_name": config.Name,
		"image":          config.Image,
	}).Info("Container created successfully")

	return resp.ID, nil
}

// StartContainer démarre un conteneur
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	err := c.cli.ContainerStart(ctx, containerID, types.ContainerStartOptions{})
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to start container")
		return fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	c.logger.WithField("container_id", containerID).Info("Container started successfully")
	return nil
}

// StopContainer arrête un conteneur
func (c *Client) StopContainer(ctx context.Context, containerID string, timeout *time.Duration) error {
	var timeoutSeconds *int
	if timeout != nil {
		seconds := int(timeout.Seconds())
		timeoutSeconds = &seconds
	}

	err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: timeoutSeconds,
	})
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to stop container")
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}

	c.logger.WithField("container_id", containerID).Info("Container stopped successfully")
	return nil
}

// RestartContainer redémarre un conteneur
func (c *Client) RestartContainer(ctx context.Context, containerID string, timeout *time.Duration) error {
	var timeoutSeconds *int
	if timeout != nil {
		seconds := int(timeout.Seconds())
		timeoutSeconds = &seconds
	}

	err := c.cli.ContainerRestart(ctx, containerID, container.StopOptions{
		Timeout: timeoutSeconds,
	})
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to restart container")
		return fmt.Errorf("failed to restart container %s: %w", containerID, err)
	}

	c.logger.WithField("container_id", containerID).Info("Container restarted successfully")
	return nil
}

// RemoveContainer supprime un conteneur
func (c *Client) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	err := c.cli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		Force: force,
	})
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to remove container")
		return fmt.Errorf("failed to remove container %s: %w", containerID, err)
	}

	c.logger.WithField("container_id", containerID).Info("Container removed successfully")
	return nil
}

// GetContainerLogs récupère les logs d'un conteneur
func (c *Client) GetContainerLogs(ctx context.Context, containerID string, options LogOptions) (io.ReadCloser, error) {
	logOptions := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     options.Follow,
		Timestamps: options.Timestamps,
		Tail:       options.Tail,
	}

	if !options.Since.IsZero() {
		logOptions.Since = options.Since.Format(time.RFC3339)
	}

	if !options.Until.IsZero() {
		logOptions.Until = options.Until.Format(time.RFC3339)
	}

	logs, err := c.cli.ContainerLogs(ctx, containerID, logOptions)
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to get container logs")
		return nil, fmt.Errorf("failed to get logs for container %s: %w", containerID, err)
	}

	return logs, nil
}

// GetContainerStats récupère les statistiques d'un conteneur
func (c *Client) GetContainerStats(ctx context.Context, containerID string, stream bool) (types.ContainerStats, error) {
	stats, err := c.cli.ContainerStats(ctx, containerID, stream)
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to get container stats")
		return types.ContainerStats{}, fmt.Errorf("failed to get stats for container %s: %w", containerID, err)
	}

	return stats, nil
}

// ExecCommand exécute une commande dans un conteneur
func (c *Client) ExecCommand(ctx context.Context, containerID string, cmd []string) (string, error) {
	execConfig := types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}

	resp, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		c.logger.WithError(err).WithField("container_id", containerID).Error("Failed to create exec")
		return "", fmt.Errorf("failed to create exec for container %s: %w", containerID, err)
	}

	execResp, err := c.cli.ContainerExecAttach(ctx, resp.ID, types.ExecStartCheck{})
	if err != nil {
		c.logger.WithError(err).WithField("exec_id", resp.ID).Error("Failed to attach to exec")
		return "", fmt.Errorf("failed to attach to exec %s: %w", resp.ID, err)
	}
	defer execResp.Close()

	// Lire la sortie
	output, err := io.ReadAll(execResp.Reader)
	if err != nil {
		c.logger.WithError(err).WithField("exec_id", resp.ID).Error("Failed to read exec output")
		return "", fmt.Errorf("failed to read exec output: %w", err)
	}

	return string(output), nil
}

// Ping vérifie la connexion au daemon Docker
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		c.logger.WithError(err).Error("Failed to ping docker daemon")
		return fmt.Errorf("failed to ping docker daemon: %w", err)
	}

	return nil
}

// GetVersion récupère la version du daemon Docker
func (c *Client) GetVersion(ctx context.Context) (types.Version, error) {
	version, err := c.cli.ServerVersion(ctx)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get docker version")
		return types.Version{}, fmt.Errorf("failed to get docker version: %w", err)
	}

	return version, nil
}

// GetSystemInfo récupère les informations système de Docker
func (c *Client) GetSystemInfo(ctx context.Context) (types.Info, error) {
	info, err := c.cli.Info(ctx)
	if err != nil {
		c.logger.WithError(err).Error("Failed to get docker system info")
		return types.Info{}, fmt.Errorf("failed to get docker system info: %w", err)
	}

	return info, nil
}

// Fonctions utilitaires

// mapToEnvSlice convertit une map en slice d'environnement
func mapToEnvSlice(envMap map[string]string) []string {
	var envSlice []string
	for key, value := range envMap {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", key, value))
	}
	return envSlice
}

// parsePort convertit une chaîne de port en entier
func parsePort(portStr string) int {
	if portStr == "" {
		return 0
	}

	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

// IsContainerRunning vérifie si un conteneur est en cours d'exécution
func (c *Client) IsContainerRunning(ctx context.Context, containerID string) (bool, error) {
	container, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	return container.State.Running, nil
}

// WaitForContainer attend qu'un conteneur atteigne un état spécifique
func (c *Client) WaitForContainer(ctx context.Context, containerID string) (<-chan container.WaitResponse, <-chan error) {
	return c.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
}

// GetContainerByName trouve un conteneur par son nom
func (c *Client) GetContainerByName(ctx context.Context, name string) (*ContainerInfo, error) {
	containers, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}

	for _, container := range containers {
		if container.Name == name {
			return &container, nil
		}
	}

	return nil, fmt.Errorf("container with name %s not found", name)
}
