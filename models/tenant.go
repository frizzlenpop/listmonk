package models

import (
        "database/sql/driver"
        "encoding/json"
        "fmt"

        "github.com/jmoiron/sqlx/types"
        null "gopkg.in/volatiletech/null.v6"
)

// TenantStatus represents the status of a tenant.
const (
	TenantStatusActive    = "active"
	TenantStatusSuspended = "suspended"
	TenantStatusDeleted   = "deleted"
)

// TenantUserRole represents the role of a user within a tenant.
const (
	TenantUserRoleOwner  = "owner"
	TenantUserRoleAdmin  = "admin"
	TenantUserRoleMember = "member"
	TenantUserRoleViewer = "viewer"
)

// Tenant represents a tenant/organization in the multi-tenant system.
type Tenant struct {
	ID           int         `db:"id" json:"id"`
	UUID         string      `db:"uuid" json:"uuid"`
	Name         string      `db:"name" json:"name"`
	Slug         string      `db:"slug" json:"slug"`
	Domain       null.String `db:"domain" json:"domain"`
	Settings     types.JSONText `db:"settings" json:"settings"`
	Features     types.JSONText `db:"features" json:"features"`
	Status       string      `db:"status" json:"status"`
	Plan         null.String `db:"plan" json:"plan"`
	BillingEmail null.String `db:"billing_email" json:"billing_email"`
	Metadata     types.JSONText `db:"metadata" json:"metadata"`
	CreatedAt    null.Time   `db:"created_at" json:"created_at"`
	UpdatedAt    null.Time   `db:"updated_at" json:"updated_at"`

	// Computed fields
	SubscriberCount int `db:"subscriber_count" json:"subscriber_count,omitempty"`
	CampaignCount   int `db:"campaign_count" json:"campaign_count,omitempty"`
	ListCount       int `db:"list_count" json:"list_count,omitempty"`
	UserCount       int `db:"user_count" json:"user_count,omitempty"`
}

// TenantUser represents the relationship between a user and a tenant.
type TenantUser struct {
	UserID    int       `db:"user_id" json:"user_id"`
	TenantID  int       `db:"tenant_id" json:"tenant_id"`
	Role      string    `db:"role" json:"role"`
	IsDefault bool      `db:"is_default" json:"is_default"`
	CreatedAt null.Time `db:"created_at" json:"created_at"`

	// Join fields
	UserName   string `db:"user_name" json:"user_name,omitempty"`
	UserEmail  string `db:"user_email" json:"user_email,omitempty"`
	TenantName string `db:"tenant_name" json:"tenant_name,omitempty"`
	TenantSlug string `db:"tenant_slug" json:"tenant_slug,omitempty"`
}

// TenantSettings represents tenant-specific settings.
type TenantSettings struct {
	ID        int            `db:"id" json:"id"`
	TenantID  int            `db:"tenant_id" json:"tenant_id"`
	Key       string         `db:"key" json:"key"`
	Value     types.JSONText `db:"value" json:"value"`
	UpdatedAt null.Time      `db:"updated_at" json:"updated_at"`
}

// TenantFeatures represents the features/limits for a tenant.
type TenantFeatures struct {
	MaxSubscribers       int  `json:"max_subscribers"`
	MaxCampaignsPerMonth int  `json:"max_campaigns_per_month"`
	MaxLists             int  `json:"max_lists"`
	MaxTemplates         int  `json:"max_templates"`
	MaxUsers             int  `json:"max_users"`
	CustomDomain         bool `json:"custom_domain"`
	APIAccess            bool `json:"api_access"`
	WebhooksEnabled      bool `json:"webhooks_enabled"`
	AdvancedAnalytics    bool `json:"advanced_analytics"`
}

// TenantContext holds the current tenant information for a request.
type TenantContext struct {
	ID       int            `json:"id"`
	UUID     string         `json:"uuid"`
	Name     string         `json:"name"`
	Slug     string         `json:"slug"`
	Status   string         `json:"status"`
	Settings types.JSONText `json:"settings"`
	Features *TenantFeatures `json:"features"`
	UserRole string         `json:"user_role"` // Role of current user in this tenant
}

// Scan implements the sql.Scanner interface for TenantFeatures.
func (tf *TenantFeatures) Scan(src interface{}) error {
        b, ok := src.([]byte)
        if !ok {
                return fmt.Errorf("invalid type %T for TenantFeatures", src)
        }
        return json.Unmarshal(b, tf)
}

// Value implements the driver.Valuer interface for TenantFeatures.
func (tf TenantFeatures) Value() (driver.Value, error) {
        return json.Marshal(tf)
}

// IsActive checks if the tenant is active.
func (t *Tenant) IsActive() bool {
	return t.Status == TenantStatusActive
}

// CanAddSubscriber checks if the tenant can add more subscribers.
func (t *Tenant) CanAddSubscriber(currentCount int, features *TenantFeatures) bool {
	if features == nil || features.MaxSubscribers == 0 {
		return true // No limit
	}
	return currentCount < features.MaxSubscribers
}

// CanCreateCampaign checks if the tenant can create more campaigns this month.
func (t *Tenant) CanCreateCampaign(monthlyCount int, features *TenantFeatures) bool {
	if features == nil || features.MaxCampaignsPerMonth == 0 {
		return true // No limit
	}
	return monthlyCount < features.MaxCampaignsPerMonth
}

// HasPermission checks if a user has a specific permission level in the tenant.
func (tu *TenantUser) HasPermission(requiredRole string) bool {
	roleHierarchy := map[string]int{
		TenantUserRoleOwner:  4,
		TenantUserRoleAdmin:  3,
		TenantUserRoleMember: 2,
		TenantUserRoleViewer: 1,
	}

	userLevel, userOk := roleHierarchy[tu.Role]
	requiredLevel, reqOk := roleHierarchy[requiredRole]

	if !userOk || !reqOk {
		return false
	}

	return userLevel >= requiredLevel
}

// GetTenantFromSlug retrieves a tenant by its slug.
func GetTenantFromSlug(slug string) (*Tenant, error) {
	// This will be implemented when we update the queries
	return nil, nil
}

// GetTenantFromDomain retrieves a tenant by its custom domain.
func GetTenantFromDomain(domain string) (*Tenant, error) {
	// This will be implemented when we update the queries
	return nil, nil
}

// SetCurrentTenant sets the current tenant for database row-level security.
func SetCurrentTenant(db driver.Conn, tenantID int) error {
	// This will be implemented to set the PostgreSQL session variable
	return nil
}