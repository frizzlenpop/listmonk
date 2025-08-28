package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

       "github.com/gofrs/uuid/v5"
	"github.com/knadh/listmonk/internal/middleware"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// handleGetTenants returns all tenants (admin only).
func handleGetTenants(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		out = struct {
			Results []models.Tenant `json:"results"`
		}{}
	)

	// Only super admin can list all tenants
	// TODO: Add super admin check

	if err := app.queries.GetTenants.Select(&out.Results); err != nil {
		app.log.Printf("error fetching tenants: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching", "name", "tenants", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// handleGetTenant returns a single tenant by ID.
func handleGetTenant(c echo.Context) error {
	var (
		app     = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
		out     models.Tenant
	)

	// Users can only view their own tenant unless they're super admin
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	// TODO: Add super admin check
	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	if err := app.queries.GetTenant.Get(&out, tenantID); err != nil {
		app.log.Printf("error fetching tenant: %v", err)
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, "Tenant not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching", "name", "tenant", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// handleCreateTenant creates a new tenant.
func handleCreateTenant(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		req = struct {
			Name         string                 `json:"name" validate:"required,min=1,max=255"`
			Slug         string                 `json:"slug" validate:"required,min=1,max=100"`
			Domain       string                 `json:"domain"`
			Plan         string                 `json:"plan"`
			BillingEmail string                 `json:"billing_email"`
			Settings     map[string]interface{} `json:"settings"`
			Features     map[string]interface{} `json:"features"`
		}{}
	)

	// Only super admin can create tenants
	// TODO: Add super admin check

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := c.Validate(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Generate UUID
	tenantUUID, err := uuid.NewV4()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to generate UUID")
	}

	// Prepare tenant data
	var (
		settingsJSON = `{}`
		featuresJSON = `{"max_subscribers": 10000, "max_campaigns_per_month": 100}`
	)

	if req.Settings != nil {
		if b, err := json.Marshal(req.Settings); err == nil {
			settingsJSON = string(b)
		}
	}

	if req.Features != nil {
		if b, err := json.Marshal(req.Features); err == nil {
			featuresJSON = string(b)
		}
	}

	var out models.Tenant
	if err := app.queries.CreateTenant.Get(&out,
		tenantUUID.String(),
		req.Name,
		req.Slug,
		req.Domain,
		req.Plan,
		req.BillingEmail,
		settingsJSON,
		featuresJSON,
	); err != nil {
		app.log.Printf("error creating tenant: %v", err)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			if pqErr.Constraint == "tenants_slug_key" {
				return echo.NewHTTPError(http.StatusBadRequest, "Tenant slug already exists")
			}
			if pqErr.Constraint == "tenants_domain_key" {
				return echo.NewHTTPError(http.StatusBadRequest, "Domain already assigned to another tenant")
			}
		}
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorCreating", "name", "tenant", "error", pqErrMsg(err)))
	}

	// Create default tenant settings by copying from global settings
	if err := createDefaultTenantSettings(app, out.ID); err != nil {
		app.log.Printf("warning: failed to create default tenant settings: %v", err)
	}

	return c.JSON(http.StatusCreated, okResp{out})
}

// handleUpdateTenant updates a tenant.
func handleUpdateTenant(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
		req = struct {
			Name         string                 `json:"name" validate:"required,min=1,max=255"`
			Slug         string                 `json:"slug" validate:"required,min=1,max=100"`
			Domain       string                 `json:"domain"`
			Status       string                 `json:"status" validate:"required,oneof=active suspended deleted"`
			Plan         string                 `json:"plan"`
			BillingEmail string                 `json:"billing_email"`
			Settings     map[string]interface{} `json:"settings"`
			Features     map[string]interface{} `json:"features"`
		}{}
	)

	// Users can only update their own tenant unless they're super admin
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	// TODO: Add super admin check
	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := c.Validate(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Prepare JSON fields
	var (
		settingsJSON = `{}`
		featuresJSON = `{}`
	)

	if req.Settings != nil {
		if b, err := json.Marshal(req.Settings); err == nil {
			settingsJSON = string(b)
		}
	}

	if req.Features != nil {
		if b, err := json.Marshal(req.Features); err == nil {
			featuresJSON = string(b)
		}
	}

	var out models.Tenant
	if err := app.queries.UpdateTenant.Get(&out,
		tenantID,
		req.Name,
		req.Slug,
		req.Domain,
		req.Status,
		req.Plan,
		req.BillingEmail,
		settingsJSON,
		featuresJSON,
	); err != nil {
		app.log.Printf("error updating tenant: %v", err)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			if pqErr.Constraint == "tenants_slug_key" {
				return echo.NewHTTPError(http.StatusBadRequest, "Tenant slug already exists")
			}
			if pqErr.Constraint == "tenants_domain_key" {
				return echo.NewHTTPError(http.StatusBadRequest, "Domain already assigned to another tenant")
			}
		}
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorUpdating", "name", "tenant", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// handleDeleteTenant soft deletes a tenant.
func handleDeleteTenant(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
	)

	// Only super admin can delete tenants
	// TODO: Add super admin check

	if _, err := app.queries.DeleteTenant.Exec(tenantID); err != nil {
		app.log.Printf("error deleting tenant: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorDeleting", "name", "tenant", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{true})
}

// handleGetTenantStats returns statistics for a tenant.
func handleGetTenantStats(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
	)

	// Users can only view their own tenant stats unless they're super admin
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	// TODO: Add super admin check
	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Use tenant-aware core
	tenantCore := app.core.WithTenant(tenantID)
	stats, err := tenantCore.GetTenantStats()
	if err != nil {
		app.log.Printf("error fetching tenant stats: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching", "name", "stats", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{stats})
}

// handleGetUserTenants returns all tenants a user has access to.
func handleGetUserTenants(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		out = struct {
			Results []models.TenantUser `json:"results"`
		}{}
	)

	// Get user ID from session
	// TODO: Get actual user ID from session
	userID := 1 // Placeholder

	if err := app.queries.GetUserTenants.Select(&out.Results, userID); err != nil {
		app.log.Printf("error fetching user tenants: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching", "name", "tenants", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// handleAddUserToTenant adds a user to a tenant with a specific role.
func handleAddUserToTenant(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
		req = struct {
			UserID    int    `json:"user_id" validate:"required"`
			Role      string `json:"role" validate:"required,oneof=owner admin member viewer"`
			IsDefault bool   `json:"is_default"`
		}{}
	)

	// Only tenant owners/admins can add users
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Check if current user is owner/admin of this tenant
	if tenant.UserRole != models.TenantUserRoleOwner && tenant.UserRole != models.TenantUserRoleAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := c.Validate(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	var out models.TenantUser
	if err := app.queries.AddUserToTenant.Get(&out,
		req.UserID,
		tenantID,
		req.Role,
		req.IsDefault,
	); err != nil {
		app.log.Printf("error adding user to tenant: %v", err)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusBadRequest, "User is already a member of this tenant")
		}
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorCreating", "name", "tenant membership", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusCreated, okResp{out})
}

// handleRemoveUserFromTenant removes a user from a tenant.
func handleRemoveUserFromTenant(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
		userID,  _ = strconv.Atoi(c.Param("userId"))
	)

	// Only tenant owners/admins can remove users
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Check if current user is owner/admin of this tenant
	if tenant.UserRole != models.TenantUserRoleOwner && tenant.UserRole != models.TenantUserRoleAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	if _, err := app.queries.RemoveUserFromTenant.Exec(userID, tenantID); err != nil {
		app.log.Printf("error removing user from tenant: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorDeleting", "name", "tenant membership", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{true})
}

// handleSwitchTenant switches the user's active tenant.
func handleSwitchTenant(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
	)

	// Get user ID from session
	// TODO: Get actual user ID from session
	userID := 1 // Placeholder

	// Verify user has access to this tenant
	var count int
	if err := app.queries.CheckUserTenantAccess.Get(&count, userID, tenantID); err != nil {
		app.log.Printf("error checking tenant access: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to verify tenant access")
	}

	if count == 0 {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied to this tenant")
	}

	// Update session with new tenant ID
	// TODO: Update session with tenant ID
	// This would typically involve updating the session store or JWT token

	return c.JSON(http.StatusOK, okResp{map[string]interface{}{
		"tenant_id": tenantID,
		"message":   "Tenant switched successfully",
	}})
}

// handleGetTenantSettings returns settings for a tenant.
func handleGetTenantSettings(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
	)

	// Users can only view their own tenant settings unless they're super admin
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Use tenant-aware core
	tenantCore := app.core.WithTenant(tenantID)
	settings, err := tenantCore.GetSettings()
	if err != nil {
		app.log.Printf("error fetching tenant settings: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching", "name", "settings", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{settings})
}

// handleUpdateTenantSettings updates settings for a tenant.
func handleUpdateTenantSettings(c echo.Context) error {
	var (
		app      = c.Get("app").(*App)
		tenantID, _ = strconv.Atoi(c.Param("id"))
		req      map[string]interface{}
	)

	// Users can only update their own tenant settings unless they're super admin
	tenant, err := middleware.GetTenant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Tenant context required")
	}

	if tenant.ID != tenantID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Check if current user is owner/admin of this tenant
	if tenant.UserRole != models.TenantUserRoleOwner && tenant.UserRole != models.TenantUserRoleAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Use tenant-aware core
	tenantCore := app.core.WithTenant(tenantID)
	if err := tenantCore.UpdateSettings(req); err != nil {
		app.log.Printf("error updating tenant settings: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorUpdating", "name", "settings", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{true})
}

// createDefaultTenantSettings creates default settings for a new tenant by copying from global_settings.
func createDefaultTenantSettings(app *App, tenantID int) error {
	_, err := app.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, key, value)
		SELECT $1, key, value FROM global_settings
		ON CONFLICT (tenant_id, key) DO NOTHING
	`, tenantID)
	return err
}