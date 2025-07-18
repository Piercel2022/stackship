package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// GitProvider représente les différents fournisseurs Git supportés
type GitProvider string

const (
	GitHubProvider    GitProvider = "github"
	GitLabProvider    GitProvider = "gitlab"
	BitbucketProvider GitProvider = "bitbucket"
	GenericProvider   GitProvider = "generic"
)

// Repository représente un dépôt Git
type Repository struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	URL         string      `json:"url"`
	Branch      string      `json:"branch"`
	Provider    GitProvider `json:"provider"`
	Owner       string      `json:"owner"`
	Private     bool        `json:"private"`
	Description string      `json:"description"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Commit représente un commit Git
type Commit struct {
	Hash      string    `json:"hash"`
	Message   string    `json:"message"`
	Author    string    `json:"author"`
	Email     string    `json:"email"`
	Date      time.Time `json:"date"`
	Branch    string    `json:"branch"`
	ParentIDs []string  `json:"parent_ids"`
}

// Branch représente une branche Git
type Branch struct {
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	IsDefault bool      `json:"is_default"`
	Protected bool      `json:"protected"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Tag représente un tag Git
type Tag struct {
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	Message   string    `json:"message"`
	Tagger    string    `json:"tagger"`
	Date      time.Time `json:"date"`
}

// WebhookPayload représente les données d'un webhook Git
type WebhookPayload struct {
	Event      string     `json:"event"`
	Repository Repository `json:"repository"`
	Commits    []Commit   `json:"commits"`
	Branch     string     `json:"branch"`
	Tag        *Tag       `json:"tag,omitempty"`
	PushedAt   time.Time  `json:"pushed_at"`
	Pusher     string     `json:"pusher"`
}

// GitCredentials représente les informations d'authentification Git
type GitCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"` // Token pour la plupart des providers
	SSHKey   string `json:"ssh_key,omitempty"`
}

// BuildConfig représente la configuration de build trouvée dans le dépôt
type BuildConfig struct {
	Language     string            `yaml:"language"`
	Runtime      string            `yaml:"runtime"`
	BuildCommand string            `yaml:"build_command"`
	StartCommand string            `yaml:"start_command"`
	Environment  map[string]string `yaml:"environment"`
	Dockerfile   string            `yaml:"dockerfile"`
	Port         int               `yaml:"port"`
	Dependencies []string          `yaml:"dependencies"`
}

// GitClient interface définit les opérations Git
type GitClient interface {
	// Repository operations
	CloneRepository(ctx context.Context, repoURL, branch, targetDir string, creds *GitCredentials) error
	PullRepository(ctx context.Context, repoPath, branch string, creds *GitCredentials) error
	GetRepository(ctx context.Context, repoURL string, creds *GitCredentials) (*Repository, error)
	
	// Commit operations
	GetCommits(ctx context.Context, repoPath, branch string, limit int) ([]Commit, error)
	GetCommit(ctx context.Context, repoPath, commitHash string) (*Commit, error)
	GetLatestCommit(ctx context.Context, repoPath, branch string) (*Commit, error)
	
	// Branch operations
	GetBranches(ctx context.Context, repoPath string) ([]Branch, error)
	GetBranch(ctx context.Context, repoPath, branchName string) (*Branch, error)
	CreateBranch(ctx context.Context, repoPath, branchName, fromBranch string) error
	DeleteBranch(ctx context.Context, repoPath, branchName string) error
	
	// Tag operations
	GetTags(ctx context.Context, repoPath string) ([]Tag, error)
	GetTag(ctx context.Context, repoPath, tagName string) (*Tag, error)
	CreateTag(ctx context.Context, repoPath, tagName, message string) error
	
	// File operations
	GetFileContent(ctx context.Context, repoPath, filePath, branch string) ([]byte, error)
	GetBuildConfig(ctx context.Context, repoPath, branch string) (*BuildConfig, error)
	DetectLanguage(ctx context.Context, repoPath string) (string, error)
	
	// Webhook operations
	ValidateWebhook(ctx context.Context, payload []byte, secret string) bool
	ParseWebhook(ctx context.Context, provider GitProvider, payload []byte) (*WebhookPayload, error)
	
	// Utility operations
	GetRepositorySize(ctx context.Context, repoPath string) (int64, error)
	GetFileChanges(ctx context.Context, repoPath, fromCommit, toCommit string) ([]string, error)
	IsRepositoryClean(ctx context.Context, repoPath string) (bool, error)
}

// gitClient implémente GitClient
type gitClient struct {
	logger       *logrus.Logger
	workspaceDir string
	httpClient   *http.Client
}

// NewGitClient crée une nouvelle instance de GitClient
func NewGitClient(logger *logrus.Logger, workspaceDir string) GitClient {
	return &gitClient{
		logger:       logger,
		workspaceDir: workspaceDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CloneRepository clone un dépôt Git
func (gc *gitClient) CloneRepository(ctx context.Context, repoURL, branch, targetDir string, creds *GitCredentials) error {
	gc.logger.WithFields(logrus.Fields{
		"repo_url":   repoURL,
		"branch":     branch,
		"target_dir": targetDir,
	}).Info("Cloning repository")

	cloneOptions := &git.CloneOptions{
		URL:      repoURL,
		Progress: os.Stdout,
	}

	// Configuration de l'authentification
	if creds != nil {
		cloneOptions.Auth = &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Password,
		}
	}

	// Configuration de la branche
	if branch != "" {
		cloneOptions.ReferenceName = plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branch))
		cloneOptions.SingleBranch = true
	}

	// Création du répertoire cible
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Clonage du dépôt
	_, err := git.PlainCloneContext(ctx, targetDir, false, cloneOptions)
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	gc.logger.WithField("target_dir", targetDir).Info("Repository cloned successfully")
	return nil
}

// PullRepository met à jour un dépôt existant
func (gc *gitClient) PullRepository(ctx context.Context, repoPath, branch string, creds *GitCredentials) error {
	gc.logger.WithFields(logrus.Fields{
		"repo_path": repoPath,
		"branch":    branch,
	}).Info("Pulling repository")

	// Ouverture du dépôt
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la working tree
	workTree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Configuration du pull
	pullOptions := &git.PullOptions{
		RemoteName: "origin",
	}

	// Configuration de l'authentification
	if creds != nil {
		pullOptions.Auth = &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Password,
		}
	}

	// Configuration de la branche
	if branch != "" {
		pullOptions.ReferenceName = plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branch))
	}

	// Pull du dépôt
	err = workTree.PullContext(ctx, pullOptions)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to pull repository: %w", err)
	}

	gc.logger.WithField("repo_path", repoPath).Info("Repository pulled successfully")
	return nil
}

// GetRepository récupère les informations d'un dépôt
func (gc *gitClient) GetRepository(ctx context.Context, repoURL string, creds *GitCredentials) (*Repository, error) {
	gc.logger.WithField("repo_url", repoURL).Info("Getting repository information")

	// Détection du provider
	provider := gc.detectProvider(repoURL)
	
	// Parsing de l'URL pour extraire owner et name
	parts := strings.Split(strings.TrimSuffix(repoURL, ".git"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repository URL format")
	}

	owner := parts[len(parts)-2]
	name := parts[len(parts)-1]

	// Création temporaire du dépôt pour récupérer les informations
	tempDir := filepath.Join(gc.workspaceDir, "temp", fmt.Sprintf("repo_%d", time.Now().Unix()))
	defer os.RemoveAll(tempDir)

	err := gc.CloneRepository(ctx, repoURL, "", tempDir, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to clone repository for inspection: %w", err)
	}

	// Récupération des informations du dépôt
	repo, err := git.PlainOpen(tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la branche par défaut
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	defaultBranch := strings.TrimPrefix(head.Name().String(), "refs/heads/")

	repository := &Repository{
		ID:        gc.generateRepositoryID(repoURL),
		Name:      name,
		URL:       repoURL,
		Branch:    defaultBranch,
		Provider:  provider,
		Owner:     owner,
		Private:   false, // À déterminer selon le provider
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return repository, nil
}

// GetCommits récupère les commits d'une branche
func (gc *gitClient) GetCommits(ctx context.Context, repoPath, branch string, limit int) ([]Commit, error) {
	gc.logger.WithFields(logrus.Fields{
		"repo_path": repoPath,
		"branch":    branch,
		"limit":     limit,
	}).Info("Getting commits")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la référence de la branche
	var ref *plumbing.Reference
	if branch != "" {
		ref, err = repo.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branch)), true)
		if err != nil {
			return nil, fmt.Errorf("failed to get branch reference: %w", err)
		}
	} else {
		ref, err = repo.Head()
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD: %w", err)
		}
	}

	// Récupération des commits
	commitIter, err := repo.Log(&git.LogOptions{
		From: ref.Hash(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit iterator: %w", err)
	}
	defer commitIter.Close()

	var commits []Commit
	count := 0
	
	err = commitIter.ForEach(func(c *object.Commit) error {
		if limit > 0 && count >= limit {
			return fmt.Errorf("limit reached") // Arrêt de l'itération
		}

		parentIDs := make([]string, len(c.ParentHashes))
		for i, parentHash := range c.ParentHashes {
			parentIDs[i] = parentHash.String()
		}

		commit := Commit{
			Hash:      c.Hash.String(),
			Message:   c.Message,
			Author:    c.Author.Name,
			Email:     c.Author.Email,
			Date:      c.Author.When,
			Branch:    branch,
			ParentIDs: parentIDs,
		}

		commits = append(commits, commit)
		count++
		return nil
	})

	if err != nil && err.Error() != "limit reached" {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return commits, nil
}

// GetCommit récupère un commit spécifique
func (gc *gitClient) GetCommit(ctx context.Context, repoPath, commitHash string) (*Commit, error) {
	gc.logger.WithFields(logrus.Fields{
		"repo_path":   repoPath,
		"commit_hash": commitHash,
	}).Info("Getting commit")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	hash := plumbing.NewHash(commitHash)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	parentIDs := make([]string, len(commit.ParentHashes))
	for i, parentHash := range commit.ParentHashes {
		parentIDs[i] = parentHash.String()
	}

	return &Commit{
		Hash:      commit.Hash.String(),
		Message:   commit.Message,
		Author:    commit.Author.Name,
		Email:     commit.Author.Email,
		Date:      commit.Author.When,
		ParentIDs: parentIDs,
	}, nil
}

// GetLatestCommit récupère le dernier commit d'une branche
func (gc *gitClient) GetLatestCommit(ctx context.Context, repoPath, branch string) (*Commit, error) {
	commits, err := gc.GetCommits(ctx, repoPath, branch, 1)
	if err != nil {
		return nil, err
	}

	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits found")
	}

	return &commits[0], nil
}

// GetBranches récupère toutes les branches
func (gc *gitClient) GetBranches(ctx context.Context, repoPath string) ([]Branch, error) {
	gc.logger.WithField("repo_path", repoPath).Info("Getting branches")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la branche par défaut
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	defaultBranch := strings.TrimPrefix(head.Name().String(), "refs/heads/")

	// Récupération de toutes les branches
	branchIter, err := repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("failed to get branches: %w", err)
	}
	defer branchIter.Close()

	var branches []Branch
	err = branchIter.ForEach(func(ref *plumbing.Reference) error {
		branchName := strings.TrimPrefix(ref.Name().String(), "refs/heads/")
		
		// Récupération du commit le plus récent
		commit, err := repo.CommitObject(ref.Hash())
		if err != nil {
			return err
		}

		branch := Branch{
			Name:      branchName,
			Hash:      ref.Hash().String(),
			IsDefault: branchName == defaultBranch,
			Protected: false, // À déterminer selon la configuration
			UpdatedAt: commit.Author.When,
		}

		branches = append(branches, branch)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to iterate branches: %w", err)
	}

	return branches, nil
}

// GetBranch récupère une branche spécifique
func (gc *gitClient) GetBranch(ctx context.Context, repoPath, branchName string) (*Branch, error) {
	branches, err := gc.GetBranches(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	for _, branch := range branches {
		if branch.Name == branchName {
			return &branch, nil
		}
	}

	return nil, fmt.Errorf("branch not found: %s", branchName)
}

// CreateBranch crée une nouvelle branche
func (gc *gitClient) CreateBranch(ctx context.Context, repoPath, branchName, fromBranch string) error {
	gc.logger.WithFields(logrus.Fields{
		"repo_path":   repoPath,
		"branch_name": branchName,
		"from_branch": fromBranch,
	}).Info("Creating branch")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la référence de la branche source
	var fromRef *plumbing.Reference
	if fromBranch != "" {
		fromRef, err = repo.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", fromBranch)), true)
		if err != nil {
			return fmt.Errorf("failed to get source branch reference: %w", err)
		}
	} else {
		fromRef, err = repo.Head()
		if err != nil {
			return fmt.Errorf("failed to get HEAD: %w", err)
		}
	}

	// Création de la nouvelle branche
	branchRef := plumbing.NewHashReference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branchName)), fromRef.Hash())
	err = repo.Storer.SetReference(branchRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	gc.logger.WithField("branch_name", branchName).Info("Branch created successfully")
	return nil
}

// DeleteBranch supprime une branche
func (gc *gitClient) DeleteBranch(ctx context.Context, repoPath, branchName string) error {
	gc.logger.WithFields(logrus.Fields{
		"repo_path":   repoPath,
		"branch_name": branchName,
	}).Info("Deleting branch")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Suppression de la branche
	err = repo.Storer.RemoveReference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branchName)))
	if err != nil {
		return fmt.Errorf("failed to delete branch: %w", err)
	}

	gc.logger.WithField("branch_name", branchName).Info("Branch deleted successfully")
	return nil
}

// GetTags récupère tous les tags
func (gc *gitClient) GetTags(ctx context.Context, repoPath string) ([]Tag, error) {
	gc.logger.WithField("repo_path", repoPath).Info("Getting tags")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	tagIter, err := repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}
	defer tagIter.Close()

	var tags []Tag
	err = tagIter.ForEach(func(ref *plumbing.Reference) error {
		tagName := strings.TrimPrefix(ref.Name().String(), "refs/tags/")
		
		// Tentative de récupération du tag annoté
		tagObj, err := repo.TagObject(ref.Hash())
		if err != nil {
			// Si ce n'est pas un tag annoté, c'est un tag léger
			tag := Tag{
				Name: tagName,
				Hash: ref.Hash().String(),
				Date: time.Now(), // Approximation pour les tags légers
			}
			tags = append(tags, tag)
			return nil
		}

		tag := Tag{
			Name:    tagName,
			Hash:    ref.Hash().String(),
			Message: tagObj.Message,
			Tagger:  tagObj.Tagger.Name,
			Date:    tagObj.Tagger.When,
		}

		tags = append(tags, tag)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to iterate tags: %w", err)
	}

	return tags, nil
}

// GetTag récupère un tag spécifique
func (gc *gitClient) GetTag(ctx context.Context, repoPath, tagName string) (*Tag, error) {
	tags, err := gc.GetTags(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	for _, tag := range tags {
		if tag.Name == tagName {
			return &tag, nil
		}
	}

	return nil, fmt.Errorf("tag not found: %s", tagName)
}

// CreateTag crée un nouveau tag
func (gc *gitClient) CreateTag(ctx context.Context, repoPath, tagName, message string) error {
	gc.logger.WithFields(logrus.Fields{
		"repo_path": repoPath,
		"tag_name":  tagName,
		"message":   message,
	}).Info("Creating tag")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération du HEAD
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Création du tag
	tagRef := plumbing.NewHashReference(plumbing.ReferenceName(fmt.Sprintf("refs/tags/%s", tagName)), head.Hash())
	err = repo.Storer.SetReference(tagRef)
	if err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	gc.logger.WithField("tag_name", tagName).Info("Tag created successfully")
	return nil
}

// GetFileContent récupère le contenu d'un fichier
func (gc *gitClient) GetFileContent(ctx context.Context, repoPath, filePath, branch string) ([]byte, error) {
	gc.logger.WithFields(logrus.Fields{
		"repo_path": repoPath,
		"file_path": filePath,
		"branch":    branch,
	}).Info("Getting file content")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Récupération de la référence de la branche
	var ref *plumbing.Reference
	if branch != "" {
		ref, err = repo.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branch)), true)
		if err != nil {
			return nil, fmt.Errorf("failed to get branch reference: %w", err)
		}
	} else {
		ref, err = repo.Head()
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD: %w", err)
		}
	}

	// Récupération du commit
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Récupération du tree
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %w", err)
	}

	// Récupération du fichier
	file, err := tree.File(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to get file contents: %w", err)
	}

	return []byte(content), nil
}

// GetBuildConfig récupère la configuration de build
func (gc *gitClient) GetBuildConfig(ctx context.Context, repoPath, branch string) (*BuildConfig, error) {
	gc.logger.WithFields(logrus.Fields{
		"repo_path": repoPath,
		"branch":    branch,
	}).Info("Getting build configuration")

	// Recherche des fichiers de configuration
	configFiles := []string{
		"stackship.yml",
		"stackship.yaml",
		".stackship.yml",
		".stackship.yaml",
		"buildpack.yml",
		"buildpack.yaml",
	}

	for _, configFile := range configFiles {
		content, err := gc.GetFileContent(ctx, repoPath, configFile, branch)
		if err != nil {
			continue // Fichier non trouvé, essayer le suivant
		}

		var config BuildConfig
		err = yaml.Unmarshal(content, &config)
		if err != nil {
			gc.logger.WithError(err).Warnf("Failed to parse config file: %s", configFile)
			continue
		}

		return &config, nil
	}

	// Si aucun fichier de configuration n'est trouvé, détecter automatiquement
	language, err := gc.DetectLanguage(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to detect language: %w", err)
	}

	return gc.generateDefaultBuildConfig(language), nil
}

// DetectLanguage détecte le langage du projet
func (gc *gitClient) DetectLanguage(ctx context.Context, repoPath string) (string, error) {
	gc.logger.WithField("repo_path", repoPath).Info("Detecting language")

	// Définition des fichiers indicateurs par langage
	languageFiles := map[string][]string{
		"go":         {"go.mod", "go.sum", "main.go"},
		"node":       {"package.json", "package-lock.json", "yarn.lock"},
		"python":     {"requirements.txt", "setup.py", "pyproject.toml", "Pipfile"},
		"java":       {"pom.xml", "build.gradle", "build.gradle.kts"},
		"php":        {"composer.json", "composer.lock"},
		"ruby":       {"Gemfile", "Gemfile.lock"},
		"rust":       {"Cargo.toml", "Cargo.lock"},
		"docker":     {"Dockerfile", "docker-compose.yml"},
	}

	for language, files := range languageFiles {
		for _, file := range files {
			_, err := gc.GetFileContent(ctx, repoPath, file, "")
			if err == nil {
				gc.logger.WithField("language", language).Info("Language detected")
				return language, nil
			}
		}
	}

	return "unknown", nil
}

// ValidateWebhook valide la signature d'un webhook
func (gc *gitClient) ValidateWebhook(ctx context.Context, payload []byte, secret string) bool {
	if secret == "" {
		return true // Pas de validation si pas de secret
	}

	// Génération de la signature attendue
	h := sha256.New()
	h.Write([]byte(secret))
	h.Write(payload)
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	// Validation de la signature (implémentation basique)
	// En production, il faudrait utiliser les mécanismes spécifiques à chaque provider
	return expectedSignature != ""
}

// ParseWebhook parse un webhook selon le provider
func (gc *gitClient) ParseWebhook(ctx context.Context, provider GitProvider, payload []byte) (*WebhookPayload, error) {
	gc.logger.WithField("provider", provider).Info("Parsing webhook")

	// Parsing basique - à adapter selon chaque provider
	var webhookPayload WebhookPayload
	
	switch provider {
	case GitHubProvider:
		return gc.parseGitHubWebhook(payload)
	case GitLabProvider:
		return gc.parseGitLabWebhook(payload)
	case BitbucketProvider:
		return gc.parseBitbucketWebhook(payload)
	default