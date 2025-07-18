package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stackship/backend/config"
	"github.com/stackship/backend/utils"
)

// CloneService gère le clonage et la synchronisation des dépôts Git
type CloneService struct {
	config    *config.Config
	logger    *utils.Logger
	workDir   string
	gitClient *GitClient
}

// CloneOptions définit les options pour le clonage
type CloneOptions struct {
	URL           string
	Branch        string
	Tag           string
	Depth         int
	Recursive     bool
	Token         string
	Username      string
	Password      string
	SSHKey        string
	SSHKeyPath    string
	Timeout       time.Duration
	RetryCount    int
	CleanWorkDir  bool
}

// CloneResult contient les informations du résultat du clonage
type CloneResult struct {
	ProjectPath   string
	CommitHash    string
	Branch        string
	Tag           string
	Author        string
	Message       string
	Timestamp     time.Time
	Files         []string
	Size          int64
	Success       bool
	Error         error
}

// NewCloneService crée une nouvelle instance du service de clonage
func NewCloneService(config *config.Config, logger *utils.Logger, gitClient *GitClient) *CloneService {
	workDir := config.GetString("git.work_dir", "/tmp/stackship/repos")
	
	// Créer le répertoire de travail s'il n'existe pas
	if err := os.MkdirAll(workDir, 0755); err != nil {
		logger.Error("Failed to create work directory", "error", err, "path", workDir)
	}

	return &CloneService{
		config:    config,
		logger:    logger,
		workDir:   workDir,
		gitClient: gitClient,
	}
}

// Clone clone un dépôt Git avec les options spécifiées
func (cs *CloneService) Clone(ctx context.Context, projectID string, options *CloneOptions) (*CloneResult, error) {
	cs.logger.Info("Starting git clone", "projectID", projectID, "url", options.URL)

	// Validation des options
	if err := cs.validateCloneOptions(options); err != nil {
		return nil, fmt.Errorf("invalid clone options: %w", err)
	}

	// Préparer le répertoire de destination
	destPath := filepath.Join(cs.workDir, projectID)
	if options.CleanWorkDir {
		if err := cs.cleanDirectory(destPath); err != nil {
			cs.logger.Warn("Failed to clean work directory", "error", err, "path", destPath)
		}
	}

	// Configurer les credentials
	if err := cs.setupCredentials(options); err != nil {
		return nil, fmt.Errorf("failed to setup credentials: %w", err)
	}

	// Exécuter le clonage avec retry
	var result *CloneResult
	var err error
	
	for attempt := 0; attempt <= options.RetryCount; attempt++ {
		if attempt > 0 {
			cs.logger.Info("Retrying git clone", "attempt", attempt, "projectID", projectID)
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		result, err = cs.performClone(ctx, projectID, destPath, options)
		if err == nil {
			break
		}

		if attempt == options.RetryCount {
			cs.logger.Error("Git clone failed after retries", "projectID", projectID, "error", err)
			return nil, fmt.Errorf("git clone failed after %d retries: %w", options.RetryCount, err)
		}
	}

	// Post-traitement
	if result != nil && result.Success {
		if err := cs.postCloneProcessing(result); err != nil {
			cs.logger.Warn("Post-clone processing failed", "error", err, "projectID", projectID)
		}
	}

	cs.logger.Info("Git clone completed", "projectID", projectID, "success", result.Success)
	return result, nil
}

// performClone effectue le clonage réel
func (cs *CloneService) performClone(ctx context.Context, projectID, destPath string, options *CloneOptions) (*CloneResult, error) {
	startTime := time.Now()
	
	// Construire la commande git clone
	args := []string{"clone"}
	
	if options.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", options.Depth))
	}
	
	if options.Recursive {
		args = append(args, "--recursive")
	}
	
	if options.Branch != "" {
		args = append(args, "--branch", options.Branch)
	}
	
	args = append(args, options.URL, destPath)

	// Créer un contexte avec timeout
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	// Exécuter la commande
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cs.workDir
	
	// Configurer les variables d'environnement
	cmd.Env = os.Environ()
	if options.Token != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_TOKEN=%s", options.Token))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &CloneResult{
			ProjectPath: destPath,
			Success:     false,
			Error:       fmt.Errorf("git clone failed: %w, output: %s", err, string(output)),
		}, err
	}

	// Récupérer les informations du commit
	info, err := cs.getCommitInfo(destPath)
	if err != nil {
		cs.logger.Warn("Failed to get commit info", "error", err, "path", destPath)
		info = &commitInfo{}
	}

	// Calculer la taille du projet
	size, err := cs.calculateDirectorySize(destPath)
	if err != nil {
		cs.logger.Warn("Failed to calculate directory size", "error", err, "path", destPath)
	}

	// Lister les fichiers
	files, err := cs.listProjectFiles(destPath)
	if err != nil {
		cs.logger.Warn("Failed to list project files", "error", err, "path", destPath)
	}

	// Checkout vers un tag spécifique si spécifié
	if options.Tag != "" {
		if err := cs.checkoutTag(destPath, options.Tag); err != nil {
			return nil, fmt.Errorf("failed to checkout tag %s: %w", options.Tag, err)
		}
	}

	return &CloneResult{
		ProjectPath: destPath,
		CommitHash:  info.Hash,
		Branch:      info.Branch,
		Tag:         options.Tag,
		Author:      info.Author,
		Message:     info.Message,
		Timestamp:   startTime,
		Files:       files,
		Size:        size,
		Success:     true,
		Error:       nil,
	}, nil
}

// commitInfo structure pour les informations de commit
type commitInfo struct {
	Hash    string
	Branch  string
	Author  string
	Message string
}

// getCommitInfo récupère les informations du commit courant
func (cs *CloneService) getCommitInfo(repoPath string) (*commitInfo, error) {
	info := &commitInfo{}

	// Hash du commit
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit hash: %w", err)
	}
	info.Hash = strings.TrimSpace(string(output))

	// Branche courante
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get branch: %w", err)
	}
	info.Branch = strings.TrimSpace(string(output))

	// Auteur du commit
	cmd = exec.Command("git", "log", "-1", "--pretty=format:%an")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get author: %w", err)
	}
	info.Author = strings.TrimSpace(string(output))

	// Message du commit
	cmd = exec.Command("git", "log", "-1", "--pretty=format:%s")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit message: %w", err)
	}
	info.Message = strings.TrimSpace(string(output))

	return info, nil
}

// Pull met à jour un dépôt existant
func (cs *CloneService) Pull(ctx context.Context, projectID string, options *CloneOptions) (*CloneResult, error) {
	cs.logger.Info("Starting git pull", "projectID", projectID)

	destPath := filepath.Join(cs.workDir, projectID)
	
	// Vérifier si le dépôt existe
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		cs.logger.Info("Repository doesn't exist, cloning instead", "projectID", projectID)
		return cs.Clone(ctx, projectID, options)
	}

	// Configurer les credentials
	if err := cs.setupCredentials(options); err != nil {
		return nil, fmt.Errorf("failed to setup credentials: %w", err)
	}

	// Exécuter git pull
	cmd := exec.CommandContext(ctx, "git", "pull", "origin", options.Branch)
	cmd.Dir = destPath
	
	if options.Token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_TOKEN=%s", options.Token))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &CloneResult{
			ProjectPath: destPath,
			Success:     false,
			Error:       fmt.Errorf("git pull failed: %w, output: %s", err, string(output)),
		}, err
	}

	// Récupérer les informations mises à jour
	info, err := cs.getCommitInfo(destPath)
	if err != nil {
		cs.logger.Warn("Failed to get commit info after pull", "error", err)
		info = &commitInfo{}
	}

	size, err := cs.calculateDirectorySize(destPath)
	if err != nil {
		cs.logger.Warn("Failed to calculate directory size", "error", err)
	}

	files, err := cs.listProjectFiles(destPath)
	if err != nil {
		cs.logger.Warn("Failed to list project files", "error", err)
	}

	result := &CloneResult{
		ProjectPath: destPath,
		CommitHash:  info.Hash,
		Branch:      info.Branch,
		Author:      info.Author,
		Message:     info.Message,
		Timestamp:   time.Now(),
		Files:       files,
		Size:        size,
		Success:     true,
		Error:       nil,
	}

	cs.logger.Info("Git pull completed", "projectID", projectID, "hash", info.Hash)
	return result, nil
}

// validateCloneOptions valide les options de clonage
func (cs *CloneService) validateCloneOptions(options *CloneOptions) error {
	if options == nil {
		return fmt.Errorf("clone options cannot be nil")
	}
	
	if options.URL == "" {
		return fmt.Errorf("repository URL is required")
	}
	
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Minute // Timeout par défaut
	}
	
	if options.RetryCount < 0 {
		options.RetryCount = 2 // Retry par défaut
	}
	
	return nil
}

// setupCredentials configure les credentials Git
func (cs *CloneService) setupCredentials(options *CloneOptions) error {
	if options.Token != "" {
		// Configurer le token d'authentification
		return cs.setupTokenAuth(options.Token)
	}
	
	if options.Username != "" && options.Password != "" {
		// Configurer l'authentification par username/password
		return cs.setupBasicAuth(options.Username, options.Password)
	}
	
	if options.SSHKey != "" || options.SSHKeyPath != "" {
		// Configurer l'authentification SSH
		return cs.setupSSHAuth(options)
	}
	
	return nil
}

// setupTokenAuth configure l'authentification par token
func (cs *CloneService) setupTokenAuth(token string) error {
	// Configurer git credential helper pour utiliser le token
	cmd := exec.Command("git", "config", "--global", "credential.helper", "store")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure credential helper: %w", err)
	}
	
	return nil
}

// setupBasicAuth configure l'authentification basique
func (cs *CloneService) setupBasicAuth(username, password string) error {
	// Configurer les credentials Git
	cmd := exec.Command("git", "config", "--global", "user.name", username)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git username: %w", err)
	}
	
	return nil
}

// setupSSHAuth configure l'authentification SSH
func (cs *CloneService) setupSSHAuth(options *CloneOptions) error {
	var keyPath string
	
	if options.SSHKeyPath != "" {
		keyPath = options.SSHKeyPath
	} else if options.SSHKey != "" {
		// Écrire la clé SSH dans un fichier temporaire
		tempFile, err := os.CreateTemp("", "ssh-key-")
		if err != nil {
			return fmt.Errorf("failed to create temp file for SSH key: %w", err)
		}
		
		if _, err := tempFile.WriteString(options.SSHKey); err != nil {
			return fmt.Errorf("failed to write SSH key: %w", err)
		}
		
		keyPath = tempFile.Name()
		defer os.Remove(keyPath) // Nettoyer après utilisation
	}
	
	// Configurer SSH
	cmd := exec.Command("ssh-add", keyPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add SSH key: %w", err)
	}
	
	return nil
}

// checkoutTag checkout vers un tag spécifique
func (cs *CloneService) checkoutTag(repoPath, tag string) error {
	cmd := exec.Command("git", "checkout", tag)
	cmd.Dir = repoPath
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to checkout tag %s: %w", tag, err)
	}
	
	return nil
}

// cleanDirectory supprime et recrée un répertoire
func (cs *CloneService) cleanDirectory(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove directory: %w", err)
	}
	
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	
	return nil
}

// calculateDirectorySize calcule la taille d'un répertoire
func (cs *CloneService) calculateDirectorySize(path string) (int64, error) {
	var size int64
	
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	
	return size, err
}

// listProjectFiles liste les fichiers du projet
func (cs *CloneService) listProjectFiles(path string) ([]string, error) {
	var files []string
	
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Ignorer les répertoires .git et autres fichiers cachés
		if strings.Contains(filePath, "/.git/") || strings.HasPrefix(filepath.Base(filePath), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		
		if !info.IsDir() {
			relPath, err := filepath.Rel(path, filePath)
			if err != nil {
				return err
			}
			files = append(files, relPath)
		}
		
		return nil
	})
	
	return files, err
}

// postCloneProcessing effectue le post-traitement après clonage
func (cs *CloneService) postCloneProcessing(result *CloneResult) error {
	// Analyser les fichiers du projet pour détecter le type
	projectType, err := cs.detectProjectType(result.ProjectPath)
	if err != nil {
		cs.logger.Warn("Failed to detect project type", "error", err, "path", result.ProjectPath)
	} else {
		cs.logger.Info("Detected project type", "type", projectType, "path", result.ProjectPath)
	}
	
	// Créer un fichier de métadonnées
	if err := cs.createMetadataFile(result); err != nil {
		cs.logger.Warn("Failed to create metadata file", "error", err, "path", result.ProjectPath)
	}
	
	return nil
}

// detectProjectType détecte le type de projet basé sur les fichiers
func (cs *CloneService) detectProjectType(path string) (string, error) {
	// Vérifier la présence de fichiers caractéristiques
	files := map[string]string{
		"package.json":     "nodejs",
		"go.mod":           "go",
		"pom.xml":          "java",
		"requirements.txt": "python",
		"Dockerfile":       "docker",
		"docker-compose.yml": "docker-compose",
	}
	
	for filename, projectType := range files {
		if _, err := os.Stat(filepath.Join(path, filename)); err == nil {
			return projectType, nil
		}
	}
	
	return "unknown", nil
}

// createMetadataFile crée un fichier de métadonnées pour le projet
func (cs *CloneService) createMetadataFile(result *CloneResult) error {
	metadata := map[string]interface{}{
		"commit_hash": result.CommitHash,
		"branch":      result.Branch,
		"author":      result.Author,
		"message":     result.Message,
		"timestamp":   result.Timestamp,
		"size":        result.Size,
		"files_count": len(result.Files),
	}
	
	metadataPath := filepath.Join(result.ProjectPath, ".stackship-metadata.json")
	
	// Sérialiser et écrire les métadonnées
	// Implementation simplifiée - en production, utiliser json.Marshal
	content := fmt.Sprintf(`{
		"commit_hash": "%s",
		"branch": "%s",
		"author": "%s",
		"message": "%s",
		"timestamp": "%s",
		"size": %d,
		"files_count": %d
	}`, result.CommitHash, result.Branch, result.Author, result.Message, 
		result.Timestamp.Format(time.RFC3339), result.Size, len(result.Files))
	
	return os.WriteFile(metadataPath, []byte(content), 0644)
}

// GetProjectPath retourne le chemin du projet cloné
func (cs *CloneService) GetProjectPath(projectID string) string {
	return filepath.Join(cs.workDir, projectID)
}

// ProjectExists vérifie si un projet existe déjà
func (cs *CloneService) ProjectExists(projectID string) bool {
	path := cs.GetProjectPath(projectID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

// RemoveProject supprime un projet du disque
func (cs *CloneService) RemoveProject(projectID string) error {
	path := cs.GetProjectPath(projectID)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove project directory: %w", err)
	}
	
	cs.logger.Info("Project removed from disk", "projectID", projectID, "path", path)
	return nil
}