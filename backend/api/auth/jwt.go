package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

/*
=== FONCTIONNALITÉS DU FICHIER jwt.go ===

1. Génération de tokens JWT d'accès et de rafraîchissement
2. Validation et parsing des tokens JWT
3. Extraction des claims utilisateur depuis les tokens
4. Gestion de l'expiration des tokens
5. Rotation des tokens de rafraîchissement
6. Support des différents types de tokens (access, refresh)

=== RELATION AVEC L'APPLICATION STACKSHIP ===

Ce fichier est au cœur du système d'authentification de StackShip :
- Utilisé par /api/auth/handlers.go pour générer les tokens lors de la connexion
- Intégré dans /api/auth/middleware.go pour valider les tokens sur chaque requête
- Stocke les informations utilisateur (ID, rôles, permissions) dans les claims JWT
- Communique avec /api/auth/rbac.go pour inclure les rôles dans les tokens
- Utilisé par le frontend via /services/auth.ts pour gérer l'authentification
- Les tokens sont stockés côté client et envoyés avec chaque requête API
- Permet l'accès sécurisé aux ressources projets, déploiements, monitoring
- Intégré dans le système de notifications WebSocket pour l'authentification temps réel

=== ARCHITECTURE DE SÉCURITÉ ===
- Tokens courts (15min) + refresh tokens longs (7 jours) pour la sécurité
- Claims personnalisés pour les rôles et permissions StackShip
- Support de la rotation des tokens pour une sécurité renforcée
- Validation stricte des signatures et de l'expiration
*/

// TokenType représente le type de token JWT
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

// CustomClaims représente les claims personnalisés pour StackShip
type CustomClaims struct {
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	Username    string   `json:"username"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	TokenType   string   `json:"token_type"`
	TokenID     string   `json:"token_id"`
	jwt.RegisteredClaims
}

// JWTManager gère la création et validation des tokens JWT
type JWTManager struct {
	secretKey       string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	issuer          string
}

// NewJWTManager crée une nouvelle instance du gestionnaire JWT
func NewJWTManager(secretKey string, accessTTL, refreshTTL time.Duration, issuer string) *JWTManager {
	return &JWTManager{
		secretKey:       secretKey,
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
		issuer:          issuer,
	}
}

// TokenPair représente une paire de tokens access/refresh
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// GenerateTokenPair génère une paire de tokens pour un utilisateur
func (jm *JWTManager) GenerateTokenPair(userID, email, username string, roles, permissions []string) (*TokenPair, error) {
	// Générer le token d'accès
	accessToken, err := jm.generateToken(userID, email, username, roles, permissions, AccessToken)
	if err != nil {
		return nil, fmt.Errorf("erreur génération access token: %w", err)
	}

	// Générer le token de rafraîchissement
	refreshToken, err := jm.generateToken(userID, email, username, roles, permissions, RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("erreur génération refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(jm.accessTokenTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// generateToken génère un token JWT avec les claims spécifiés
func (jm *JWTManager) generateToken(userID, email, username string, roles, permissions []string, tokenType TokenType) (string, error) {
	now := time.Now()
	var expiresAt time.Time

	// Définir la durée d'expiration selon le type de token
	switch tokenType {
	case AccessToken:
		expiresAt = now.Add(jm.accessTokenTTL)
	case RefreshToken:
		expiresAt = now.Add(jm.refreshTokenTTL)
	default:
		return "", errors.New("type de token invalide")
	}

	// Créer les claims personnalisés
	claims := CustomClaims{
		UserID:      userID,
		Email:       email,
		Username:    username,
		Roles:       roles,
		Permissions: permissions,
		TokenType:   string(tokenType),
		TokenID:     uuid.New().String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    jm.issuer,
			Subject:   userID,
			ID:        uuid.New().String(),
		},
	}

	// Créer le token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Signer le token
	signedToken, err := token.SignedString([]byte(jm.secretKey))
	if err != nil {
		return "", fmt.Errorf("erreur signature token: %w", err)
	}

	return signedToken, nil
}

// ValidateToken valide un token JWT et retourne les claims
func (jm *JWTManager) ValidateToken(tokenString string) (*CustomClaims, error) {
	// Parser le token
	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Vérifier la méthode de signature
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("méthode de signature inattendue: %v", token.Header["alg"])
		}
		return []byte(jm.secretKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("erreur parsing token: %w", err)
	}

	// Extraire les claims
	claims, ok := token.Claims.(*CustomClaims)
	if !ok || !token.Valid {
		return nil, errors.New("token invalide")
	}

	return claims, nil
}

// RefreshToken génère un nouveau token d'accès à partir d'un refresh token
func (jm *JWTManager) RefreshToken(refreshTokenString string) (*TokenPair, error) {
	// Valider le refresh token
	claims, err := jm.ValidateToken(refreshTokenString)
	if err != nil {
		return nil, fmt.Errorf("refresh token invalide: %w", err)
	}

	// Vérifier que c'est bien un refresh token
	if claims.TokenType != string(RefreshToken) {
		return nil, errors.New("ce n'est pas un refresh token")
	}

	// Générer une nouvelle paire de tokens
	return jm.GenerateTokenPair(
		claims.UserID,
		claims.Email,
		claims.Username,
		claims.Roles,
		claims.Permissions,
	)
}

// ExtractUserFromToken extrait les informations utilisateur d'un token
func (jm *JWTManager) ExtractUserFromToken(tokenString string) (*UserInfo, error) {
	claims, err := jm.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Vérifier que c'est un access token
	if claims.TokenType != string(AccessToken) {
		return nil, errors.New("ce n'est pas un access token")
	}

	return &UserInfo{
		UserID:      claims.UserID,
		Email:       claims.Email,
		Username:    claims.Username,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
	}, nil
}

// IsTokenExpired vérifie si un token est expiré
func (jm *JWTManager) IsTokenExpired(tokenString string) bool {
	claims, err := jm.ValidateToken(tokenString)
	if err != nil {
		return true
	}

	return claims.ExpiresAt.Before(time.Now())
}

// GetTokenRemainingTime retourne le temps restant avant expiration
func (jm *JWTManager) GetTokenRemainingTime(tokenString string) (time.Duration, error) {
	claims, err := jm.ValidateToken(tokenString)
	if err != nil {
		return 0, err
	}

	remaining := claims.ExpiresAt.Sub(time.Now())
	if remaining < 0 {
		return 0, errors.New("token expiré")
	}

	return remaining, nil
}

// RevokeToken ajoute un token à la liste des tokens révoqués
// Note: Dans une implémentation complète, ceci devrait utiliser Redis ou une base de données
func (jm *JWTManager) RevokeToken(tokenString string) error {
	claims, err := jm.ValidateToken(tokenString)
	if err != nil {
		return err
	}

	// TODO: Implémenter la logique de révocation (Redis, DB, etc.)
	// Pour l'instant, on log juste l'action
	fmt.Printf("Token révoqué: ID=%s, UserID=%s\n", claims.ID, claims.UserID)

	return nil
}

// UserInfo représente les informations utilisateur extraites du token
type UserInfo struct {
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	Username    string   `json:"username"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

// HasRole vérifie si l'utilisateur a un rôle spécifique
func (ui *UserInfo) HasRole(role string) bool {
	for _, r := range ui.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasPermission vérifie si l'utilisateur a une permission spécifique
func (ui *UserInfo) HasPermission(permission string) bool {
	for _, p := range ui.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// HasAnyRole vérifie si l'utilisateur a au moins un des rôles spécifiés
func (ui *UserInfo) HasAnyRole(roles []string) bool {
	for _, role := range roles {
		if ui.HasRole(role) {
			return true
		}
	}
	return false
}

// HasAllRoles vérifie si l'utilisateur a tous les rôles spécifiés
func (ui *UserInfo) HasAllRoles(roles []string) bool {
	for _, role := range roles {
		if !ui.HasRole(role) {
			return false
		}
	}
	return true
}

// HasAnyPermission vérifie si l'utilisateur a au moins une des permissions spécifiées
func (ui *UserInfo) HasAnyPermission(permissions []string) bool {
	for _, permission := range permissions {
		if ui.HasPermission(permission) {
			return true
		}
	}
	return false
}

// HasAllPermissions vérifie si l'utilisateur a toutes les permissions spécifiées
func (ui *UserInfo) HasAllPermissions(permissions []string) bool {
	for _, permission := range permissions {
		if !ui.HasPermission(permission) {
			return false
		}
	}
	return true
}
