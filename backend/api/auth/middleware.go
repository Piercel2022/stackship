package auth

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"stackship/backend/utils"
)

/*
 * FONCTIONNALITÉS :
 * - Middleware d'authentification pour protéger les routes
 * - Validation des tokens JWT dans les en-têtes HTTP
 * - Injection des informations utilisateur dans le contexte
 * - Middleware de gestion des CORS
 * - Middleware de rate limiting
 * - Middleware de logging des requêtes
 * - Middleware de validation des permissions RBAC
 * - Gestion des erreurs d'authentification
 * - Middleware de timeout des requêtes
 *
 * RELATIONS AVEC L'APPLICATION :
 * - Utilisé par tous les endpoints protégés de l'application
 * - Intégré dans la chaîne de middleware du serveur HTTP
 * - Utilise jwt.go pour valider les tokens
 * - Utilise rbac.go pour vérifier les permissions
 * - Communique avec les handlers pour l'authentification
 * - Protège les modules projects, deployments, monitoring
 * - Fournit le contexte utilisateur aux handlers suivants
 * - Gère la sécurité globale de l'application
 */

// AuthMiddleware vérifie l'authentification JWT
func AuthMiddleware(jwtSvc *JWTService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Récupération du token depuis l'en-tête Authorization
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Authorization header required", http.StatusUnauthorized)
				return
			}

			// Vérification du format "Bearer <token>"
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
				return
			}

			// Extraction du token
			token := authHeader[7:]
			if token == "" {
				http.Error(w, "Token is empty", http.StatusUnauthorized)
				return
			}

			// Validation du token
			claims, err := jwtSvc.ValidateToken(token)
			if err != nil {
				http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
				return
			}

			// Injection des informations utilisateur dans le contexte
			ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
			ctx = context.WithValue(ctx, "user_email", claims.Email)
			ctx = context.WithValue(ctx, "user_role", claims.Role)
			ctx = context.WithValue(ctx, "token", token)

			// Poursuite avec le handler suivant
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RBACMiddleware vérifie les permissions basées sur les rôles
func RBACMiddleware(rbacSvc *RBACService, requiredPermission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Récupération du rôle depuis le contexte
			userRole, ok := r.Context().Value("user_role").(string)
			if !ok {
				http.Error(w, "User role not found in context", http.StatusInternalServerError)
				return
			}

			// Vérification de la permission
			if !rbacSvc.HasPermission(userRole, requiredPermission) {
				http.Error(w, "Insufficient permissions", http.StatusForbidden)
				return
			}

			// Poursuite avec le handler suivant
			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuthMiddleware permet l'authentification optionnelle
func OptionalAuthMiddleware(jwtSvc *JWTService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Récupération du token depuis l'en-tête Authorization
			authHeader := r.Header.Get("Authorization")

			// Si pas d'en-tête, on continue sans authentification
			if authHeader == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Vérification du format "Bearer <token>"
			if !strings.HasPrefix(authHeader, "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}

			// Extraction du token
			token := authHeader[7:]
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Validation du token (optionnelle)
			claims, err := jwtSvc.ValidateToken(token)
			if err == nil {
				// Injection des informations utilisateur dans le contexte
				ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
				ctx = context.WithValue(ctx, "user_email", claims.Email)
				ctx = context.WithValue(ctx, "user_role", claims.Role)
				ctx = context.WithValue(ctx, "token", token)
				r = r.WithContext(ctx)
			}

			// Poursuite avec le handler suivant
			next.ServeHTTP(w, r)
		})
	}
}

// CORSMiddleware gère les requêtes CORS
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Vérification si l'origine est autorisée
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")

			// Réponse aux requêtes OPTIONS (preflight)
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitMiddleware implémente une limitation de taux simple
func RateLimitMiddleware(maxRequests int, windowDuration time.Duration) func(http.Handler) http.Handler {
	// Simple rate limiter basé sur les IPs
	requestCounts := make(map[string][]time.Time)
	mu := sync.RWMutex{}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Récupération de l'IP client
			clientIP := utils.GetClientIP(r)
			now := time.Now()

			mu.Lock()
			defer mu.Unlock()

			// Nettoyage des anciennes requêtes
			if timestamps, exists := requestCounts[clientIP]; exists {
				validTimestamps := make([]time.Time, 0, len(timestamps))
				for _, timestamp := range timestamps {
					if now.Sub(timestamp) < windowDuration {
						validTimestamps = append(validTimestamps, timestamp)
					}
				}
				requestCounts[clientIP] = validTimestamps
			}

			// Vérification de la limite
			if len(requestCounts[clientIP]) >= maxRequests {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			// Ajout de la nouvelle requête
			requestCounts[clientIP] = append(requestCounts[clientIP], now)

			next.ServeHTTP(w, r)
		})
	}
}

// LoggingMiddleware enregistre les informations de requête
func LoggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Création d'un wrapper pour capturer le code de statut
			wrapper := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Traitement de la requête
			next.ServeHTTP(wrapper, r)

			// Logging des informations
			duration := time.Since(start)
			clientIP := utils.GetClientIP(r)
			userAgent := r.UserAgent()

			log.Printf(
				"[%s] %s %s %s - %d - %v - %s",
				start.Format("2006-01-02 15:04:05"),
				r.Method,
				r.URL.Path,
				clientIP,
				wrapper.statusCode,
				duration,
				userAgent,
			)
		})
	}
}

// responseWriter wrapper pour capturer le code de statut
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// TimeoutMiddleware ajoute un timeout aux requêtes
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Canal pour signaler la fin du traitement
			done := make(chan struct{})

			// Exécution du handler dans une goroutine
			go func() {
				defer close(done)
				next.ServeHTTP(w, r.WithContext(ctx))
			}()

			// Attente de la fin ou du timeout
			select {
			case <-done:
				// Requête terminée normalement
				return
			case <-ctx.Done():
				// Timeout atteint
				if ctx.Err() == context.DeadlineExceeded {
					http.Error(w, "Request timeout", http.StatusRequestTimeout)
				}
				return
			}
		})
	}
}

// SecurityHeadersMiddleware ajoute des en-têtes de sécurité
func SecurityHeadersMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// En-têtes de sécurité
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'self'")

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryMiddleware récupère les paniques et retourne une erreur 500
func RecoveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("Panic recovered: %v", err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// ChainMiddleware chaîne plusieurs middlewares ensemble
func ChainMiddleware(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(handler http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			handler = middlewares[i](handler)
		}
		return handler
	}
}

// GetUserFromContext récupère les informations utilisateur depuis le contexte
func GetUserFromContext(ctx context.Context) (userID, email, role string, ok bool) {
	userID, ok1 := ctx.Value("user_id").(string)
	email, ok2 := ctx.Value("user_email").(string)
	role, ok3 := ctx.Value("user_role").(string)

	return userID, email, role, ok1 && ok2 && ok3
}

// IsAuthenticated vérifie si l'utilisateur est authentifié
func IsAuthenticated(ctx context.Context) bool {
	_, _, _, ok := GetUserFromContext(ctx)
	return ok
}
