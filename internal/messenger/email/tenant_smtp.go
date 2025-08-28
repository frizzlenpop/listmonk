package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// TenantSMTPConfig represents SMTP configuration for a specific tenant
type TenantSMTPConfig struct {
	TenantID int                    `json:"tenant_id"`
	SMTP     []SMTPConf            `json:"smtp"`
	Default  string                `json:"default"` // Default SMTP server name
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// TenantEmailer manages per-tenant SMTP configurations
type TenantEmailer struct {
	db     *sqlx.DB
	logger *log.Logger

	// Cache of tenant-specific emailers
	tenantEmailers map[int]*Emailer
	mu             sync.RWMutex

	// Global fallback emailer (for backward compatibility)
	fallbackEmailer *Emailer

	// Configuration
	cacheEnabled    bool
	cacheExpiry     time.Duration
	refreshInterval time.Duration

	// Cache management
	lastRefresh map[int]time.Time
	cacheMu     sync.RWMutex
}

// NewTenantEmailer creates a new tenant-aware emailer
func NewTenantEmailer(db *sqlx.DB, fallbackEmailer *Emailer, logger *log.Logger) *TenantEmailer {
	te := &TenantEmailer{
		db:              db,
		logger:          logger,
		tenantEmailers:  make(map[int]*Emailer),
		fallbackEmailer: fallbackEmailer,
		cacheEnabled:    true,
		cacheExpiry:     time.Hour, // Cache SMTP config for 1 hour
		refreshInterval: time.Minute * 15,
		lastRefresh:     make(map[int]time.Time),
	}

	// Start background refresh routine
	go te.refreshRoutine()

	return te
}

// GetEmailerForTenant returns the emailer instance for a specific tenant
func (te *TenantEmailer) GetEmailerForTenant(tenantID int) (*Emailer, error) {
	if !te.cacheEnabled {
		return te.loadTenantEmailer(tenantID)
	}

	te.mu.RLock()
	emailer, exists := te.tenantEmailers[tenantID]
	te.mu.RUnlock()

	if exists && te.isCacheValid(tenantID) {
		return emailer, nil
	}

	// Cache miss or expired, load fresh configuration
	return te.loadTenantEmailer(tenantID)
}

// loadTenantEmailer loads SMTP configuration for a tenant and creates an emailer
func (te *TenantEmailer) loadTenantEmailer(tenantID int) (*Emailer, error) {
	config, err := te.loadTenantSMTPConfig(tenantID)
	if err != nil {
		te.logger.Printf("Error loading SMTP config for tenant %d: %v", tenantID, err)
		
		// Fall back to global configuration if available
		if te.fallbackEmailer != nil {
			te.logger.Printf("Using fallback SMTP for tenant %d", tenantID)
			return te.fallbackEmailer, nil
		}
		
		return nil, fmt.Errorf("no SMTP configuration available for tenant %d: %v", tenantID, err)
	}

	// Create emailer with tenant-specific config
	emailer, err := te.createEmailerFromConfig(config)
	if err != nil {
		te.logger.Printf("Error creating emailer for tenant %d: %v", tenantID, err)
		
		// Fall back to global configuration
		if te.fallbackEmailer != nil {
			return te.fallbackEmailer, nil
		}
		
		return nil, err
	}

	// Cache the emailer
	te.mu.Lock()
	te.tenantEmailers[tenantID] = emailer
	te.mu.Unlock()

	te.cacheMu.Lock()
	te.lastRefresh[tenantID] = time.Now()
	te.cacheMu.Unlock()

	te.logger.Printf("Created emailer for tenant %d with %d SMTP servers", tenantID, len(config.SMTP))
	return emailer, nil
}

// loadTenantSMTPConfig loads SMTP configuration from tenant_settings
func (te *TenantEmailer) loadTenantSMTPConfig(tenantID int) (*TenantSMTPConfig, error) {
	// Query tenant-specific SMTP settings
	var smtpValue, defaultValue []byte
	err := te.db.QueryRow(`
		SELECT 
			COALESCE((SELECT value FROM tenant_settings WHERE tenant_id = $1 AND key = 'smtp'), '[]'::jsonb) as smtp_value,
			COALESCE((SELECT value FROM tenant_settings WHERE tenant_id = $1 AND key = 'smtp.default'), '""'::jsonb) as default_value
	`, tenantID).Scan(&smtpValue, &defaultValue)
	
	if err != nil {
		return nil, fmt.Errorf("failed to query tenant SMTP settings: %v", err)
	}

	// Parse SMTP configuration
	var smtpConfig []SMTPConf
	if err := json.Unmarshal(smtpValue, &smtpConfig); err != nil {
		return nil, fmt.Errorf("failed to parse SMTP config: %v", err)
	}

	// Parse default server name
	var defaultServer string
	if err := json.Unmarshal(defaultValue, &defaultServer); err != nil {
		// Default server name is optional
		defaultServer = ""
	}

	// If no tenant-specific SMTP config, try to load from global settings as fallback
	if len(smtpConfig) == 0 {
		te.logger.Printf("No tenant-specific SMTP config for tenant %d, checking global settings", tenantID)
		
		err = te.db.QueryRow(`
			SELECT COALESCE((SELECT value FROM global_settings WHERE key = 'smtp'), '[]'::jsonb)
		`).Scan(&smtpValue)
		
		if err == nil {
			json.Unmarshal(smtpValue, &smtpConfig)
		}
	}

	if len(smtpConfig) == 0 {
		return nil, fmt.Errorf("no SMTP configuration found for tenant %d", tenantID)
	}

	return &TenantSMTPConfig{
		TenantID: tenantID,
		SMTP:     smtpConfig,
		Default:  defaultServer,
	}, nil
}

// createEmailerFromConfig creates an Emailer instance from tenant SMTP configuration
func (te *TenantEmailer) createEmailerFromConfig(config *TenantSMTPConfig) (*Emailer, error) {
	if len(config.SMTP) == 0 {
		return nil, fmt.Errorf("no SMTP servers configured for tenant %d", config.TenantID)
	}

	// Create new emailer with tenant-specific configuration
	// This mimics the existing New() function but with tenant config
	var servers []Server
	
	for _, s := range config.SMTP {
		if !s.Enabled {
			continue
		}

		// Validate required fields
		if s.Host == "" {
			te.logger.Printf("Skipping SMTP server for tenant %d: missing host", config.TenantID)
			continue
		}

		srv := Server{
			Name:            s.Host, // Use host as name if not specified
			Host:            s.Host,
			Port:            s.Port,
			AuthProtocol:    s.AuthProtocol,
			Username:        s.Username,
			Password:        s.Password,
			HelloHostname:   s.HelloHostname,
			MaxConns:        s.MaxConns,
			IdleTimeout:     s.IdleTimeout,
			WaitTimeout:     s.WaitTimeout,
			MaxMessageRetries: s.MaxMsgRetries,
			TLSType:         s.TLSType,
			TLSSkipVerify:   s.TLSSkipVerify,
			EmailHeaders:    s.EmailHeaders,
		}

		// Set defaults
		if srv.Port == 0 {
			srv.Port = 587
		}
		if srv.MaxConns == 0 {
			srv.MaxConns = 10
		}
		if srv.MaxMessageRetries == 0 {
			srv.MaxMessageRetries = 2
		}
		if srv.IdleTimeout == 0 {
			srv.IdleTimeout = time.Second * 15
		}
		if srv.WaitTimeout == 0 {
			srv.WaitTimeout = time.Second * 5
		}

		servers = append(servers, srv)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("no enabled SMTP servers found for tenant %d", config.TenantID)
	}

	// Create the emailer
	emailer := &Emailer{
		servers: servers,
		logger:  te.logger,
	}

	return emailer, nil
}

// isCacheValid checks if the cached emailer is still valid
func (te *TenantEmailer) isCacheValid(tenantID int) bool {
	te.cacheMu.RLock()
	lastRefresh, exists := te.lastRefresh[tenantID]
	te.cacheMu.RUnlock()

	if !exists {
		return false
	}

	return time.Since(lastRefresh) < te.cacheExpiry
}

// refreshRoutine runs in the background to refresh expired cache entries
func (te *TenantEmailer) refreshRoutine() {
	ticker := time.NewTicker(te.refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		te.cleanExpiredCache()
	}
}

// cleanExpiredCache removes expired entries from the cache
func (te *TenantEmailer) cleanExpiredCache() {
	te.mu.Lock()
	te.cacheMu.Lock()

	now := time.Now()
	for tenantID, lastRefresh := range te.lastRefresh {
		if now.Sub(lastRefresh) > te.cacheExpiry {
			delete(te.tenantEmailers, tenantID)
			delete(te.lastRefresh, tenantID)
		}
	}

	te.cacheMu.Unlock()
	te.mu.Unlock()
}

// InvalidateCache forces a reload of SMTP configuration for a tenant
func (te *TenantEmailer) InvalidateCache(tenantID int) {
	te.mu.Lock()
	delete(te.tenantEmailers, tenantID)
	te.mu.Unlock()

	te.cacheMu.Lock()
	delete(te.lastRefresh, tenantID)
	te.cacheMu.Unlock()

	te.logger.Printf("Invalidated SMTP cache for tenant %d", tenantID)
}

// InvalidateAllCache clears all cached emailers
func (te *TenantEmailer) InvalidateAllCache() {
	te.mu.Lock()
	te.tenantEmailers = make(map[int]*Emailer)
	te.mu.Unlock()

	te.cacheMu.Lock()
	te.lastRefresh = make(map[int]time.Time)
	te.cacheMu.Unlock()

	te.logger.Println("Invalidated all SMTP caches")
}

// GetCacheStats returns cache statistics
func (te *TenantEmailer) GetCacheStats() map[string]interface{} {
	te.mu.RLock()
	te.cacheMu.RLock()
	
	stats := map[string]interface{}{
		"cached_tenants": len(te.tenantEmailers),
		"cache_enabled":  te.cacheEnabled,
		"cache_expiry":   te.cacheExpiry.String(),
		"tenant_details": make(map[int]map[string]interface{}),
	}

	// Add per-tenant cache details
	tenantDetails := make(map[int]map[string]interface{})
	for tenantID, lastRefresh := range te.lastRefresh {
		tenantDetails[tenantID] = map[string]interface{}{
			"last_refresh": lastRefresh,
			"age":          time.Since(lastRefresh).String(),
			"valid":        time.Since(lastRefresh) < te.cacheExpiry,
		}
	}
	stats["tenant_details"] = tenantDetails

	te.cacheMu.RUnlock()
	te.mu.RUnlock()

	return stats
}

// SetCacheConfig updates cache configuration
func (te *TenantEmailer) SetCacheConfig(enabled bool, expiry, refreshInterval time.Duration) {
	te.cacheEnabled = enabled
	te.cacheExpiry = expiry
	te.refreshInterval = refreshInterval

	te.logger.Printf("Updated SMTP cache config: enabled=%v, expiry=%v, refresh=%v", 
		enabled, expiry, refreshInterval)
}

// Close shuts down the tenant emailer and cleans up resources
func (te *TenantEmailer) Close() {
	te.mu.Lock()
	for tenantID, emailer := range te.tenantEmailers {
		if emailer != nil {
			// Close connections if the emailer has a close method
			te.logger.Printf("Closing emailer for tenant %d", tenantID)
		}
	}
	te.tenantEmailers = make(map[int]*Emailer)
	te.mu.Unlock()

	te.cacheMu.Lock()
	te.lastRefresh = make(map[int]time.Time)
	te.cacheMu.Unlock()

	te.logger.Println("Tenant emailer closed")
}

// Send sends an email using the appropriate tenant's SMTP configuration
func (te *TenantEmailer) Send(ctx context.Context, tenantID int, msg Message) error {
	emailer, err := te.GetEmailerForTenant(tenantID)
	if err != nil {
		return fmt.Errorf("failed to get emailer for tenant %d: %v", tenantID, err)
	}

	return emailer.Send(msg)
}

// SendWithContext sends an email with context using tenant's SMTP configuration
func (te *TenantEmailer) SendWithContext(ctx context.Context, tenantID int, msg Message) error {
	emailer, err := te.GetEmailerForTenant(tenantID)
	if err != nil {
		return fmt.Errorf("failed to get emailer for tenant %d: %v", tenantID, err)
	}

	// Check if emailer supports context
	type contextSender interface {
		SendWithContext(context.Context, Message) error
	}

	if ctxSender, ok := emailer.(contextSender); ok {
		return ctxSender.SendWithContext(ctx, msg)
	}

	// Fallback to regular Send
	return emailer.Send(msg)
}