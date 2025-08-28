package manager

import (
	"context"
	"log"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
)

// This file provides examples of how to integrate the multi-tenant manager
// into the main application. It shows the usage patterns and setup required.

// ExampleTenantManagerSetup demonstrates how to set up the multi-tenant manager
func ExampleTenantManagerSetup(db *sqlx.DB, queries *models.Queries) {
	// Initialize logger
	logger := log.New(os.Stdout, "[TENANT-MANAGER] ", log.LstdFlags)

	// Initialize i18n
	i18nInstance, err := i18n.New("en", "./i18n")
	if err != nil {
		logger.Fatalf("failed to initialize i18n: %v", err)
	}

	// Configure the manager
	cfg := Config{
		BatchSize:             1000,
		Concurrency:          10,
		MessageRate:          100,
		MaxSendErrors:        10,
		SlidingWindow:        true,
		SlidingWindowDuration: 60,
		SlidingWindowRate:    1000,
		RequeueOnError:       true,
		FromEmail:           "noreply@example.com",
		IndividualTracking:  true,
		LinkTrackURL:        "https://example.com/link/%s/%s/%s",
		UnsubURL:            "https://example.com/subscription/%s/%s",
		OptinURL:            "https://example.com/subscription/optin/%s?l=%s",
		MessageURL:          "https://example.com/campaign/%s/%s",
		ViewTrackURL:        "https://example.com/campaign/%s/%s/px.png",
		ArchiveURL:          "https://example.com/archive",
		RootURL:             "https://example.com",
		UnsubHeader:         true,
		ScanInterval:        60,
		ScanCampaigns:       true,
	}

	// Create tenant store implementation
	tenantStore := &store{
		queries: queries,
		db:      db,
		// core and media would be initialized here in real usage
	}

	// Create manager factory
	factory := NewManagerFactory(
		MultiTenantMode,
		cfg,
		nil, // regular store not needed for multi-tenant mode
		tenantStore,
		i18nInstance,
		logger,
	)

	// Create the tenant manager
	managerInterface, err := factory.CreateManager()
	if err != nil {
		logger.Fatalf("failed to create manager: %v", err)
	}

	tenantManager := managerInterface.(*TenantManager)

	// Add messengers (SMTP, etc.)
	// This would typically be done in your main application setup
	// smtp := email.New(...)
	// tenantManager.AddMessenger(smtp)

	// Start the tenant manager
	go tenantManager.Run()

	logger.Printf("multi-tenant campaign manager started")
}

// ExampleSingleTenantCompatibility demonstrates backward compatibility
func ExampleSingleTenantCompatibility(db *sqlx.DB, queries *models.Queries) {
	logger := log.New(os.Stdout, "[SINGLE-MANAGER] ", log.LstdFlags)

	i18nInstance, err := i18n.New("en", "./i18n")
	if err != nil {
		logger.Fatalf("failed to initialize i18n: %v", err)
	}

	cfg := Config{
		BatchSize:     1000,
		Concurrency:   5,
		MessageRate:   50,
		ScanInterval:  60,
		ScanCampaigns: true,
		// ... other config
	}

	// Option 1: Use traditional single-tenant manager
	legacyStore := &store{
		queries: queries,
		db:      db,
	}

	traditionalManager := New(cfg, legacyStore, i18nInstance, logger)
	go traditionalManager.Run()

	// Option 2: Use single-tenant manager with tenant store adapter (recommended)
	tenantStore := &store{
		queries: queries,
		db:      db,
	}

	adaptedManager := NewFromTenantStore(cfg, tenantStore, i18nInstance, logger)
	go adaptedManager.Run()

	logger.Printf("single-tenant managers started")
}

// ExampleTenantManagerUsage shows how to interact with the tenant manager
func ExampleTenantManagerUsage(tenantManager *TenantManager) {
	// Check if any campaigns are running
	hasRunning := tenantManager.HasRunningCampaigns()
	log.Printf("Has running campaigns: %v", hasRunning)

	// Get campaign stats for a specific tenant
	stats := tenantManager.GetTenantCampaignStats(1, 123)
	log.Printf("Campaign 123 send rate for tenant 1: %d/min", stats.SendRate)

	// Stop a campaign for a specific tenant
	tenantManager.StopTenantCampaign(1, 123)
	log.Printf("Stopped campaign 123 for tenant 1")

	// Health check
	healthChecker := NewManagerHealthChecker(tenantManager)
	health := healthChecker.CheckHealth()
	log.Printf("Manager health: %+v", health)
}

// ExampleTenantConfigValidation shows how to validate tenant configuration
func ExampleTenantConfigValidation() {
	validator := NewTenantConfigValidator()

	// Example tenant settings
	tenantSettings := map[string]interface{}{
		"smtp_host":     "smtp.tenant1.com",
		"smtp_port":     587.0,
		"smtp_username": "tenant1@example.com",
		"smtp_password": "password123",
		"from_email":    "noreply@tenant1.com",
		"root_url":      "https://tenant1.example.com",
	}

	// Validate configuration
	if err := validator.ValidateTenantConfig(1, tenantSettings); err != nil {
		log.Printf("Tenant configuration invalid: %v", err)
	} else {
		log.Printf("Tenant configuration valid")
	}
}

// ExampleTenantLimitsEnforcement shows how to enforce tenant limits
func ExampleTenantLimitsEnforcement(store TenantStore) {
	enforcer := NewTenantLimitsEnforcer(store)

	features := &models.TenantFeatures{
		MaxSubscribers:       10000,
		MaxCampaignsPerMonth: 100,
		WebhooksEnabled:      true,
	}

	canProcess, reason := enforcer.CanProcessCampaign(1, features)
	if !canProcess {
		log.Printf("Cannot process campaign for tenant 1: %s", reason)
	} else {
		log.Printf("Tenant 1 can process campaigns")
	}
}

// ExampleManagerModeSelection demonstrates how to select the appropriate manager mode
func ExampleManagerModeSelection() {
	cfg := Config{
		// ... configuration
	}

	// Determine mode based on environment or configuration
	hasMultiTenancy := checkMultiTenancyEnabled() // Your implementation
	mode := DetermineMode(cfg, hasMultiTenancy)

	switch mode {
	case SingleTenantMode:
		log.Printf("Using single-tenant mode")
	case MultiTenantMode:
		log.Printf("Using multi-tenant mode")
	}
}

// checkMultiTenancyEnabled is a placeholder for your multi-tenancy detection logic
func checkMultiTenancyEnabled() bool {
	// Check environment variable, configuration file, or database
	return os.Getenv("ENABLE_MULTI_TENANCY") == "true"
}

// ExampleTenantStoreImplementation shows how to implement a custom tenant store
type CustomTenantStore struct {
	db      *sqlx.DB
	queries *models.Queries
	// Add other dependencies
}

// Implement TenantStore interface methods
func (cts *CustomTenantStore) NextTenantCampaigns(tenantID int, currentIDs []int64, sentCounts []int64) ([]*models.Campaign, error) {
	// Set tenant context
	ctx := context.WithValue(context.Background(), "tenant_id", tenantID)
	
	// Your custom implementation
	var campaigns []*models.Campaign
	
	// Example query with tenant filtering
	query := `
		SELECT * FROM campaigns 
		WHERE tenant_id = $1 
		AND status = 'running'
		AND id NOT IN (SELECT unnest($2::int[]))
		ORDER BY created_at ASC
		LIMIT 10
	`
	
	err := cts.db.SelectContext(ctx, &campaigns, query, tenantID, currentIDs)
	return campaigns, err
}

// Implement other TenantStore methods...
// (Other method implementations would follow similar patterns)

// ExampleContextualLogging demonstrates tenant-aware logging
type TenantLogger struct {
	*log.Logger
	tenantID int
}

func NewTenantLogger(base *log.Logger, tenantID int) *TenantLogger {
	return &TenantLogger{
		Logger:   base,
		tenantID: tenantID,
	}
}

func (tl *TenantLogger) Printf(format string, v ...interface{}) {
	prefixedFormat := "[TENANT-%d] " + format
	args := make([]interface{}, 0, len(v)+1)
	args = append(args, tl.tenantID)
	args = append(args, v...)
	tl.Logger.Printf(prefixedFormat, args...)
}

// ExampleTenantAwareBounceHandling shows how to handle bounces per tenant
func ExampleTenantAwareBounceHandling(tenantManager *TenantManager, tenantID int, bounceEvent models.Bounce) {
	// Process bounce event with tenant context
	tenantManager.tenantManagersMut.RLock()
	if tim, exists := tenantManager.tenantManagers[tenantID]; exists {
		// Handle bounce for specific tenant
		if err := tim.store.BlocklistTenantSubscriber(tenantID, int64(bounceEvent.SubscriberID)); err != nil {
			log.Printf("tenant %d: failed to blocklist subscriber %d: %v", 
				tenantID, bounceEvent.SubscriberID, err)
		}
	}
	tenantManager.tenantManagersMut.RUnlock()
}

// ExampleTenantSpecificNotifications demonstrates tenant-specific notification handling
func ExampleTenantSpecificNotifications(tenantManager *TenantManager) {
	// Set up tenant-specific notification function
	tenantManager.fnNotify = func(tenantID int, subject string, data any) error {
		// Send notification specific to tenant
		log.Printf("NOTIFICATION [TENANT-%d]: %s - %+v", tenantID, subject, data)
		
		// Here you could:
		// - Send email to tenant administrators
		// - Post to tenant-specific webhook
		// - Log to tenant-specific log file
		// - Send to tenant-specific monitoring system
		
		return nil
	}
}