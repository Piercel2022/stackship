package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"stackship/backend/database"
	"stackship/backend/utils"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

/*
 * FONCTIONNALITÉS :
 * - Gestion des endpoints d'authentification (login, register, logout, refresh)
 * - Validation des données utilisateur
 * - Hashage et vérification des mots de passe
 * - Génération et validation des tokens JWT
 * - Gestion des sessions utilisateur
 * - Endpoints de profil utilisateur
 * - Réinitialisation de mot de passe
 *
 * RELATIONS AVEC L'APPLICATION :
 * - Point d'entrée HTTP pour toutes les opérations d'authentification
 * - Utilise jwt.go pour la gestion des tokens
 * - Utilise rbac.go pour la vérification des permissions
 * - Utilise middleware.go pour protéger les routes
 * - Communique avec la base de données via database/models.go
 * - Utilise utils/validator.go pour la validation
 * - Intégré dans le router principal de l'application
 * - Fournit les tokens nécessaires pour les autres modules (projects, deployments)
 */

// LoginRequest représente la structure de la demande de connexion
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

// RegisterRequest représente la structure de la demande d'inscription
type RegisterRequest struct {
	Email     string `json:"email" validate:"required,email"`
	Password  string `json:"password" validate:"required,min=8"`
	FirstName string `json:"first_name" validate:"required,min=2"`
	LastName  string `json:"last_name" validate:"required,min=2"`
	Role      string `json:"role,omitempty"`
}

// LoginResponse représente la réponse de connexion
type LoginResponse struct {
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
	User         UserInfo  `json:"user"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// UserInfo représente les informations utilisateur publiques
type UserInfo struct {
	ID        uint      `json:"id"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// AuthHandler gère les opérations d'authentification
type AuthHandler struct {
	db       *database.DB
	jwtSvc   *JWTService
	rbacSvc  *RBACService
	logger   *utils.Logger
	validate *utils.Validator
}

// NewAuthHandler crée une nouvelle instance d'AuthHandler
func NewAuthHandler(db *database.DB, jwtSvc *JWTService, rbacSvc *RBACService, logger *utils.Logger, validate *utils.Validator) *AuthHandler {
	return &AuthHandler{
		db:       db,
		jwtSvc:   jwtSvc,
		rbacSvc:  rbacSvc,
		logger:   logger,
		validate: validate,
	}
}

// RegisterRoutes enregistre toutes les routes d'authentification
func (h *AuthHandler) RegisterRoutes(router *mux.Router) {
	// Routes publiques
	router.HandleFunc("/auth/login", h.Login).Methods("POST")
	router.HandleFunc("/auth/register", h.Register).Methods("POST")
	router.HandleFunc("/auth/refresh", h.RefreshToken).Methods("POST")
	router.HandleFunc("/auth/forgot-password", h.ForgotPassword).Methods("POST")
	router.HandleFunc("/auth/reset-password", h.ResetPassword).Methods("POST")

	// Routes protégées
	protected := router.PathPrefix("/auth").Subrouter()
	protected.Use(AuthMiddleware(h.jwtSvc))
	protected.HandleFunc("/profile", h.GetProfile).Methods("GET")
	protected.HandleFunc("/profile", h.UpdateProfile).Methods("PUT")
	protected.HandleFunc("/logout", h.Logout).Methods("POST")
	protected.HandleFunc("/change-password", h.ChangePassword).Methods("POST")
}

// Login gère la connexion utilisateur
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Invalid JSON in login request", "error", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validation des données
	if err := h.validate.Struct(req); err != nil {
		h.logger.Error("Login validation failed", "error", err)
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Recherche de l'utilisateur
	user, err := h.db.GetUserByEmail(req.Email)
	if err != nil {
		h.logger.Error("User not found", "email", req.Email, "error", err)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Vérification du mot de passe
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		h.logger.Error("Invalid password", "email", req.Email)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Génération des tokens
	token, err := h.jwtSvc.GenerateToken(user.ID, user.Email, user.Role)
	if err != nil {
		h.logger.Error("Failed to generate token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	refreshToken, err := h.jwtSvc.GenerateRefreshToken(user.ID)
	if err != nil {
		h.logger.Error("Failed to generate refresh token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Mise à jour du dernier login
	if err := h.db.UpdateLastLogin(user.ID); err != nil {
		h.logger.Warning("Failed to update last login", "user_id", user.ID, "error", err)
	}

	// Réponse
	response := LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User: UserInfo{
			ID:        user.ID,
			Email:     user.Email,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
		},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	h.logger.Info("User logged in successfully", "user_id", user.ID, "email", user.Email)
}

// Register gère l'inscription utilisateur
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Invalid JSON in register request", "error", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validation des données
	if err := h.validate.Struct(req); err != nil {
		h.logger.Error("Register validation failed", "error", err)
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Vérification si l'utilisateur existe déjà
	if _, err := h.db.GetUserByEmail(req.Email); err == nil {
		h.logger.Warning("Registration attempt with existing email", "email", req.Email)
		http.Error(w, "Email already exists", http.StatusConflict)
		return
	}

	// Hashage du mot de passe
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("Failed to hash password", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Rôle par défaut si non spécifié
	role := req.Role
	if role == "" {
		role = "user"
	}

	// Création de l'utilisateur
	user := &database.User{
		Email:        req.Email,
		PasswordHash: string(hashedPassword),
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Role:         role,
		IsActive:     true,
		CreatedAt:    time.Now(),
	}

	if err := h.db.CreateUser(user); err != nil {
		h.logger.Error("Failed to create user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Génération des tokens
	token, err := h.jwtSvc.GenerateToken(user.ID, user.Email, user.Role)
	if err != nil {
		h.logger.Error("Failed to generate token for new user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	refreshToken, err := h.jwtSvc.GenerateRefreshToken(user.ID)
	if err != nil {
		h.logger.Error("Failed to generate refresh token for new user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Réponse
	response := LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User: UserInfo{
			ID:        user.ID,
			Email:     user.Email,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
		},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)

	h.logger.Info("User registered successfully", "user_id", user.ID, "email", user.Email)
}

// RefreshToken gère le renouvellement des tokens
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.Header.Get("Authorization")
	if refreshToken == "" {
		http.Error(w, "Refresh token required", http.StatusBadRequest)
		return
	}

	// Suppression du préfixe "Bearer "
	if strings.HasPrefix(refreshToken, "Bearer ") {
		refreshToken = refreshToken[7:]
	}

	// Validation du refresh token
	claims, err := h.jwtSvc.ValidateRefreshToken(refreshToken)
	if err != nil {
		h.logger.Error("Invalid refresh token", "error", err)
		http.Error(w, "Invalid refresh token", http.StatusUnauthorized)
		return
	}

	// Récupération des informations utilisateur
	user, err := h.db.GetUserByID(claims.UserID)
	if err != nil {
		h.logger.Error("User not found for refresh", "user_id", claims.UserID, "error", err)
		http.Error(w, "User not found", http.StatusUnauthorized)
		return
	}

	// Génération d'un nouveau token
	newToken, err := h.jwtSvc.GenerateToken(user.ID, user.Email, user.Role)
	if err != nil {
		h.logger.Error("Failed to generate new token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Réponse
	response := map[string]interface{}{
		"token":      newToken,
		"expires_at": time.Now().Add(24 * time.Hour),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	h.logger.Info("Token refreshed successfully", "user_id", user.ID)
}

// GetProfile récupère le profil utilisateur
func (h *AuthHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(uint)

	user, err := h.db.GetUserByID(userID)
	if err != nil {
		h.logger.Error("Failed to get user profile", "user_id", userID, "error", err)
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	userInfo := UserInfo{
		ID:        user.ID,
		Email:     user.Email,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)
}

// UpdateProfile met à jour le profil utilisateur
func (h *AuthHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(uint)

	var req struct {
		FirstName string `json:"first_name" validate:"required,min=2"`
		LastName  string `json:"last_name" validate:"required,min=2"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := h.validate.Struct(req); err != nil {
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Mise à jour du profil
	if err := h.db.UpdateUserProfile(userID, req.FirstName, req.LastName); err != nil {
		h.logger.Error("Failed to update user profile", "user_id", userID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Récupération du profil mis à jour
	user, err := h.db.GetUserByID(userID)
	if err != nil {
		h.logger.Error("Failed to get updated user profile", "user_id", userID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	userInfo := UserInfo{
		ID:        user.ID,
		Email:     user.Email,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)

	h.logger.Info("User profile updated successfully", "user_id", userID)
}

// Logout gère la déconnexion utilisateur
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Récupération du token depuis l'en-tête
	token := r.Header.Get("Authorization")
	if token != "" && strings.HasPrefix(token, "Bearer ") {
		token = token[7:]

		// Ajout du token à la blacklist
		if err := h.jwtSvc.BlacklistToken(token); err != nil {
			h.logger.Warning("Failed to blacklist token", "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Logged out successfully"})

	userID := r.Context().Value("user_id").(uint)
	h.logger.Info("User logged out successfully", "user_id", userID)
}

// ChangePassword gère le changement de mot de passe
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(uint)

	var req struct {
		CurrentPassword string `json:"current_password" validate:"required"`
		NewPassword     string `json:"new_password" validate:"required,min=8"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := h.validate.Struct(req); err != nil {
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Récupération de l'utilisateur
	user, err := h.db.GetUserByID(userID)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Vérification du mot de passe actuel
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		http.Error(w, "Current password is incorrect", http.StatusBadRequest)
		return
	}

	// Hashage du nouveau mot de passe
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("Failed to hash new password", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Mise à jour du mot de passe
	if err := h.db.UpdateUserPassword(userID, string(hashedPassword)); err != nil {
		h.logger.Error("Failed to update password", "user_id", userID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Password updated successfully"})

	h.logger.Info("Password changed successfully", "user_id", userID)
}

// ForgotPassword gère la demande de réinitialisation de mot de passe
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email" validate:"required,email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := h.validate.Struct(req); err != nil {
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Vérification de l'existence de l'utilisateur
	user, err := h.db.GetUserByEmail(req.Email)
	if err != nil {
		// Pour des raisons de sécurité, on ne révèle pas si l'email existe
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "If the email exists, a reset link has been sent"})
		return
	}

	// Génération d'un token de réinitialisation
	resetToken, err := h.jwtSvc.GenerateResetToken(user.ID)
	if err != nil {
		h.logger.Error("Failed to generate reset token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// TODO: Envoyer l'email de réinitialisation avec le token
	// Cette fonctionnalité nécessiterait un service d'email

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "If the email exists, a reset link has been sent",
		"token":   resetToken, // En développement seulement
	})

	h.logger.Info("Password reset requested", "email", req.Email)
}

// ResetPassword gère la réinitialisation du mot de passe
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token" validate:"required"`
		NewPassword string `json:"new_password" validate:"required,min=8"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := h.validate.Struct(req); err != nil {
		http.Error(w, "Invalid input data", http.StatusBadRequest)
		return
	}

	// Validation du token de réinitialisation
	claims, err := h.jwtSvc.ValidateResetToken(req.Token)
	if err != nil {
		h.logger.Error("Invalid reset token", "error", err)
		http.Error(w, "Invalid or expired reset token", http.StatusBadRequest)
		return
	}

	// Hashage du nouveau mot de passe
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("Failed to hash new password", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Mise à jour du mot de passe
	if err := h.db.UpdateUserPassword(claims.UserID, string(hashedPassword)); err != nil {
		h.logger.Error("Failed to update password", "user_id", claims.UserID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Password reset successfully"})

	h.logger.Info("Password reset successfully", "user_id", claims.UserID)
}
