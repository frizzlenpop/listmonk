package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

const (
	// TenantCtxKey is the key used to store tenant context in echo.Context.
	TenantCtxKey = "tenant_context"
	
	// TenantHeaderKey is the HTTP header for tenant identification.
	TenantHeaderKey = "X-Tenant-ID"
	
	// TenantSubdomainKey is used for subdomain-based tenant resolution.
	TenantSubdomainSuffix = ".listmonk.local" // Change this to your domain
)

var (
	// ErrTenantNotFound is returned when a tenant cannot be resolved.
	ErrTenantNotFound = errors.New("tenant not found")
	
	// ErrTenantInactive is returned when a tenant is not active.
	ErrTenantInactive = errors.New("tenant is inactive")
	
	// ErrTenantAccessDenied is returned when a user doesn't have access to a tenant.
	ErrTenantAccessDenied = errors.New("access denied to this tenant")
)

// TenantResolver is an interface for resolving tenants from requests.
type TenantResolver interface {
	ResolveTenant(c echo.Context) (*models.TenantContext, error)
}

// TenantMiddleware provides tenant context for multi-tenant operations.
type TenantMiddleware struct {
	db       *sqlx.DB
	queries  *models.Queries
	resolver TenantResolver
}

// NewTenantMiddleware creates a new tenant middleware instance.
func NewTenantMiddleware(db *sqlx.DB, queries *models.Queries) *TenantMiddleware {
	tm := &TenantMiddleware{
		db:      db,
		queries: queries,
	}
	// Set self as default resolver
	tm.resolver = tm
	return tm
}

// Middleware returns the Echo middleware function.
func (tm *TenantMiddleware) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Resolve tenant from request
			tenant, err := tm.resolver.ResolveTenant(c)
			if err != nil {
				if err == ErrTenantNotFound {
					return echo.NewHTTPError(http.StatusBadRequest, "Tenant not found")
				}
				if err == ErrTenantInactive {
					return echo.NewHTTPError(http.StatusForbidden, "Tenant is inactive")
				}
				if err == ErrTenantAccessDenied {
					return echo.NewHTTPError(http.StatusForbidden, "Access denied to this tenant")
				}
				return echo.NewHTTPError(http.StatusInternalServerError, "Failed to resolve tenant")
			}

			// Store tenant context in Echo context
			c.Set(TenantCtxKey, tenant)

			// Set tenant context in database session for RLS
			if err := tm.SetDatabaseTenant(tenant.ID); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "Failed to set tenant context")
			}

			// Add tenant info to response headers for debugging (optional)
			c.Response().Header().Set("X-Tenant-ID", strconv.Itoa(tenant.ID))
			c.Response().Header().Set("X-Tenant-Slug", tenant.Slug)

			return next(c)
		}
	}
}

// ResolveTenant implements the default tenant resolution strategy.
func (tm *TenantMiddleware) ResolveTenant(c echo.Context) (*models.TenantContext, error) {
	var tenant *models.Tenant
	var err error

	// Strategy 1: Check subdomain
	host := c.Request().Host
	if strings.Contains(host, ".") {
		parts := strings.Split(host, ".")
		if len(parts) > 2 {
			subdomain := parts[0]
			tenant, err = tm.GetTenantBySlug(subdomain)
			if err == nil && tenant != nil {
				return tm.buildTenantContext(c, tenant)
			}
		}
	}

	// Strategy 2: Check custom domain
	if tenant == nil {
		domain := strings.Split(host, ":")[0] // Remove port if present
		tenant, err = tm.GetTenantByDomain(domain)
		if err == nil && tenant != nil {
			return tm.buildTenantContext(c, tenant)
		}
	}

	// Strategy 3: Check X-Tenant-ID header
	if tenantHeader := c.Request().Header.Get(TenantHeaderKey); tenantHeader != "" {
		if tenantID, err := strconv.Atoi(tenantHeader); err == nil {
			tenant, err = tm.GetTenantByID(tenantID)
			if err == nil && tenant != nil {
				return tm.buildTenantContext(c, tenant)
			}
		}
	}

	// Strategy 4: Check query parameter (useful for development)
	if tenantParam := c.QueryParam("tenant"); tenantParam != "" {
		tenant, err = tm.GetTenantBySlug(tenantParam)
		if err == nil && tenant != nil {
			return tm.buildTenantContext(c, tenant)
		}
	}

	// Strategy 5: Get user's default tenant from session
	if session := GetUserSession(c); session != nil && session.UserID > 0 {
		tenant, err = tm.GetUserDefaultTenant(session.UserID)
		if err == nil && tenant != nil {
			return tm.buildTenantContext(c, tenant)
		}
	}

	// Strategy 6: Fall back to default tenant (ID: 1) for backward compatibility
	// Remove this in production for strict multi-tenancy
	if tenant == nil {
		tenant, err = tm.GetTenantByID(1)
		if err == nil && tenant != nil {
			return tm.buildTenantContext(c, tenant)
		}
	}

	return nil, ErrTenantNotFound
}

// buildTenantContext creates a TenantContext from a Tenant model.
func (tm *TenantMiddleware) buildTenantContext(c echo.Context, tenant *models.Tenant) (*models.TenantContext, error) {
	// Check if tenant is active
	if !tenant.IsActive() {
		return nil, ErrTenantInactive
	}

	// Get user's role in this tenant if authenticated
	var userRole string = models.TenantUserRoleViewer // Default role
	if session := GetUserSession(c); session != nil && session.UserID > 0 {
		role, err := tm.GetUserTenantRole(session.UserID, tenant.ID)
		if err != nil {
			// User doesn't have access to this tenant
			return nil, ErrTenantAccessDenied
		}
		userRole = role
	}

	// Parse features
	var features models.TenantFeatures
	if err := tenant.Features.Unmarshal(&features); err != nil {
		// Use default features if parsing fails
		features = models.TenantFeatures{
			MaxSubscribers:       10000,
			MaxCampaignsPerMonth: 100,
			MaxLists:            50,
			MaxTemplates:        20,
			MaxUsers:            10,
			CustomDomain:        false,
			APIAccess:           true,
			WebhooksEnabled:     true,
			AdvancedAnalytics:   false,
		}
	}

	return &models.TenantContext{
		ID:       tenant.ID,
		UUID:     tenant.UUID,
		Name:     tenant.Name,
		Slug:     tenant.Slug,
		Status:   tenant.Status,
		Settings: tenant.Settings,
		Features: &features,
		UserRole: userRole,
	}, nil
}

// SetDatabaseTenant sets the current tenant in the database session for RLS.
func (tm *TenantMiddleware) SetDatabaseTenant(tenantID int) error {
	query := fmt.Sprintf("SELECT set_config('app.current_tenant', '%d', false)", tenantID)
	_, err := tm.db.Exec(query)
	return err
}

// GetTenantByID retrieves a tenant by ID.
func (tm *TenantMiddleware) GetTenantByID(id int) (*models.Tenant, error) {
	var tenant models.Tenant
	err := tm.db.Get(&tenant, `
		SELECT * FROM tenants WHERE id = $1 AND status != 'deleted'
	`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &tenant, nil
}

// GetTenantBySlug retrieves a tenant by slug.
func (tm *TenantMiddleware) GetTenantBySlug(slug string) (*models.Tenant, error) {
	var tenant models.Tenant
	err := tm.db.Get(&tenant, `
		SELECT * FROM tenants WHERE slug = $1 AND status != 'deleted'
	`, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &tenant, nil
}

// GetTenantByDomain retrieves a tenant by custom domain.
func (tm *TenantMiddleware) GetTenantByDomain(domain string) (*models.Tenant, error) {
	var tenant models.Tenant
	err := tm.db.Get(&tenant, `
		SELECT * FROM tenants WHERE domain = $1 AND status != 'deleted'
	`, domain)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &tenant, nil
}

// GetUserDefaultTenant retrieves the default tenant for a user.
func (tm *TenantMiddleware) GetUserDefaultTenant(userID int) (*models.Tenant, error) {
	var tenant models.Tenant
	err := tm.db.Get(&tenant, `
		SELECT t.* FROM tenants t
		JOIN user_tenants ut ON t.id = ut.tenant_id
		WHERE ut.user_id = $1 AND ut.is_default = true AND t.status != 'deleted'
		LIMIT 1
	`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &tenant, nil
}

// GetUserTenantRole retrieves the user's role in a specific tenant.
func (tm *TenantMiddleware) GetUserTenantRole(userID, tenantID int) (string, error) {
	var role string
	err := tm.db.Get(&role, `
		SELECT role FROM user_tenants 
		WHERE user_id = $1 AND tenant_id = $2
	`, userID, tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrTenantAccessDenied
		}
		return "", err
	}
	return role, nil
}

// GetUserSession retrieves the user session from the context.
// This integrates with Listmonk's existing auth system.
func GetUserSession(c echo.Context) *UserSession {
	// Import the auth package to access the User type and context key
	user := c.Get("auth_user") // Using the auth.UserHTTPCtxKey value
	if user == nil {
		return nil
	}

	// Handle the case where auth middleware sets an error
	if _, isError := user.(*echo.HTTPError); isError {
		return nil
	}

	// Cast to the auth.User type - we need to use interface{} and type assertion
	// since we can't import auth package in middleware without circular dependency
	if userObj, ok := user.(interface{}); ok {
		// Use reflection or interface methods to extract user info
		// This is a simplified approach - in a real implementation you might
		// want to define an interface that auth.User implements
		
		// For now, we'll try to extract common fields using type assertion
		// This assumes the User struct has exported fields
		session := extractUserSession(userObj)
		return session
	}

	return nil
}

// extractUserSession extracts user information from the auth.User object using reflection
// This uses reflection to avoid circular import dependencies
func extractUserSession(user interface{}) *UserSession {
	if user == nil {
		return nil
	}
	
	v := reflect.ValueOf(user)
	
	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	
	// Ensure we have a struct
	if v.Kind() != reflect.Struct {
		return nil
	}
	
	session := &UserSession{}
	
	// Extract ID field
	if idField := v.FieldByName("ID"); idField.IsValid() && idField.CanInterface() {
		if id, ok := idField.Interface().(int); ok {
			session.UserID = id
		}
	}
	
	// Extract Username field
	if usernameField := v.FieldByName("Username"); usernameField.IsValid() && usernameField.CanInterface() {
		if username, ok := usernameField.Interface().(string); ok {
			session.Username = username
		}
	}
	
	// Extract Email field (it might be a null.String in Listmonk)
	if emailField := v.FieldByName("Email"); emailField.IsValid() && emailField.CanInterface() {
		emailValue := emailField.Interface()
		
		// Handle null.String type
		if emailStruct := reflect.ValueOf(emailValue); emailStruct.Kind() == reflect.Struct {
			if validField := emailStruct.FieldByName("Valid"); validField.IsValid() && validField.Kind() == reflect.Bool {
				if validField.Bool() {
					if stringField := emailStruct.FieldByName("String"); stringField.IsValid() && stringField.Kind() == reflect.String {
						session.Email = stringField.String()
					}
				}
			}
		} else if email, ok := emailValue.(string); ok {
			session.Email = email
		}
	}
	
	// Only return session if we got a valid user ID
	if session.UserID > 0 {
		return session
	}
	
	return nil
}

// UserSession represents a user's session data.
type UserSession struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// GetTenant retrieves the tenant context from Echo context.
func GetTenant(c echo.Context) (*models.TenantContext, error) {
	tenant, ok := c.Get(TenantCtxKey).(*models.TenantContext)
	if !ok || tenant == nil {
		return nil, errors.New("tenant context not found")
	}
	return tenant, nil
}

// RequireTenantRole returns a middleware that requires a minimum tenant role.
func RequireTenantRole(minRole string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tenant, err := GetTenant(c)
			if err != nil {
				return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
			}

			roleHierarchy := map[string]int{
				models.TenantUserRoleOwner:  4,
				models.TenantUserRoleAdmin:  3,
				models.TenantUserRoleMember: 2,
				models.TenantUserRoleViewer: 1,
			}

			userLevel, userOk := roleHierarchy[tenant.UserRole]
			requiredLevel, reqOk := roleHierarchy[minRole]

			if !userOk || !reqOk || userLevel < requiredLevel {
				return echo.NewHTTPError(http.StatusForbidden, 
					fmt.Sprintf("Insufficient permissions. Required: %s, Current: %s", minRole, tenant.UserRole))
			}

			return next(c)
		}
	}
}

// WithTenantContext adds tenant ID to the SQL context for queries.
func WithTenantContext(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, "tenant_id", tenantID)
}

// GetTenantID retrieves the tenant ID from context.
func GetTenantID(ctx context.Context) (int, bool) {
	tenantID, ok := ctx.Value("tenant_id").(int)
	return tenantID, ok
}