package manager

import (
	"fmt"
	"log"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
)

// ManagerMode represents the operating mode for the campaign manager
type ManagerMode int

const (
	// SingleTenantMode uses the traditional single-tenant manager
	SingleTenantMode ManagerMode = iota
	// MultiTenantMode uses the new multi-tenant manager
	MultiTenantMode
)

// ManagerFactory creates the appropriate manager based on configuration
type ManagerFactory struct {
	mode        ManagerMode
	cfg         Config
	store       Store
	tenantStore TenantStore
	i18n        *i18n.I18n
	log         *log.Logger
}

// NewManagerFactory creates a new manager factory
func NewManagerFactory(mode ManagerMode, cfg Config, store Store, tenantStore TenantStore, i *i18n.I18n, l *log.Logger) *ManagerFactory {
	return &ManagerFactory{
		mode:        mode,
		cfg:         cfg,
		store:       store,
		tenantStore: tenantStore,
		i18n:        i,
		log:         l,
	}
}

// CreateManager creates the appropriate manager instance based on mode
func (mf *ManagerFactory) CreateManager() (interface{}, error) {
	switch mf.mode {
	case SingleTenantMode:
		if mf.store == nil {
			return nil, fmt.Errorf("store is required for single-tenant mode")
		}
		mf.log.Printf("creating single-tenant campaign manager")
		return New(mf.cfg, mf.store, mf.i18n, mf.log), nil
	
	case MultiTenantMode:
		if mf.tenantStore == nil {
			return nil, fmt.Errorf("tenant store is required for multi-tenant mode")
		}
		mf.log.Printf("creating multi-tenant campaign manager")
		return NewTenantManager(mf.cfg, mf.tenantStore, mf.i18n, mf.log), nil
	
	default:
		return nil, fmt.Errorf("unknown manager mode: %d", mf.mode)
	}
}

// DetermineMode automatically determines the appropriate mode based on configuration or environment
func DetermineMode(cfg Config, hasMultiTenancy bool) ManagerMode {
	// Check if multi-tenancy is explicitly enabled
	if hasMultiTenancy {
		return MultiTenantMode
	}
	
	// Default to single-tenant mode for backward compatibility
	return SingleTenantMode
}

// ManagerInterface defines common methods that both Manager and TenantManager should implement
type ManagerInterface interface {
	Run()
	Close()
	AddMessenger(msg Messenger) error
	HasRunningCampaigns() bool
}

// Ensure both managers implement the interface
var _ ManagerInterface = (*Manager)(nil)

// TenantManagerInterface extends ManagerInterface with tenant-specific methods
type TenantManagerInterface interface {
	ManagerInterface
	GetTenantCampaignStats(tenantID, campID int) CampStats
	StopTenantCampaign(tenantID, campID int)
}

// Ensure TenantManager implements the extended interface
var _ TenantManagerInterface = (*TenantManager)(nil)

// Configuration helpers for multi-tenant setup

// TenantConfigValidator validates tenant-specific configuration
type TenantConfigValidator struct {
	requiredSMTPFields []string
	requiredURLFields  []string
}

// NewTenantConfigValidator creates a new configuration validator
func NewTenantConfigValidator() *TenantConfigValidator {
	return &TenantConfigValidator{
		requiredSMTPFields: []string{"smtp_host", "smtp_port", "from_email"},
		requiredURLFields:  []string{"root_url"},
	}
}

// ValidateTenantConfig validates that a tenant has the required configuration for campaign processing
func (tcv *TenantConfigValidator) ValidateTenantConfig(tenantID int, settings map[string]interface{}) error {
	// Check required SMTP fields
	for _, field := range tcv.requiredSMTPFields {
		if _, ok := settings[field]; !ok {
			return fmt.Errorf("tenant %d missing required SMTP configuration: %s", tenantID, field)
		}
	}

	// Check required URL fields  
	for _, field := range tcv.requiredURLFields {
		if _, ok := settings[field]; !ok {
			return fmt.Errorf("tenant %d missing required URL configuration: %s", tenantID, field)
		}
	}

	// Validate SMTP port is numeric
	if port, ok := settings["smtp_port"]; ok {
		if _, ok := port.(float64); !ok {
			return fmt.Errorf("tenant %d: smtp_port must be numeric", tenantID)
		}
	}

	return nil
}

// TenantLimitsEnforcer checks tenant limits before processing
type TenantLimitsEnforcer struct {
	store TenantStore
}

// NewTenantLimitsEnforcer creates a new limits enforcer
func NewTenantLimitsEnforcer(store TenantStore) *TenantLimitsEnforcer {
	return &TenantLimitsEnforcer{
		store: store,
	}
}

// CanProcessCampaign checks if a tenant can process campaigns based on their limits
func (tle *TenantLimitsEnforcer) CanProcessCampaign(tenantID int, features *models.TenantFeatures) (bool, string) {
	// Check if tenant has campaign processing enabled
	if features != nil && !features.WebhooksEnabled {
		return false, "campaign processing disabled for tenant"
	}

	// Add more limit checks as needed
	return true, ""
}

// MultiTenantConfig holds multi-tenant specific configuration
type MultiTenantConfig struct {
	// Enable tenant discovery
	EnableTenantDiscovery bool
	
	// Tenant discovery interval
	TenantDiscoveryInterval string
	
	// Maximum number of tenant instances
	MaxTenantInstances int
	
	// Enable tenant isolation
	EnableTenantIsolation bool
	
	// Default tenant limits
	DefaultTenantLimits models.TenantFeatures
}

// DefaultMultiTenantConfig returns sensible defaults for multi-tenant configuration
func DefaultMultiTenantConfig() MultiTenantConfig {
	return MultiTenantConfig{
		EnableTenantDiscovery:   true,
		TenantDiscoveryInterval: "5m",
		MaxTenantInstances:      100,
		EnableTenantIsolation:   true,
		DefaultTenantLimits: models.TenantFeatures{
			MaxSubscribers:       10000,
			MaxCampaignsPerMonth: 50,
			MaxLists:            25,
			MaxTemplates:        10,
			MaxUsers:            5,
			CustomDomain:        false,
			APIAccess:           true,
			WebhooksEnabled:     true,
			AdvancedAnalytics:   false,
		},
	}
}

// ManagerHealthChecker provides health checking for both manager types
type ManagerHealthChecker struct {
	manager interface{}
}

// NewManagerHealthChecker creates a health checker for any manager type
func NewManagerHealthChecker(manager interface{}) *ManagerHealthChecker {
	return &ManagerHealthChecker{
		manager: manager,
	}
}

// CheckHealth performs health checks on the manager
func (mhc *ManagerHealthChecker) CheckHealth() map[string]interface{} {
	health := make(map[string]interface{})
	
	switch m := mhc.manager.(type) {
	case *Manager:
		health["type"] = "single_tenant"
		health["running_campaigns"] = m.HasRunningCampaigns()
		health["mode"] = "traditional"
		
	case *TenantManager:
		health["type"] = "multi_tenant"
		health["running_campaigns"] = m.HasRunningCampaigns()
		health["mode"] = "multi_tenant"
		
		// Add tenant-specific health info
		m.tenantManagersMut.RLock()
		tenantCount := len(m.tenantManagers)
		activeTenants := 0
		for _, tim := range m.tenantManagers {
			if tim.IsActive() {
				activeTenants++
			}
		}
		m.tenantManagersMut.RUnlock()
		
		health["total_tenants"] = tenantCount
		health["active_tenants"] = activeTenants
		
	default:
		health["type"] = "unknown"
		health["error"] = "unrecognized manager type"
	}
	
	return health
}