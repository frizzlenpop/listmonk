# Multi-Tenancy Integration Patches

This file contains the exact changes needed to integrate multi-tenancy into the existing Listmonk codebase.

## 1. Update cmd/main.go

### Add tenant configuration to App struct:

```go
// Add to import section
import (
    // ... existing imports ...
    "github.com/knadh/listmonk/internal/middleware"
    "time"
    "net/http"
)

// Add to App struct (around line 35)
type App struct {
    cfg        *Config
    urlCfg     *UrlConfig
    fs         stuffbin.FileSystem
    db         *sqlx.DB
    queries    *models.Queries
    core       *core.Core
    manager    *manager.Manager
    messengers []manager.Messenger
    emailMsgr  manager.Messenger
    importer   *subimporter.Importer
    auth       *auth.Auth
    media      media.Store
    bounce     *bounce.Manager
    captcha    *captcha.Captcha
    i18n       *i18n.I18n
    pg         *paginator.Paginator
    events     *events.Events
    log        *log.Logger
    bufLog     *buflog.BufLog
    
    // ADD THESE LINES:
    tenantMiddleware *middleware.TenantMiddleware
    tenantEnabled    bool

    about         about
    fnOptinNotify func(models.Subscriber, []int) (int, error)
    // ... rest of struct unchanged
}
```

### Update environment variable loading (around line 123):

```go
// Load environment variables and merge into the loaded config.
if err := ko.Load(env.Provider("LISTMONK_", ".", func(s string) string {
    return strings.Replace(strings.ToLower(strings.TrimPrefix(s, "LISTMONK_")), "__", ".", -1)
}), nil); err != nil {
    lo.Fatalf("error loading config from env: %v", err)
}

// ADD THESE LINES AFTER THE ABOVE:
// Load tenant-specific environment variables
tenantEnvs := map[string]interface{}{
    "tenant.enabled":            getEnvBool("LISTMONK_TENANT_MODE", false),
    "tenant.strategy":           getEnvString("LISTMONK_TENANT_STRATEGY", "subdomain"),
    "tenant.default_tenant_id":  getEnvInt("LISTMONK_DEFAULT_TENANT_ID", 1),
    "tenant.domain_suffix":      getEnvString("LISTMONK_TENANT_DOMAIN_SUFFIX", ".listmonk.local"),
    "tenant.require_tenant":     getEnvBool("LISTMONK_TENANT_REQUIRE", false),
}

if err := ko.Load(confmap.Provider(tenantEnvs, "."), nil); err != nil {
    lo.Fatalf("error loading tenant config: %v", err)
}
```

### Add tenant initialization in main() function (around line 280, after app creation):

```go
// Start the update checker.
if ko.Bool("app.check_updates") {
    go app.checkUpdates(versionString, time.Hour*24)
}

// ADD THESE LINES BEFORE initHTTPServer:
// Initialize tenant middleware
app.tenantEnabled = ko.Bool("tenant.enabled")
if app.tenantEnabled {
    app.tenantMiddleware = middleware.NewTenantMiddleware(app.db, app.queries)
    lo.Println("Multi-tenancy mode enabled")
    
    // Ensure default tenant exists
    if err := ensureDefaultTenant(app, ko.Int("tenant.default_tenant_id")); err != nil {
        lo.Printf("Warning: Failed to ensure default tenant: %v", err)
    }
} else {
    lo.Println("Single-tenant mode (multi-tenancy disabled)")
}

// Start the app server.
srv := initHTTPServer(cfg, urlCfg, i18n, fs, app)
```

## 2. Update cmd/init.go

### Add tenant middleware to HTTP server (in initHTTPServer function):

Find this section (around line 10 in initHTTPServer):
```go
// Register app (*App) to be injected into all HTTP handlers.
srv.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
    return func(c echo.Context) error {
        c.Set("app", app)
        return next(c)
    }
})
```

### Replace with:
```go
// Register app (*App) to be injected into all HTTP handlers.
srv.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
    return func(c echo.Context) error {
        c.Set("app", app)
        return next(c)
    }
})

// ADD TENANT MIDDLEWARE HERE:
// Add tenant middleware if multi-tenancy is enabled
tenantMW := initTenantMiddleware(app)
srv.Use(tenantMW)
```

### Update initHTTPHandlers call to add tenant routes:

Find this line (around line 40):
```go
// Register all HTTP handlers.
initHTTPHandlers(srv, app)
```

### Replace with:
```go
// Register all HTTP handlers.
initHTTPHandlers(srv, app)

// Add tenant-specific routes if multi-tenancy is enabled
initTenantRoutes(srv, app)
```

## 3. Update cmd/handlers.go

### Add tenant-aware middleware for admin routes:

Add this function at the end of cmd/handlers.go:
```go
// tenantRequired middleware ensures tenant context is available
func tenantRequired(app *App) echo.MiddlewareFunc {
    if !app.tenantEnabled {
        // If multi-tenancy is disabled, this is a no-op
        return func(next echo.HandlerFunc) echo.HandlerFunc {
            return next
        }
    }

    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c echo.Context) error {
            tenant, err := middleware.GetTenant(c)
            if err != nil {
                return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
            }
            
            // Store tenant info for easy access
            c.Set("tenant_id", tenant.ID)
            c.Set("tenant_name", tenant.Name)
            
            return next(c)
        }
    }
}

// adminRequired is the existing admin middleware - this is just a placeholder
// The actual implementation should already exist in the codebase
func adminRequired(app *App) echo.MiddlewareFunc {
    // This should already exist in handlers.go
    // Just adding this comment to show where it fits
    return app.auth.AdminRequired()
}

// authRequired is the existing auth middleware - this is just a placeholder  
// The actual implementation should already exist in the codebase
func authRequired(app *App) echo.MiddlewareFunc {
    // This should already exist in handlers.go
    // Just adding this comment to show where it fits
    return app.auth.Middleware
}
```

## 4. Create enhanced handler wrapper

Add this to the end of cmd/handlers.go:
```go
// withTenantAwareCore wraps handlers to automatically provide tenant-scoped core
func withTenantAwareCore(handler func(echo.Context, *App, *core.Core) error) echo.HandlerFunc {
    return func(c echo.Context) error {
        app := c.Get("app").(*App)
        
        var coreInstance *core.Core
        
        if app.tenantEnabled {
            tenant, err := middleware.GetTenant(c)
            if err != nil {
                return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
            }
            coreInstance = app.core.WithTenant(tenant.ID)
        } else {
            coreInstance = app.core
        }

        return handler(c, app, coreInstance)
    }
}
```

## 5. Example of updating a handler (cmd/subscribers.go)

### Original handler pattern:
```go
func handleGetSubscribers(c echo.Context) error {
    var (
        app = c.Get("app").(*App)
        // ... other variables
    )
    
    // Query database directly
    if err := app.queries.GetSubscribers.Select(&out.Results, queryStr, //...
}
```

### Updated handler pattern:
```go
func handleGetSubscribers(c echo.Context) error {
    return withTenantAwareCore(func(c echo.Context, app *App, core *core.Core) error {
        // ... existing handler logic but using tenant-aware core
        // The core parameter is now automatically tenant-scoped
        
        // Use core methods instead of direct queries
        subscribers, err := core.GetSubscribers(queryStr, searchStr, listIDs, orderBy, order, offset, limit)
        // ... rest of handler logic
    })(c)
}
```

## 6. Required Import Additions

### cmd/main.go needs these imports:
```go
import (
    // ... existing imports ...
    "github.com/knadh/listmonk/internal/middleware"
    "github.com/knadh/koanf/providers/confmap" 
    "time"
    "net/http"
)
```

### cmd/handlers.go needs these imports:
```go
import (
    // ... existing imports ...
    "github.com/knadh/listmonk/internal/middleware"
    "github.com/knadh/listmonk/internal/core"
    "time"
)
```

## 7. Docker Environment Variables

### Add to docker-compose.yml:
```yaml
environment:
  # Multi-tenancy configuration
  - LISTMONK_TENANT_MODE=true
  - LISTMONK_TENANT_STRATEGY=subdomain
  - LISTMONK_DEFAULT_TENANT_ID=1
  - LISTMONK_TENANT_DOMAIN_SUFFIX=.listmonk.yourdomain.com
  - LISTMONK_TENANT_REQUIRE=false
```

## 8. Database Initialization

The application will automatically:
1. Check if default tenant exists
2. Create default tenant if missing
3. Copy global settings to tenant settings
4. Set up tenant context for all operations

## Testing the Integration

1. Deploy with `LISTMONK_TENANT_MODE=false` - should work as single-tenant (backward compatible)
2. Deploy with `LISTMONK_TENANT_MODE=true` - should enable multi-tenancy
3. Test tenant health check: `GET /api/health/tenants`
4. Test tenant management: `GET /api/tenants` (admin only)

## Priority Implementation Order

1. Apply changes to cmd/main.go and cmd/init.go first
2. Add the tenant_integration.go file 
3. Update handlers.go with middleware functions
4. Test basic tenant context with health check
5. Gradually update individual handlers to use withTenantAwareCore wrapper

This approach ensures backward compatibility while enabling multi-tenancy when configured.