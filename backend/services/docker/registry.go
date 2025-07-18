package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/stackship/backend/config"
	"github.com/stackship/backend/utils"
)

// RegistryService gère les opérations sur les registres Docker
type RegistryService struct {
	client *client.Client
	config *config.Config
	logger *utils.Logger
}

// RegistryConfig configuration pour un registre Docker
type RegistryConfig struct {
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Email     string `json:"email"`
	IsDefault bool   `json:"is_default"`
}

// ImageInfo informations sur une image Docker
type ImageInfo struct {
	Repository string    `json:"repository"`
	Tag        string    `json:"tag"`
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
	PushedAt   time.Time `json:"pushed_at"`
}

// RegistryAuth structure d'authentification pour un registre
type RegistryAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Auth     string `json:"auth"`
}

// PushResult résultat d'un push vers un registre
type PushResult struct {
	ImageName string        `json:"image_name"`
	Tag       string        `json:"tag"`
	Digest    string        `json:"digest"`
	Size      int64         `json:"size"`
	Duration  time.Duration `json:"duration"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

// PullResult résultat d'un pull depuis un registre
type PullResult struct {
	ImageName string        `json:"image_name"`
	Tag       string        `json:"tag"`
	Size      int64         `json:"size"`
	Duration  time.Duration `json:"duration"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

// NewRegistryService crée une nouvelle instance du service registry
func NewRegistryService(dockerClient *client.Client, cfg *config.Config, logger *utils.Logger) *RegistryService {
	return &RegistryService{
		client: dockerClient,
		config: cfg,
		logger: logger,
	}
}

// PushImage pousse une image vers un registre Docker
func (rs *RegistryService) PushImage(ctx context.Context, imageName, tag string, registryConfig *RegistryConfig) (*PushResult, error) {
	startTime := time.Now()
	fullImageName := fmt.Sprintf("%s:%s", imageName, tag)

	rs.logger.Info("Pushing image to registry", map[string]interface{}{
		"image":    fullImageName,
		"registry": registryConfig.URL,
	})

	// Construire l'URL complète de l'image avec le registre
	registryImageName := rs.buildRegistryImageName(imageName, registryConfig.URL)
	fullRegistryImageName := fmt.Sprintf("%s:%s", registryImageName, tag)

	// Tag l'image pour le registre de destination
	err := rs.client.ImageTag(ctx, fullImageName, fullRegistryImageName)
	if err != nil {
		return &PushResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to tag image: %v", err),
		}, err
	}

	// Authentification
	authConfig := rs.buildAuthConfig(registryConfig)
	encodedAuth, err := rs.encodeAuth(authConfig)
	if err != nil {
		return &PushResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to encode auth: %v", err),
		}, err
	}

	// Push l'image
	pushOptions := types.ImagePushOptions{
		RegistryAuth: encodedAuth,
	}

	pushResponse, err := rs.client.ImagePush(ctx, fullRegistryImageName, pushOptions)
	if err != nil {
		return &PushResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to push image: %v", err),
		}, err
	}
	defer pushResponse.Close()

	// Lire la réponse du push
	digest, size, err := rs.parsePushResponse(pushResponse)
	if err != nil {
		return &PushResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to parse push response: %v", err),
		}, err
	}

	rs.logger.Info("Successfully pushed image to registry", map[string]interface{}{
		"image":    fullImageName,
		"registry": registryConfig.URL,
		"digest":   digest,
		"size":     size,
		"duration": time.Since(startTime),
	})

	return &PushResult{
		ImageName: imageName,
		Tag:       tag,
		Digest:    digest,
		Size:      size,
		Duration:  time.Since(startTime),
		Success:   true,
	}, nil
}

// PullImage tire une image depuis un registre Docker
func (rs *RegistryService) PullImage(ctx context.Context, imageName, tag string, registryConfig *RegistryConfig) (*PullResult, error) {
	startTime := time.Now()

	// Construire l'URL complète de l'image avec le registre
	registryImageName := rs.buildRegistryImageName(imageName, registryConfig.URL)
	fullRegistryImageName := fmt.Sprintf("%s:%s", registryImageName, tag)

	rs.logger.Info("Pulling image from registry", map[string]interface{}{
		"image":    fullRegistryImageName,
		"registry": registryConfig.URL,
	})

	// Authentification
	authConfig := rs.buildAuthConfig(registryConfig)
	encodedAuth, err := rs.encodeAuth(authConfig)
	if err != nil {
		return &PullResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to encode auth: %v", err),
		}, err
	}

	// Pull l'image
	pullOptions := types.ImagePullOptions{
		RegistryAuth: encodedAuth,
	}

	pullResponse, err := rs.client.ImagePull(ctx, fullRegistryImageName, pullOptions)
	if err != nil {
		return &PullResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to pull image: %v", err),
		}, err
	}
	defer pullResponse.Close()

	// Lire la réponse du pull
	size, err := rs.parsePullResponse(pullResponse)
	if err != nil {
		return &PullResult{
			ImageName: imageName,
			Tag:       tag,
			Duration:  time.Since(startTime),
			Success:   false,
			Error:     fmt.Sprintf("Failed to parse pull response: %v", err),
		}, err
	}

	rs.logger.Info("Successfully pulled image from registry", map[string]interface{}{
		"image":    fullRegistryImageName,
		"registry": registryConfig.URL,
		"size":     size,
		"duration": time.Since(startTime),
	})

	return &PullResult{
		ImageName: imageName,
		Tag:       tag,
		Size:      size,
		Duration:  time.Since(startTime),
		Success:   true,
	}, nil
}

// ListImages liste les images disponibles dans un registre
func (rs *RegistryService) ListImages(ctx context.Context, repository string, registryConfig *RegistryConfig) ([]ImageInfo, error) {
	rs.logger.Info("Listing images from registry", map[string]interface{}{
		"repository": repository,
		"registry":   registryConfig.URL,
	})

	// Pour une implémentation complète, nous aurions besoin d'utiliser l'API REST du registre
	// Cette fonction est une structure de base qui peut être étendue
	images := []ImageInfo{}

	// Obtenir la liste des images locales pour maintenant
	localImages, err := rs.client.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list local images: %v", err)
	}

	for _, img := range localImages {
		for _, tag := range img.RepoTags {
			if strings.Contains(tag, repository) {
				parts := strings.Split(tag, ":")
				if len(parts) == 2 {
					images = append(images, ImageInfo{
						Repository: parts[0],
						Tag:        parts[1],
						Size:       img.Size,
						CreatedAt:  time.Unix(img.Created, 0),
					})
				}
			}
		}
	}

	return images, nil
}

// DeleteImage supprime une image d'un registre
func (rs *RegistryService) DeleteImage(ctx context.Context, imageName, tag string, registryConfig *RegistryConfig) error {
	registryImageName := rs.buildRegistryImageName(imageName, registryConfig.URL)
	fullRegistryImageName := fmt.Sprintf("%s:%s", registryImageName, tag)

	rs.logger.Info("Deleting image from registry", map[string]interface{}{
		"image":    fullRegistryImageName,
		"registry": registryConfig.URL,
	})

	// Supprimer l'image localement
	_, err := rs.client.ImageRemove(ctx, fullRegistryImageName, types.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		return fmt.Errorf("failed to delete image: %v", err)
	}

	rs.logger.Info("Successfully deleted image", map[string]interface{}{
		"image": fullRegistryImageName,
	})

	return nil
}

// TestRegistryConnection teste la connexion à un registre
func (rs *RegistryService) TestRegistryConnection(ctx context.Context, registryConfig *RegistryConfig) error {
	rs.logger.Info("Testing registry connection", map[string]interface{}{
		"registry": registryConfig.URL,
		"username": registryConfig.Username,
	})

	authConfig := rs.buildAuthConfig(registryConfig)

	// Tester l'authentification
	_, err := rs.client.RegistryLogin(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to authenticate with registry: %v", err)
	}

	rs.logger.Info("Successfully connected to registry", map[string]interface{}{
		"registry": registryConfig.URL,
	})

	return nil
}

// GetRegistryInfo obtient les informations d'un registre
func (rs *RegistryService) GetRegistryInfo(ctx context.Context, registryConfig *RegistryConfig) (*registry.DistributionInspect, error) {
	rs.logger.Info("Getting registry info", map[string]interface{}{
		"registry": registryConfig.URL,
	})

	// Cette fonction nécessiterait une implémentation spécifique selon le type de registre
	// Pour maintenant, nous retournons une structure basique
	return &registry.DistributionInspect{
		Descriptor: registry.Descriptor{
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		},
	}, nil
}

// buildRegistryImageName construit le nom complet de l'image avec le registre
func (rs *RegistryService) buildRegistryImageName(imageName, registryURL string) string {
	if registryURL == "" || registryURL == "docker.io" {
		return imageName
	}

	// Nettoyer l'URL du registre
	registryURL = strings.TrimPrefix(registryURL, "http://")
	registryURL = strings.TrimPrefix(registryURL, "https://")
	registryURL = strings.TrimSuffix(registryURL, "/")

	return fmt.Sprintf("%s/%s", registryURL, imageName)
}

// buildAuthConfig construit la configuration d'authentification
func (rs *RegistryService) buildAuthConfig(registryConfig *RegistryConfig) types.AuthConfig {
	return types.AuthConfig{
		Username:      registryConfig.Username,
		Password:      registryConfig.Password,
		Email:         registryConfig.Email,
		ServerAddress: registryConfig.URL,
	}
}

// encodeAuth encode les informations d'authentification en base64
func (rs *RegistryService) encodeAuth(authConfig types.AuthConfig) (string, error) {
	authBytes, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal auth config: %v", err)
	}

	return base64.URLEncoding.EncodeToString(authBytes), nil
}

// parsePushResponse analyse la réponse du push pour extraire le digest et la taille
func (rs *RegistryService) parsePushResponse(response io.ReadCloser) (string, int64, error) {
	decoder := json.NewDecoder(response)
	var digest string
	var size int64

	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return "", 0, fmt.Errorf("failed to decode push response: %v", err)
		}

		// Rechercher le digest dans les événements
		if aux, ok := event["aux"].(map[string]interface{}); ok {
			if d, ok := aux["Digest"].(string); ok {
				digest = d
			}
			if s, ok := aux["Size"].(float64); ok {
				size = int64(s)
			}
		}

		// Vérifier les erreurs
		if errorDetail, ok := event["errorDetail"].(map[string]interface{}); ok {
			if message, ok := errorDetail["message"].(string); ok {
				return "", 0, fmt.Errorf("push error: %s", message)
			}
		}
	}

	return digest, size, nil
}

// parsePullResponse analyse la réponse du pull pour extraire la taille
func (rs *RegistryService) parsePullResponse(response io.ReadCloser) (int64, error) {
	decoder := json.NewDecoder(response)
	var totalSize int64

	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return 0, fmt.Errorf("failed to decode pull response: %v", err)
		}

		// Accumuler la taille des layers
		if progressDetail, ok := event["progressDetail"].(map[string]interface{}); ok {
			if current, ok := progressDetail["current"].(float64); ok {
				totalSize += int64(current)
			}
		}

		// Vérifier les erreurs
		if errorDetail, ok := event["errorDetail"].(map[string]interface{}); ok {
			if message, ok := errorDetail["message"].(string); ok {
				return 0, fmt.Errorf("pull error: %s", message)
			}
		}
	}

	return totalSize, nil
}

// GetDefaultRegistryConfig retourne la configuration du registre par défaut
func (rs *RegistryService) GetDefaultRegistryConfig() *RegistryConfig {
	return &RegistryConfig{
		URL:       rs.config.Docker.DefaultRegistry,
		Username:  rs.config.Docker.RegistryUsername,
		Password:  rs.config.Docker.RegistryPassword,
		Email:     rs.config.Docker.RegistryEmail,
		IsDefault: true,
	}
}

// ValidateRegistryConfig valide la configuration d'un registre
func (rs *RegistryService) ValidateRegistryConfig(config *RegistryConfig) error {
	if config.URL == "" {
		return fmt.Errorf("registry URL is required")
	}

	if config.Username == "" {
		return fmt.Errorf("registry username is required")
	}

	if config.Password == "" {
		return fmt.Errorf("registry password is required")
	}

	return nil
}

// CleanupUnusedImages nettoie les images inutilisées
func (rs *RegistryService) CleanupUnusedImages(ctx context.Context) error {
	rs.logger.Info("Cleaning up unused images")

	pruneFilters := map[string][]string{
		"dangling": {"true"},
	}

	report, err := rs.client.ImagesPrune(ctx, pruneFilters)
	if err != nil {
		return fmt.Errorf("failed to prune images: %v", err)
	}

	rs.logger.Info("Successfully cleaned up unused images", map[string]interface{}{
		"deleted_images":  len(report.ImagesDeleted),
		"space_reclaimed": report.SpaceReclaimed,
	})

	return nil
}
