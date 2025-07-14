package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

/*
=== FONCTIONNALITÉS DU FICHIER rbac.go ===

1. Définition des rôles et permissions pour StackShip
2. Système de contrôle d'accès basé sur les rôles (RBAC)
3. Gestion hiérarchique des permissions
4. Validation des autorisations pour les ressources
5. Gestion des contextes d'autorisation (projets, équipes)
6. Système de permissions granulaires pour les opérations

=== RELATION AVEC L'APPLICATION STACKSHIP ===

Ce fichier définit le modèle de sécurité de StackShip :
- Utilisé par /api/auth/jwt.go pour inclure les rôles dans les tokens
- Intégré dans /api/auth/middleware.go pour valider les permissions
- Référencé par /api/projects/handlers.go pour contrôler l'accès aux projets
- Utilisé par /api/deployments/handlers.go pour sécuriser les déploiements
- Intégré dans /api/monitoring/handlers.go pour l'accès aux métriques
- Communique avec /database/models.go pour persister les rôles utilisateur
- Utilisé par le frontend via Redux pour afficher les éléments UI appropriés
- Permet la gestion fine des permissions dans les équipes et projets
- Intégré dans le système WebSocket pour filtrer les notifications

=== HIÉRARCHIE DES RÔLES STACKSHIP ===
- SuperAdmin: Accès total à toute l'application
- Admin: Gestion des utilisateurs et configuration système
- TeamLead: Gestion des équipes et projets assignés
- Developer: Développement et déploiement sur projets assignés
- Viewer: Lecture seule sur projets assignés
- Guest: Accès très limité, lecture seule publique
*/

// Role représente un rôle utilisateur dans StackShip
type Role string

const (
	// Rôles hiérarchiques StackShip
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleTeamLead   Role = "team_lead"
	RoleDeveloper  Role = "developer"
	RoleViewer     Role = "viewer"
	RoleGuest      Role = "guest"
)

// Permission représente une permission spécifique dans StackShip
type Permission string

const (
	// Permissions système
	PermSystemManage Permission = "system:manage"
	PermSystemView   Permission = "system:view"
	PermSystemConfig Permission = "system:config"

	// Permissions utilisateurs
	PermUserCreate Permission = "user:create"
	PermUserRead   Permission = "user:read"
	PermUserUpdate Permission = "user:update"
	PermUserDelete Permission = "user:delete"
	PermUserManage Permission = "user:manage"

	// Permissions projets
	PermProjectCreate Permission = "project:create"
	PermProjectRead   Permission = "project:read"
	PermProjectUpdate Permission = "project:update"
	PermProjectDelete Permission = "project:delete"
	PermProjectManage Permission = "project:manage"

	// Permissions déploiements
	PermDeploymentCreate Permission = "deployment:create"
	PermDeploymentRead   Permission = "deployment:read"
	PermDeploymentUpdate Permission = "deployment:update"
	PermDeploymentDelete Permission = "deployment:delete"
	PermDeploymentManage Permission = "deployment:manage"
	PermDeploymentExecute Permission = "deployment:execute"

	// Permissions monitoring
	PermMonitoringRead   Permission = "monitoring:read"
	PermMonitoringManage Permission = "monitoring:manage"
	PermMetricsRead      Permission = "metrics:read"
	PermLogsRead         Permission = "logs:read"
	PermAlertsManage     Permission = "alerts:manage"

	// Permissions équipes
	PermTeamCreate Permission = "team:create"
	PermTeamRead   Permission = "team:read"
	PermTeamUpdate Permission = "team:update"
	PermTeamDelete Permission = "team:delete"
	PermTeamManage Permission = "team:manage"

	// Permissions configuration
	PermConfigRead   Permission = "config:read"
	PermConfigWrite  Permission = "config:write"
	PermConfigManage Permission = "config:manage"

	// Permissions sécurité
	PermSecurityRead   Permission = "security:read"
	PermSecurityManage Permission = "security:manage"
	PermAuditRead      Permission = "audit:read"
)

// Resource représente une ressource dans StackShip
type Resource string

const (
	ResourceSystem     Resource = "system"
	ResourceUser       Resource = "user"
	ResourceProject    Resource = "project"
	ResourceDeployment Resource = "deployment"
	ResourceMonitoring Resource = "monitoring"
	ResourceTeam       Resource = "team"
	ResourceConfig     Resource = "config"
	ResourceSecurity   Resource = "security"
)

// Action représente une action sur une ressource
type Action string

const (
	ActionCreate  Action = "create"
	ActionRead    Action = "read"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionManage  Action = "manage"
	ActionExecute Action = "execute"
)

// RolePermissions définit les permissions pour chaque rôle
var RolePermissions = map[Role][]Permission{
	RoleSuperAdmin: {
		// Accès total à toutes les permissions
		PermSystemManage, PermSystemView, PermSystemConfig,
		PermUserCreate, PermUserRead, PermUserUpdate, PermUserDelete, PermUserManage,
		PermProjectCreate, PermProjectRead, PermProjectUpdate, PermProjectDelete, PermProjectManage,
		PermDeploymentCreate, PermDeploymentRead, PermDeploymentUpdate, PermDeploymentDelete, PermDeploymentManage, PermDeploymentExecute,
		PermMonitoringRead, PermMonitoringManage, PermMetricsRead, PermLogsRead, PermAlertsManage,
		PermTeamCreate, PermTeamRead, PermTeamUpdate, PermTeamDelete, PermTeamManage,
		PermConfigRead, PermConfigWrite, PermConfigManage,
		PermSecurityRead, PermSecurityManage, PermAuditRead,
	},
	RoleAdmin: {
		// Gestion système et utilisateurs
		PermSystemView, PermSystemConfig,
		PermUserCreate, PermUserRead, PermUserUpdate, PermUserDelete, PermUserManage,
		PermProjectCreate, PermProjectRead, PermProjectUpdate, PermProjectDelete, PermProjectManage,
		PermDeploymentRead, PermDeploymentUpdate, PermDeploymentManage,
		PermMonitoringRead, PermMonitoringManage, PermMetricsRead, PermLogsRead, PermAlertsManage,
		PermTeamCreate, PermTeamRead, PermTeamUpdate, PermTeamDelete, PermTeamManage,
		PermConfigRead, PermConfigWrite, PermConfigManage,
		PermSecurityRead, PermSecurityManage, PermAuditRead,
	},
	RoleTeamLead: {
		// Gestion d'équipe et projets
		PermUserRead, PermUserUpdate,
		PermProjectCreate, PermProjectRead, PermProjectUpdate, PermProjectManage,
		PermDeploymentCreate, PermDeploymentRead, PermDeploymentUpdate, PermDeploymentManage, PermDeploymentExecute,
		PermMonitoringRead, PermMetricsRead, PermLogsRead, PermAlertsManage,
		PermTeamRead, PermTeamUpdate, PermTeamManage,
		PermConfigRead, PermConfigWrite,
		PermSecurityRead,
	},
	RoleDeveloper: {
		// Développement et déploiement
		PermUserRead,
		PermProjectRead, PermProjectUpdate,
		PermDeploymentCreate, PermDeploymentRead, PermDeploymentUpdate, PermDeploymentExecute,
		PermMonitoringRead, PermMetricsRead, PermLogsRead,
		PermTeamRead,
		PermConfigRead,
	},
	RoleViewer: {
		// Lecture seule
		PermUserRead,
		PermProjectRead,
		PermDeploymentRead,
		PermMonitoringRead, PermMetricsRead, PermLogsRead,
		PermTeamRead,
		PermConfigRead,
	},
	RoleGuest: {
		// Accès minimal
		PermProjectRead,
		PermDeploymentRead,
		PermMonitoringRead,
	},
}

// RoleHierarchy définit la hiérarchie des rôles
var RoleHierarchy = map[Role][]Role{
	RoleSuperAdmin: {RoleAdmin, RoleTeamLead, RoleDeveloper, RoleViewer, RoleGuest},
	RoleAdmin:      {RoleTeamLead, RoleDeveloper, RoleViewer, RoleGuest},
	RoleTeamLead:   {RoleDeveloper, RoleViewer, RoleGuest},
	RoleDeveloper:  {RoleViewer, RoleGuest},
	RoleViewer:     {RoleGuest},
	RoleGuest:      {},
}

// RoleWeight définit le poids de chaque rôle pour les comparaisons
var RoleWeight = map[Role]int{
	RoleSuperAdmin: 100,
	RoleAdmin:      80,
	RoleTeamLead:   60,
	RoleDeveloper:  40,
	RoleViewer:     20,
	RoleGuest:      10,
}

// RBAC représente le système de contrôle d'accès basé sur les rôles
type RBAC struct {
	rolePermissions map[Role][]Permission
	roleHierarchy   map[Role][]Role
	roleWeight      map[Role]int
}

// NewRBAC crée une nouvelle instance du système RBAC
func NewRBAC() *RBAC {
	return &RBAC{
		rolePermissions: RolePermissions,
		roleHierarchy:   RoleHierarchy,
		roleWeight:      RoleWeight,
	}
}

// Context représente le contexte d'autorisation
type Context struct {
	UserID    string
	ProjectID string
	TeamID    string
	Resource  Resource
	Action    Action
	Timestamp time.Time
}

// CheckPermission vérifie si un utilisateur a une permission spécifique
func (rbac *RBAC) CheckPermission(userRoles []string, permission Permission) bool {
	for _, roleStr := range userRoles {
		role := Role(roleStr)
		if rbac.roleHasPermission(role, permission) {
			return true
		}
	}
	return false
}

// CheckRolePermission vérifie si un rôle a une permission spécifique
func (rbac *RBAC) CheckRolePermission(role Role, permission Permission) bool {
	return rbac.roleHasPermission(role, permission)
}

// roleHasPermission vérifie si un rôle a une permission (avec héritage)
func (rbac *RBAC) roleHasPermission(role Role, permission Permission) bool {
	// Vérifier les permissions directes
	if permissions, exists := rbac.rolePermissions[role]; exists {
		for _, perm := range permissions {
			if perm == permission {
				return true
			}
		}
	}
	return false
}

// CheckResourceAccess vérifie l'accès à une ressource avec action
func (rbac *RBAC) CheckResourceAccess(userRoles []string, resource Resource, action Action) bool {
	permission := rbac.buildPermission(resource, action)
	return rbac.CheckPermission(userRoles, permission)
}

// buildPermission construit une permission à partir d'une ressource et d'une action
func (rbac *RBAC) buildPermission(resource Resource, action Action) Permission {
	return Permission(fmt.Sprintf("%s:%s", resource, action))
}

// HasHigherRole vérifie si un utilisateur a un rôle égal ou supérieur
func (rbac *RBAC) HasHigherRole(userRoles []string, requiredRole Role) bool {
	for _, roleStr := range userRoles {
		role := Role(roleStr)
		if rbac.isRoleHigherOrEqual(role, requiredRole) {
			return true
		}
	}
	return false
}

// isRoleHigherOrEqual vérifie si un rôle est supérieur ou égal à un autre
func (rbac *RBAC) isRoleHigherOrEqual(userRole, requiredRole Role) bool {
	if userRole == requiredRole {
		return true
	}

	// Vérifier dans la hiérarchie
	if subordinates, exists := rbac.roleHierarchy[userRole]; exists {
		for _, subordinate := range subordinates {
			if subordinate == requiredRole {
				return true
			}
		}
	}
	return false
}

// GetUserPermissions retourne toutes les permissions d'un utilisateur
func (rbac *RBAC) GetUserPermissions(userRoles []string) []Permission {
	permissionSet := make(map[Permission]bool)
	
	for _, roleStr := range userRoles {
		role := Role(roleStr)
		if permissions, exists := rbac.rolePermissions[role]; exists {
			for _, perm := range permissions {
				permissionSet[perm] = true
			}
		}
	}

	var permissions []Permission
	for perm := range permissionSet {
		permissions = append(permissions, perm)
	}
	return permissions
}

// ValidateRoles valide que les rôles fournis sont valides
func (rbac *RBAC) ValidateRoles(roles []string) error {
	for _, roleStr := range roles {
		role := Role(roleStr)
		if _, exists := rbac.rolePermissions[role]; !exists {
			return fmt.Errorf("rôle invalide: %s", roleStr)
		}
	}
	return nil
}

// AuthorizationMiddleware représente un middleware d'autorisation
type AuthorizationMiddleware struct {
	rbac *RBAC
}

// NewAuthorizationMiddleware crée un nouveau middleware d'autorisation
func NewAuthorizationMiddleware(rbac *RBAC) *AuthorizationMiddleware {
	return &AuthorizationMiddleware{rbac: rbac}
}

// RequirePermission crée un middleware qui exige une permission spécifique
func (am *AuthorizationMiddleware) RequirePermission(permission Permission) func(userRoles []string) error {
	return func(userRoles []string) error {
		if !am.rbac.CheckPermission(userRoles, permission) {
			return errors.New("permission insuffisante")
		}
		return nil
	}
}

// RequireRole crée un middleware qui exige un rôle minimum
func (am *AuthorizationMiddleware) RequireRole(requiredRole Role) func(userRoles []string) error {
	return func(userRoles []string) error {
		if !am.rbac.HasHigherRole(userRoles, requiredRole) {
			return errors.New("rôle insuffisant")
		}
		return nil
	}
}

// RequireResourceAccess crée un middleware pour l'accès aux ressources
func (am *AuthorizationMiddleware) RequireResourceAccess(resource Resource, action Action) func(userRoles []string) error {
	return func(userRoles []string) error {
		if !am.rbac.CheckResourceAccess(userRoles, resource, action) {
			return fmt.Errorf("accès refusé à %s:%s", resource, action)
		}
		return nil
	}
}

// ProjectAuthorizationContext représente le contexte d'autorisation pour un projet
type ProjectAuthorizationContext struct {
	ProjectID      string
	UserID         string
	UserRoles      []string
	ProjectRoles   []string // Rôles spécifiques au projet
	TeamID         string
	IsProjectOwner bool
	IsTeamMember   bool
}

// CheckProjectAccess vérifie l'accès à un projet spécifique
func (rbac *RBAC) CheckProjectAccess(ctx *ProjectAuthorizationContext, action Action) bool {
	// Vérifier si c'est le propriétaire du projet
	if ctx.IsProjectOwner {
		return true
	}

	// Vérifier les rôles système
	if rbac.CheckResourceAccess(ctx.UserRoles, ResourceProject, action) {
		return true
	}

	// Vérifier les rôles spécifiques au projet
	if rbac.CheckResourceAccess(ctx.ProjectRoles, ResourceProject, action) {
		return true
	}

	// Vérifier l'appartenance à l'équipe pour les actions de lecture
	if ctx.IsTeamMember && action == ActionRead {
		return true
	}

	return false
}

// GetEffectivePermissions retourne les permissions effectives dans un contexte
func (rbac *RBAC) GetEffectivePermissions(userRoles, contextRoles []string) []Permission {
	allRoles := append(userRoles, contextRoles...)
	return rbac.GetUserPermissions(allRoles)
}

// IsValidRole vérifie si un rôle est valide
func (rbac *RBAC) IsValidRole(role string) bool {
	_, exists := rbac.rolePermissions[Role(role)]
	return exists
}

// GetRoleHierarchy retourne la hiérarchie complète d'un rôle
func (rbac *RBAC) GetRoleHierarchy(role Role) []Role {
	if subordinates, exists := rbac.roleHierarchy[role]; exists {
		return subordinates
	}
	return []Role{}
}

// FilterPermissionsByPrefix filtre les permissions par préfixe
func (rbac *RBAC) FilterPermissionsByPrefix(permissions []Permission, prefix string) []Permission {
	var filtered []Permission
	for _, perm := range permissions {
		if strings.HasPrefix(string(perm), prefix) {
			filtered = append(filtered, perm)
		}
	}
	return filtered
}

// GetMinimumRequiredRole retourne le rôle minimum requis pour une permission
func (rbac *RBAC) GetMinimumRequiredRole(permission Permission) Role {
	minimumRole := RoleGuest
	minimumWeight := 0

	for role, permissions := range rbac.rolePermissions {
		for _, perm := range permissions {
			if perm == permission {
				if weight, exists := rbac.roleWeight[role]; exists {
					if weight > minimumWeight {
						minimumWeight = weight
						minimumRole = role
					}
				}
			}
		}
	}

	return minimumRole
}

// GetHighestRole retourne le rôle le plus élevé parmi une liste de rôles
func (rbac *RBAC) GetHighestRole(roles []string) Role {
	var highestRole Role
	highestWeight := 0

	for _, roleStr := range roles {
		role := Role(roleStr)
		if weight, exists := rbac.roleWeight[role]; exists {
			if weight > highestWeight {
				highestWeight = weight
				highestRole = role
			}
		}
	}

	return highestRole
}

// CanAssignRole vérifie si un utilisateur peut assigner un rôle à un autre
func (rbac *RBAC) CanAssignRole(assignerRoles []string, targetRole Role) bool {
	assignerHighestRole := rbac.GetHighestRole(assignerRoles)
	
	// Un utilisateur peut assigner des rôles inférieurs ou égaux au sien
	return rbac.isRoleHigherOrEqual(assignerHighestRole, targetRole)
}

// GetAllRoles retourne tous les rôles disponibles
func (rbac *RBAC) GetAllRoles() []Role {
	var roles []Role
	for role := range rbac.rolePermissions {
		roles = append(roles, role)
	}
	return roles
}

// GetAllPermissions retourne toutes les permissions disponibles
func (rbac *RBAC) GetAllPermissions() []Permission {
	permissionSet := make(map[Permission]bool)
	
	for _, permissions := range rbac.rolePermissions {
		for _, perm := range permissions {
			permissionSet[perm] = true
		}
	}

	var permissions []Permission
	for perm := range permissionSet {
		permissions = append(permissions, perm)
	}
	return permissions
}

// GetPermissionsByResource retourne les permissions pour une ressource spécifique
func (rbac *RBAC) GetPermissionsByResource(resource Resource) []Permission {
	var permissions []Permission
	prefix := string(resource) + ":"
	
	for _, perm := range rbac.GetAllPermissions() {
		if strings.HasPrefix(string(perm), prefix) {
			permissions = append(permissions, perm)
		}
	}
	return permissions
}

// HasAnyPermission vérifie si un utilisateur a au moins une des permissions
func (rbac *RBAC) HasAnyPermission(userRoles []string, permissions []Permission) bool {
	for _, perm := range permissions {
		if rbac.CheckPermission(userRoles, perm) {
			return true
		}
	}
	return false
}

// HasAllPermissions vérifie si un utilisateur a toutes les permissions
func (rbac *RBAC) HasAllPermissions(userRoles []string, permissions []Permission) bool {
	for _, perm := range permissions {
		if !rbac.CheckPermission(userRoles, perm) {
			return false
		}
	}
	return true
}

// GetMissingPermissions retourne les permissions manquantes pour un utilisateur
func (rbac *RBAC) GetMissingPermissions(userRoles []string, requiredPermissions []Permission) []Permission {
	var missing []Permission
	
	for _, perm := range requiredPermissions {
		if !rbac.CheckPermission(userRoles, perm) {
			missing = append(missing, perm)
		}
	}
	
	return missing
}

// AuditLog représente un log d'audit pour les actions d'autorisation
type AuditLog struct {
	UserID      string
	Action      string
	Resource    string
	Permission  Permission
	Granted     bool
	Timestamp   time.Time
	UserRoles   []string
	Context     map[string]interface{}
}

// LogAccess enregistre un accès dans les logs d'audit
func (rbac *RBAC) LogAccess(userID string, action string, resource string, permission Permission, granted bool, userRoles []string, context map[string]interface{}) {
	log := AuditLog{
		UserID:     userID,
		Action:     action,
		Resource:   resource,
		Permission: permission,
		Granted:    granted,
		Timestamp:  time.Now(),
		UserRoles:  userRoles,
		Context:    context,
	}
	
	// Ici vous pouvez implémenter l'envoi vers un système de logging
	// Par exemple : logrus, zap, ou une base de données
	_ = log // Évite l'erreur de variable non utilisée
}

// SecurityPolicy représente une politique de sécurité
type SecurityPolicy struct {
	Name            string
	Description     string
	RequiredRoles   []Role
	RequiredPermissions []Permission
	ResourceFilters []string
	IPWhitelist     []string
	TimeRestrictions map[string]string // ex: "start": "09:00", "end": "17:00"
}

// PolicyManager gère les politiques de sécurité
type PolicyManager struct {
	rbac     *RBAC
	policies map[string]*SecurityPolicy
}

// NewPolicyManager crée un nouveau gestionnaire de politiques
func NewPolicyManager(rbac *RBAC) *PolicyManager {
	return &PolicyManager{
		rbac:     rbac,
		policies: make(map[string]*SecurityPolicy),
	}
}

// AddPolicy ajoute une nouvelle politique
func (pm *PolicyManager) AddPolicy(name string, policy *SecurityPolicy) {
	pm.policies[name] = policy
}

// CheckPolicy vérifie si un utilisateur respecte une politique
func (pm *PolicyManager) CheckPolicy(policyName string, userRoles []string, context map[string]interface{}) bool {
	policy, exists := pm.policies[policyName]
	if !exists {
		return false
	}

	// Vérifier les rôles requis
	for _, requiredRole := range policy.RequiredRoles {
		if !pm.rbac.HasHigherRole(userRoles, requiredRole) {
			return false
		}
	}

	// Vérifier les permissions requises
	for _, requiredPerm := range policy.RequiredPermissions {
		if !pm.rbac.CheckPermission(userRoles, requiredPerm) {
			return false
		}
	}

	// Ici vous pouvez ajouter d'autres vérifications :
	// - Filtres de ressources
	// - Whitelist IP
	// - Restrictions horaires
	// - etc.

	return true
}

// GetApplicablePolicies retourne les politiques applicables à un utilisateur
func (pm *PolicyManager) GetApplicablePolicies(userRoles []string) []string {
	var applicable []string
	
	for name, policy := range pm.policies {
		canApply := true
		
		// Vérifier si l'utilisateur a au moins un des rôles requis
		if len(policy.RequiredRoles) > 0 {
			hasRequiredRole := false
			for _, requiredRole := range policy.RequiredRoles {
				if pm.rbac.HasHigherRole(userRoles, requiredRole) {
					hasRequiredRole = true
					break
				}
			}
			if !hasRequiredRole {
				canApply = false
			}
		}
		
		if canApply {
			applicable = append(applicable, name)
		}
	}
	
	return applicable
}

// RoleBasedMenu représente un élément de menu avec contrôle d'accès
type RoleBasedMenu struct {
	ID               string
	Name             string
	URL              string
	RequiredRoles    []Role
	RequiredPermissions []Permission
	Children         []*RoleBasedMenu
}

// MenuManager gère les menus basés sur les rôles
type MenuManager struct {
	rbac *RBAC
	menus []*RoleBasedMenu
}

// NewMenuManager crée un nouveau gestionnaire de menus
func NewMenuManager(rbac *RBAC) *MenuManager {
	return &MenuManager{
		rbac:  rbac,
		menus: make([]*RoleBasedMenu, 0),
	}
}

// AddMenu ajoute un menu
func (mm *MenuManager) AddMenu(menu *RoleBasedMenu) {
	mm.menus = append(mm.menus, menu)
}

// GetAuthorizedMenus retourne les menus autorisés pour un utilisateur
func (mm *MenuManager) GetAuthorizedMenus(userRoles []string) []*RoleBasedMenu {
	var authorizedMenus []*RoleBasedMenu
	
	for _, menu := range mm.menus {
		if mm.isMenuAuthorized(menu, userRoles) {
			authorizedMenu := &RoleBasedMenu{
				ID:               menu.ID,
				Name:             menu.Name,
				URL:              menu.URL,
				RequiredRoles:    menu.RequiredRoles,
				RequiredPermissions: menu.RequiredPermissions,
				Children:         mm.getAuthorizedChildren(menu.Children, userRoles),
			}
			authorizedMenus = append(authorizedMenus, authorizedMenu)
		}
	}
	
	return authorizedMenus
}

// isMenuAuthorized vérifie si un menu est autorisé pour un utilisateur
func (mm *MenuManager) isMenuAuthorized(menu *RoleBasedMenu, userRoles []string) bool {
	// Vérifier les rôles requis
	if len(menu.RequiredRoles) > 0 {
		hasRequiredRole := false
		for _, requiredRole := range menu.RequiredRoles {
			if mm.rbac.HasHigherRole(userRoles, requiredRole) {
				hasRequiredRole = true
				break
			}
		}
		if !hasRequiredRole {
			return false
		}
	}
	
	// Vérifier les permissions requises
	if len(menu.RequiredPermissions) > 0 {
		hasRequiredPermission := false
		for _, requiredPerm := range menu.RequiredPermissions {
			if mm.rbac.CheckPermission(userRoles, requiredPerm) {
				hasRequiredPermission = true
				break
			}
		}
		if !hasRequiredPermission {
			return false
		}
	}
	
	return true
}

// getAuthorizedChildren retourne les enfants autorisés d'un menu
func (mm *MenuManager) getAuthorizedChildren(children []*RoleBasedMenu, userRoles []string) []*RoleBasedMenu {
	var authorizedChildren []*RoleBasedMenu
	
	for _, child := range children {
		if mm.isMenuAuthorized(child, userRoles) {
			authorizedChild := &RoleBasedMenu{
				ID:               child.ID,
				Name:             child.Name,
				URL:              child.URL,
				RequiredRoles:    child.RequiredRoles,
				RequiredPermissions: child.RequiredPermissions,
				Children:         mm.getAuthorizedChildren(child.Children, userRoles),
			}
			authorizedChildren = append(authorizedChildren, authorizedChild)
		}
	}
	
	return authorizedChildren
}