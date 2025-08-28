package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
)

// TenantCore wraps the Core struct to provide tenant-aware operations.
type TenantCore struct {
	*Core
	tenantID int
	db       *sqlx.DB
}

// NewTenantCore creates a new tenant-aware Core instance.
func NewTenantCore(core *Core, tenantID int, db *sqlx.DB) *TenantCore {
	return &TenantCore{
		Core:     core,
		tenantID: tenantID,
		db:       db,
	}
}

// WithTenant creates a tenant-scoped Core instance.
func (c *Core) WithTenant(tenantID int) *TenantCore {
	// Set the database session variable for RLS
	c.db.MustExec(fmt.Sprintf("SELECT set_config('app.current_tenant', '%d', false)", tenantID))
	
	return &TenantCore{
		Core:     c,
		tenantID: tenantID,
		db:       c.db,
	}
}

// GetTenantID returns the current tenant ID.
func (tc *TenantCore) GetTenantID() int {
	return tc.tenantID
}

// ensureTenantContext ensures all database operations are tenant-scoped.
func (tc *TenantCore) ensureTenantContext() error {
	_, err := tc.db.Exec(fmt.Sprintf("SELECT set_config('app.current_tenant', '%d', false)", tc.tenantID))
	return err
}

// Tenant-aware wrapper methods for Subscribers

// GetSubscriber retrieves a subscriber by ID, ensuring it belongs to the current tenant.
func (tc *TenantCore) GetSubscriber(id int, subUUID string) (models.Subscriber, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Subscriber{}, err
	}

	// Add tenant check to the query
	var sub models.Subscriber
	query := `SELECT * FROM subscribers WHERE tenant_id = $1 AND (id = $2 OR uuid = $3)`
	err := tc.db.Get(&sub, query, tc.tenantID, id, subUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			return models.Subscriber{}, ErrNotFound
		}
		return models.Subscriber{}, err
	}
	return sub, nil
}

// GetSubscribers retrieves subscribers for the current tenant.
func (tc *TenantCore) GetSubscribers(query string, searchStr string, listIDs []int, orderBy string, order string, offset int, limit int) ([]models.Subscriber, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	// The query should automatically be filtered by tenant_id through RLS
	// But we can add explicit filtering for safety
	baseQuery := query
	if baseQuery == "" {
		baseQuery = "tenant_id = $tenant_id"
	} else {
		baseQuery = fmt.Sprintf("(%s) AND tenant_id = $tenant_id", baseQuery)
	}

	// Call the original method with tenant filtering
	return tc.Core.GetSubscribers(baseQuery, searchStr, listIDs, orderBy, order, offset, limit)
}

// CreateSubscriber creates a new subscriber for the current tenant.
func (tc *TenantCore) CreateSubscriber(sub models.Subscriber, lists []int, listUUIDs []string, preconfirm bool) (models.Subscriber, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Subscriber{}, err
	}

	// Check tenant limits
	if err := tc.checkSubscriberLimit(); err != nil {
		return models.Subscriber{}, err
	}

	// Ensure lists belong to the current tenant
	if err := tc.validateListOwnership(lists, listUUIDs); err != nil {
		return models.Subscriber{}, err
	}

	// The tenant_id will be set automatically through database triggers or we can set it explicitly
	// For safety, we should modify the query to include tenant_id
	return tc.Core.CreateSubscriber(sub, lists, listUUIDs, preconfirm)
}

// Tenant-aware wrapper methods for Lists

// GetLists retrieves lists for the current tenant.
func (tc *TenantCore) GetLists(searchStr string, orderBy string, order string, offset int, limit int) ([]models.List, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	// Lists will be automatically filtered by tenant through RLS
	return tc.Core.GetLists(searchStr, orderBy, order, offset, limit)
}

// GetList retrieves a list by ID, ensuring it belongs to the current tenant.
func (tc *TenantCore) GetList(id int, uuid string) (models.List, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.List{}, err
	}

	var list models.List
	query := `SELECT * FROM lists WHERE tenant_id = $1 AND (id = $2 OR uuid = $3)`
	err := tc.db.Get(&list, query, tc.tenantID, id, uuid)
	if err != nil {
		if err == sql.ErrNoRows {
			return models.List{}, ErrNotFound
		}
		return models.List{}, err
	}
	return list, nil
}

// CreateList creates a new list for the current tenant.
func (tc *TenantCore) CreateList(list models.List) (models.List, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.List{}, err
	}

	// Check tenant limits
	if err := tc.checkListLimit(); err != nil {
		return models.List{}, err
	}

	return tc.Core.CreateList(list)
}

// Tenant-aware wrapper methods for Campaigns

// GetCampaigns retrieves campaigns for the current tenant.
func (tc *TenantCore) GetCampaigns(searchStr string, status []string, orderBy string, order string, offset int, limit int) ([]models.Campaign, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	// Campaigns will be automatically filtered by tenant through RLS
	return tc.Core.GetCampaigns(searchStr, status, orderBy, order, offset, limit)
}

// GetCampaign retrieves a campaign by ID, ensuring it belongs to the current tenant.
func (tc *TenantCore) GetCampaign(id int, uuid string) (models.Campaign, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Campaign{}, err
	}

	var campaign models.Campaign
	query := `SELECT * FROM campaigns WHERE tenant_id = $1 AND (id = $2 OR uuid = $3)`
	err := tc.db.Get(&campaign, query, tc.tenantID, id, uuid)
	if err != nil {
		if err == sql.ErrNoRows {
			return models.Campaign{}, ErrNotFound
		}
		return models.Campaign{}, err
	}
	return campaign, nil
}

// CreateCampaign creates a new campaign for the current tenant.
func (tc *TenantCore) CreateCampaign(campaign models.Campaign, listIDs []int) (models.Campaign, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Campaign{}, err
	}

	// Check tenant limits
	if err := tc.checkCampaignLimit(); err != nil {
		return models.Campaign{}, err
	}

	// Ensure lists belong to the current tenant
	if err := tc.validateListOwnership(listIDs, nil); err != nil {
		return models.Campaign{}, err
	}

	return tc.Core.CreateCampaign(campaign, listIDs)
}

// Tenant-aware wrapper methods for Templates

// GetTemplates retrieves templates for the current tenant.
func (tc *TenantCore) GetTemplates(searchStr string, orderBy string, order string, offset int, limit int) ([]models.Template, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	// Templates will be automatically filtered by tenant through RLS
	return tc.Core.GetTemplates(searchStr, orderBy, order, offset, limit)
}

// GetTemplate retrieves a template by ID, ensuring it belongs to the current tenant.
func (tc *TenantCore) GetTemplate(id int) (models.Template, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Template{}, err
	}

	var template models.Template
	query := `SELECT * FROM templates WHERE tenant_id = $1 AND id = $2`
	err := tc.db.Get(&template, query, tc.tenantID, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return models.Template{}, ErrNotFound
		}
		return models.Template{}, err
	}
	return template, nil
}

// CreateTemplate creates a new template for the current tenant.
func (tc *TenantCore) CreateTemplate(template models.Template) (models.Template, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return models.Template{}, err
	}

	// Check tenant limits
	if err := tc.checkTemplateLimit(); err != nil {
		return models.Template{}, err
	}

	return tc.Core.CreateTemplate(template)
}

// Tenant-aware settings management

// GetSettings retrieves settings for the current tenant.
func (tc *TenantCore) GetSettings() (map[string]interface{}, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	settings := make(map[string]interface{})
	rows, err := tc.db.Query(`
		SELECT key, value FROM tenant_settings 
		WHERE tenant_id = $1
	`, tc.tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		// Parse JSON value
		var val interface{}
		if err := json.Unmarshal(value, &val); err != nil {
			settings[key] = string(value)
		} else {
			settings[key] = val
		}
	}

	return settings, nil
}

// UpdateSettings updates settings for the current tenant.
func (tc *TenantCore) UpdateSettings(settings map[string]interface{}) error {
	if err := tc.ensureTenantContext(); err != nil {
		return err
	}

	tx, err := tc.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for key, value := range settings {
		valueJSON, err := json.Marshal(value)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`
			INSERT INTO tenant_settings (tenant_id, key, value, updated_at) 
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (tenant_id, key) 
			DO UPDATE SET value = $3, updated_at = NOW()
		`, tc.tenantID, key, valueJSON)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Helper methods for tenant limits

// checkSubscriberLimit checks if the tenant can add more subscribers.
func (tc *TenantCore) checkSubscriberLimit() error {
	var count int
	err := tc.db.Get(&count, `SELECT COUNT(*) FROM subscribers WHERE tenant_id = $1`, tc.tenantID)
	if err != nil {
		return err
	}

	// Get tenant features
	tenant, err := tc.getTenant()
	if err != nil {
		return err
	}

	var features models.TenantFeatures
	if err := tenant.Features.Unmarshal(&features); err != nil {
		return nil // No limits if features can't be parsed
	}

	if features.MaxSubscribers > 0 && count >= features.MaxSubscribers {
		return fmt.Errorf("subscriber limit reached (%d/%d)", count, features.MaxSubscribers)
	}

	return nil
}

// checkListLimit checks if the tenant can add more lists.
func (tc *TenantCore) checkListLimit() error {
	var count int
	err := tc.db.Get(&count, `SELECT COUNT(*) FROM lists WHERE tenant_id = $1`, tc.tenantID)
	if err != nil {
		return err
	}

	tenant, err := tc.getTenant()
	if err != nil {
		return err
	}

	var features models.TenantFeatures
	if err := tenant.Features.Unmarshal(&features); err != nil {
		return nil
	}

	if features.MaxLists > 0 && count >= features.MaxLists {
		return fmt.Errorf("list limit reached (%d/%d)", count, features.MaxLists)
	}

	return nil
}

// checkCampaignLimit checks if the tenant can create more campaigns this month.
func (tc *TenantCore) checkCampaignLimit() error {
	var count int
	err := tc.db.Get(&count, `
		SELECT COUNT(*) FROM campaigns 
		WHERE tenant_id = $1 
		AND created_at >= date_trunc('month', CURRENT_DATE)
	`, tc.tenantID)
	if err != nil {
		return err
	}

	tenant, err := tc.getTenant()
	if err != nil {
		return err
	}

	var features models.TenantFeatures
	if err := tenant.Features.Unmarshal(&features); err != nil {
		return nil
	}

	if features.MaxCampaignsPerMonth > 0 && count >= features.MaxCampaignsPerMonth {
		return fmt.Errorf("monthly campaign limit reached (%d/%d)", count, features.MaxCampaignsPerMonth)
	}

	return nil
}

// checkTemplateLimit checks if the tenant can add more templates.
func (tc *TenantCore) checkTemplateLimit() error {
	var count int
	err := tc.db.Get(&count, `SELECT COUNT(*) FROM templates WHERE tenant_id = $1`, tc.tenantID)
	if err != nil {
		return err
	}

	tenant, err := tc.getTenant()
	if err != nil {
		return err
	}

	var features models.TenantFeatures
	if err := tenant.Features.Unmarshal(&features); err != nil {
		return nil
	}

	if features.MaxTemplates > 0 && count >= features.MaxTemplates {
		return fmt.Errorf("template limit reached (%d/%d)", count, features.MaxTemplates)
	}

	return nil
}

// validateListOwnership ensures the given lists belong to the current tenant.
func (tc *TenantCore) validateListOwnership(listIDs []int, listUUIDs []string) error {
	if len(listIDs) == 0 && len(listUUIDs) == 0 {
		return nil
	}

	query := `SELECT COUNT(*) FROM lists WHERE tenant_id = $1 AND (`
	args := []interface{}{tc.tenantID}
	
	if len(listIDs) > 0 {
		query += `id = ANY($2)`
		args = append(args, listIDs)
		if len(listUUIDs) > 0 {
			query += ` OR uuid = ANY($3)`
			args = append(args, listUUIDs)
		}
	} else if len(listUUIDs) > 0 {
		query += `uuid = ANY($2)`
		args = append(args, listUUIDs)
	}
	query += `)`

	var count int
	err := tc.db.Get(&count, query, args...)
	if err != nil {
		return err
	}

	expectedCount := len(listIDs)
	if expectedCount == 0 {
		expectedCount = len(listUUIDs)
	}

	if count != expectedCount {
		return fmt.Errorf("one or more lists do not belong to this tenant")
	}

	return nil
}

// getTenant retrieves the current tenant's information.
func (tc *TenantCore) getTenant() (*models.Tenant, error) {
	var tenant models.Tenant
	err := tc.db.Get(&tenant, `SELECT * FROM tenants WHERE id = $1`, tc.tenantID)
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}

// GetTenantStats retrieves statistics for the current tenant.
func (tc *TenantCore) GetTenantStats() (map[string]interface{}, error) {
	if err := tc.ensureTenantContext(); err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	
	// Get subscriber count
	var subCount int
	tc.db.Get(&subCount, `SELECT COUNT(*) FROM subscribers WHERE tenant_id = $1`, tc.tenantID)
	stats["subscribers"] = subCount

	// Get campaign count
	var campCount int
	tc.db.Get(&campCount, `SELECT COUNT(*) FROM campaigns WHERE tenant_id = $1`, tc.tenantID)
	stats["campaigns"] = campCount

	// Get list count
	var listCount int
	tc.db.Get(&listCount, `SELECT COUNT(*) FROM lists WHERE tenant_id = $1`, tc.tenantID)
	stats["lists"] = listCount

	// Get template count
	var templateCount int
	tc.db.Get(&templateCount, `SELECT COUNT(*) FROM templates WHERE tenant_id = $1`, tc.tenantID)
	stats["templates"] = templateCount

	// Get monthly campaign count
	var monthlyCampaigns int
	tc.db.Get(&monthlyCampaigns, `
		SELECT COUNT(*) FROM campaigns 
		WHERE tenant_id = $1 
		AND created_at >= date_trunc('month', CURRENT_DATE)
	`, tc.tenantID)
	stats["monthly_campaigns"] = monthlyCampaigns

	return stats, nil
}