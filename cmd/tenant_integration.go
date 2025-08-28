package main

import (
	"database/sql"
	"os"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/middleware"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// TenantConfig holds tenant-specific configuration
type TenantConfig struct {
	Enabled         bool   `koanf:"enabled"`
	Strategy        string `koanf:"strategy"`        // subdomain, domain, header, query
	DefaultTenantID int    `koanf:"default_tenant"`  // fallback tenant ID
	DomainSuffix    string `koanf:"domain_suffix"`   // for subdomain resolution
	RequireTenant   bool   `koanf:"require_tenant"`  // strict tenant enforcement
}

// initTenantMiddleware initializes and configures tenant middleware
func initTenantMiddleware(app *App) echo.MiddlewareFunc {
	// Load tenant configuration from environment/config
	tenantConfig := loadTenantConfig()
	
	// If multi-tenancy is disabled, return a no-op middleware
	if !tenantConfig.Enabled {
		app.log.Println("Multi-tenancy is disabled")
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				// Set default tenant context for backward compatibility
				defaultTenant := &models.TenantContext{
					ID:   1, // Default tenant
					Name: "Default",
					Slug: "default",
					UserRole: models.TenantUserRoleAdmin,
				}
				c.Set(middleware.TenantCtxKey, defaultTenant)
				return next(c)
			}
		}
	}

	app.log.Println("Initializing multi-tenant middleware")
	
	// Initialize tenant middleware with database access
	tenantMiddleware := middleware.NewTenantMiddleware(app.db, app.queries)
	
	// Create default tenant if it doesn't exist
	if err := ensureDefaultTenant(app, tenantConfig.DefaultTenantID); err != nil {
		app.log.Printf("Warning: Failed to ensure default tenant: %v", err)
	}

	// Configure tenant resolution strategy
	configureTenantStrategy(tenantMiddleware, tenantConfig)

	app.log.Printf("Multi-tenancy enabled with strategy: %s", tenantConfig.Strategy)
	
	return tenantMiddleware.Middleware()
}

// loadTenantConfig loads tenant configuration from environment variables and config
func loadTenantConfig() TenantConfig {
	config := TenantConfig{
		Enabled:         getEnvBool("LISTMONK_TENANT_MODE", false),
		Strategy:        getEnvString("LISTMONK_TENANT_STRATEGY", "subdomain"),
		DefaultTenantID: getEnvInt("LISTMONK_DEFAULT_TENANT_ID", 1),
		DomainSuffix:    getEnvString("LISTMONK_TENANT_DOMAIN_SUFFIX", ".listmonk.local"),
		RequireTenant:   getEnvBool("LISTMONK_TENANT_REQUIRE", false),
	}

	// Also check koanf config if available
	if ko != nil {
		if ko.Exists("tenant.enabled") {
			config.Enabled = ko.Bool("tenant.enabled")
		}
		if ko.Exists("tenant.strategy") {
			config.Strategy = ko.String("tenant.strategy")
		}
		if ko.Exists("tenant.default_tenant_id") {
			config.DefaultTenantID = ko.Int("tenant.default_tenant_id")
		}
		if ko.Exists("tenant.domain_suffix") {
			config.DomainSuffix = ko.String("tenant.domain_suffix")
		}
		if ko.Exists("tenant.require_tenant") {
			config.RequireTenant = ko.Bool("tenant.require_tenant")
		}
	}

	return config
}

// ensureDefaultTenant creates the default tenant if it doesn't exist
func ensureDefaultTenant(app *App, defaultTenantID int) error {
	// Check if default tenant exists
	var count int
	err := app.db.Get(&count, "SELECT COUNT(*) FROM tenants WHERE id = $1", defaultTenantID)
	if err != nil {
		return err
	}

	if count > 0 {
		app.log.Printf("Default tenant (ID: %d) already exists", defaultTenantID)
		return nil
	}

	// Create default tenant
	app.log.Printf("Creating default tenant (ID: %d)", defaultTenantID)
	
	_, err = app.db.Exec(`
		INSERT INTO tenants (id, uuid, name, slug, status, settings, features, created_at, updated_at) 
		VALUES ($1, gen_random_uuid(), 'Default Tenant', 'default', 'active', 
				'{"migrated_from_single_tenant": true}'::jsonb,
				'{"max_subscribers": 1000000, "max_campaigns_per_month": 10000}'::jsonb,
				NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, defaultTenantID)

	if err != nil {
		return err
	}

	// Also ensure sequence is at least at the default tenant ID
	_, err = app.db.Exec("SELECT setval('tenants_id_seq', GREATEST($1, (SELECT MAX(id) FROM tenants)))", defaultTenantID)
	if err != nil {
		app.log.Printf("Warning: Could not update tenant sequence: %v", err)
	}

	// Create default tenant settings by copying from global settings
	err = createDefaultTenantSettings(app, defaultTenantID)
	if err != nil {
		app.log.Printf("Warning: Could not create default tenant settings: %v", err)
	}

	app.log.Printf("Successfully created default tenant (ID: %d)", defaultTenantID)
	return nil
}

// configureTenantStrategy configures the tenant resolution strategy
func configureTenantStrategy(tm *middleware.TenantMiddleware, config TenantConfig) {
	// The middleware already has multiple strategies built-in:
	// 1. Subdomain resolution
	// 2. Custom domain resolution  
	// 3. Header-based resolution
	// 4. Query parameter resolution (dev only)
	// 5. User default tenant from session

	// The middleware will automatically try all strategies in order
	// No additional configuration needed as the middleware handles this
}

// initTenantRoutes adds tenant-specific API routes
func initTenantRoutes(e *echo.Echo, app *App) {
	// Only add tenant management routes if multi-tenancy is enabled
	tenantConfig := loadTenantConfig()
	if !tenantConfig.Enabled {
		return
	}

	app.log.Println("Registering tenant management routes")

	// Public tenant switching for authenticated users
	userGroup := e.Group("/api/user")
	userGroup.Use(authRequired(app)) // Existing auth middleware
	userGroup.GET("/tenants", handleGetUserTenants)
	userGroup.POST("/tenants/switch/:id", handleSwitchTenant)

	// Admin-only tenant management routes
	adminGroup := e.Group("/api/tenants")
	adminGroup.Use(authRequired(app))     // Existing auth middleware
	adminGroup.Use(adminRequired(app))    // Existing admin middleware if available
	adminGroup.GET("", handleGetTenants)
	adminGroup.POST("", handleCreateTenant)
	adminGroup.GET("/:id", handleGetTenant)
	adminGroup.PUT("/:id", handleUpdateTenant)
	adminGroup.DELETE("/:id", handleDeleteTenant)
	adminGroup.GET("/:id/stats", handleGetTenantStats)
	adminGroup.GET("/:id/settings", handleGetTenantSettings)
	adminGroup.PUT("/:id/settings", handleUpdateTenantSettings)
	adminGroup.POST("/:id/users", handleAddUserToTenant)
	adminGroup.DELETE("/:id/users/:userId", handleRemoveUserFromTenant)

	// Health check endpoint that validates tenant context
	e.GET("/api/health/tenants", handleTenantHealthCheck)
}

// handleTenantHealthCheck provides tenant-specific health checking
func handleTenantHealthCheck(c echo.Context) error {
	app := c.Get("app").(*App)
	
	health := map[string]interface{}{
		"tenant_mode": loadTenantConfig().Enabled,
		"timestamp":   time.Now(),
	}

	// If tenant mode is enabled, validate tenant context
	if loadTenantConfig().Enabled {
		tenant, err := middleware.GetTenant(c)
		if err != nil {
			health["tenant_context"] = "missing"
			health["error"] = err.Error()
			return c.JSON(http.StatusServiceUnavailable, health)
		}

		health["tenant_context"] = "ok"
		health["tenant_id"] = tenant.ID
		health["tenant_name"] = tenant.Name

		// Test database access with tenant context
		var count int
		err = app.db.Get(&count, "SELECT COUNT(*) FROM subscribers WHERE tenant_id = $1", tenant.ID)
		if err != nil {
			health["database"] = "error"
			health["db_error"] = err.Error()
			return c.JSON(http.StatusServiceUnavailable, health)
		}

		health["database"] = "ok"
		health["tenant_subscribers"] = count
	}

	return c.JSON(http.StatusOK, health)
}

// Utility functions for environment variable handling
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on", "enabled":
			return true
		case "false", "0", "no", "off", "disabled":
			return false
		}
	}
	return defaultValue
}

// createDefaultTenantSettings creates default settings for a tenant
func createDefaultTenantSettings(app *App, tenantID int) error {
	// Copy settings from global_settings to tenant_settings for the default tenant
	_, err := app.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, key, value, updated_at)
		SELECT $1, key, value, NOW() FROM global_settings
		ON CONFLICT (tenant_id, key) DO NOTHING
	`, tenantID)
	return err
}

// Enhanced core factory that returns tenant-aware core when needed
func getTenantAwareCore(c echo.Context, app *App) (*core.Core, error) {
	// If multi-tenancy is disabled, return regular core
	tenantConfig := loadTenantConfig()
	if !tenantConfig.Enabled {
		return app.core, nil
	}

	// Get tenant context
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	// Return tenant-scoped core
	return app.core.WithTenant(tenant.ID), nil
}

// Wrapper function for handlers that need tenant-aware core
func withTenantCore(handler func(echo.Context, *App, *core.Core) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		app := c.Get("app").(*App)
		
		core, err := getTenantAwareCore(c, app)
		if err != nil {
			return err
		}

		return handler(c, app, core)
	}
}