package media

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// TenantStore wraps a media Store with tenant-aware functionality
type TenantStore struct {
	store    Store
	enabled  bool
	basePath string
}

// NewTenantStore creates a new tenant-aware media store
func NewTenantStore(store Store, tenantModeEnabled bool, basePath string) *TenantStore {
	if basePath == "" {
		basePath = "uploads"
	}

	return &TenantStore{
		store:    store,
		enabled:  tenantModeEnabled,
		basePath: basePath,
	}
}

// tenantPath generates a tenant-specific path for media files
func (ts *TenantStore) tenantPath(tenantID int, filename string) string {
	if !ts.enabled || tenantID <= 0 {
		// Single-tenant mode or invalid tenant ID - use original path
		return filename
	}

	// Multi-tenant mode - organize files by tenant
	// Structure: /uploads/tenants/{tenant_id}/media/{filename}
	tenantDir := fmt.Sprintf("tenants/%d/media", tenantID)
	return filepath.Join(tenantDir, filename)
}

// validateTenantAccess ensures the file belongs to the specified tenant
func (ts *TenantStore) validateTenantAccess(tenantID int, filename string) error {
	if !ts.enabled {
		return nil // No validation in single-tenant mode
	}

	if tenantID <= 0 {
		return fmt.Errorf("invalid tenant ID: %d", tenantID)
	}

	// Check if the filename contains the correct tenant path
	expectedPrefix := fmt.Sprintf("tenants/%d/media/", tenantID)
	if !strings.HasPrefix(filename, expectedPrefix) && !strings.HasPrefix(filename, "/"+expectedPrefix) {
		// This might be a legacy file or cross-tenant access attempt
		// For security, we should be strict about tenant boundaries
		return fmt.Errorf("file does not belong to tenant %d: %s", tenantID, filename)
	}

	return nil
}

// extractTenantFromPath attempts to extract tenant ID from a file path
func (ts *TenantStore) extractTenantFromPath(filename string) (int, error) {
	if !ts.enabled {
		return 1, nil // Default tenant in single-tenant mode
	}

	// Look for pattern: tenants/{tenant_id}/media/
	parts := strings.Split(filepath.Clean(filename), string(filepath.Separator))
	
	for i, part := range parts {
		if part == "tenants" && i+2 < len(parts) && parts[i+2] == "media" {
			tenantID, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return 0, fmt.Errorf("invalid tenant ID in path: %s", parts[i+1])
			}
			return tenantID, nil
		}
	}

	// No tenant found in path - might be a legacy file
	return 0, fmt.Errorf("no tenant ID found in path: %s", filename)
}

// Put stores a file with tenant isolation
func (ts *TenantStore) Put(tenantID int, filename string, file io.ReadSeeker) (string, error) {
	tenantPath := ts.tenantPath(tenantID, filename)
	
	// Use the underlying store to save the file
	savedPath, err := ts.store.Put(tenantPath, file)
	if err != nil {
		return "", fmt.Errorf("failed to store file for tenant %d: %v", tenantID, err)
	}

	return savedPath, nil
}

// Get retrieves a file with tenant validation
func (ts *TenantStore) Get(tenantID int, filename string) (io.ReadCloser, error) {
	// Validate tenant access to the file
	if err := ts.validateTenantAccess(tenantID, filename); err != nil {
		return nil, err
	}

	return ts.store.Get(filename)
}

// Delete removes a file with tenant validation
func (ts *TenantStore) Delete(tenantID int, filename string) error {
	// Validate tenant access to the file
	if err := ts.validateTenantAccess(tenantID, filename); err != nil {
		return err
	}

	return ts.store.Delete(filename)
}

// GetURL generates a URL for a file with tenant validation
func (ts *TenantStore) GetURL(tenantID int, filename string) string {
	// Validate tenant access (non-blocking for URL generation)
	if err := ts.validateTenantAccess(tenantID, filename); err != nil {
		// Log the error but still generate URL (for backward compatibility)
		// In production, you might want to return an error URL
		fmt.Printf("Warning: Potential cross-tenant access to %s by tenant %d: %v\n", filename, tenantID, err)
	}

	return ts.store.GetURL(filename)
}

// List returns files for a specific tenant
func (ts *TenantStore) List(tenantID int) ([]Media, error) {
	if !ts.enabled {
		// Single-tenant mode - return all files
		return ts.store.List()
	}

	// Get all files and filter by tenant
	allFiles, err := ts.store.List()
	if err != nil {
		return nil, err
	}

	var tenantFiles []Media
	for _, file := range allFiles {
		if fileTenantID, err := ts.extractTenantFromPath(file.Filename); err == nil && fileTenantID == tenantID {
			tenantFiles = append(tenantFiles, file)
		}
	}

	return tenantFiles, nil
}

// Migrate migrates existing media files to tenant structure
func (ts *TenantStore) Migrate(defaultTenantID int) error {
	if !ts.enabled {
		return nil // No migration needed in single-tenant mode
	}

	// Get all existing files
	allFiles, err := ts.store.List()
	if err != nil {
		return fmt.Errorf("failed to list files for migration: %v", err)
	}

	migrated := 0
	errors := 0

	for _, file := range allFiles {
		// Skip files that are already in tenant structure
		if strings.Contains(file.Filename, "tenants/") {
			continue
		}

		// Read the file
		reader, err := ts.store.Get(file.Filename)
		if err != nil {
			fmt.Printf("Failed to read file %s for migration: %v\n", file.Filename, err)
			errors++
			continue
		}

		// Create tenant path
		newPath := ts.tenantPath(defaultTenantID, file.Filename)

		// Save to new location
		_, err = ts.store.Put(newPath, reader)
		reader.Close()

		if err != nil {
			fmt.Printf("Failed to migrate file %s to %s: %v\n", file.Filename, newPath, err)
			errors++
			continue
		}

		// Delete old file (optional - you might want to keep for backup)
		if err := ts.store.Delete(file.Filename); err != nil {
			fmt.Printf("Failed to delete old file %s after migration: %v\n", file.Filename, err)
			// Don't increment errors count as the file was successfully migrated
		}

		migrated++
	}

	fmt.Printf("Media migration completed: %d files migrated, %d errors\n", migrated, errors)
	return nil
}

// GetTenantStats returns storage statistics for a tenant
func (ts *TenantStore) GetTenantStats(tenantID int) (TenantMediaStats, error) {
	stats := TenantMediaStats{
		TenantID: tenantID,
	}

	files, err := ts.List(tenantID)
	if err != nil {
		return stats, err
	}

	stats.FileCount = len(files)
	
	// Calculate total size if the store supports it
	for _, file := range files {
		if file.Size > 0 {
			stats.TotalSize += file.Size
		}
	}

	// Calculate storage by type
	stats.FileTypes = make(map[string]int)
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Filename))
		if ext == "" {
			ext = "no_extension"
		}
		stats.FileTypes[ext]++
	}

	return stats, nil
}

// CleanupTenantFiles removes all files for a deleted tenant
func (ts *TenantStore) CleanupTenantFiles(tenantID int) error {
	if !ts.enabled {
		return fmt.Errorf("tenant cleanup not available in single-tenant mode")
	}

	files, err := ts.List(tenantID)
	if err != nil {
		return fmt.Errorf("failed to list files for tenant %d cleanup: %v", tenantID, err)
	}

	deleted := 0
	errors := 0

	for _, file := range files {
		if err := ts.store.Delete(file.Filename); err != nil {
			fmt.Printf("Failed to delete file %s during tenant %d cleanup: %v\n", file.Filename, tenantID, err)
			errors++
		} else {
			deleted++
		}
	}

	fmt.Printf("Tenant %d cleanup completed: %d files deleted, %d errors\n", tenantID, deleted, errors)
	
	if errors > 0 {
		return fmt.Errorf("tenant cleanup completed with %d errors", errors)
	}

	return nil
}

// ValidateMediaAccess validates that a media file belongs to the specified tenant
func (ts *TenantStore) ValidateMediaAccess(tenantID int, filename string) bool {
	return ts.validateTenantAccess(tenantID, filename) == nil
}

// GetTenantPath returns the tenant-specific path for a filename
func (ts *TenantStore) GetTenantPath(tenantID int, filename string) string {
	return ts.tenantPath(tenantID, filename)
}

// IsEnabled returns whether tenant mode is enabled
func (ts *TenantStore) IsEnabled() bool {
	return ts.enabled
}

// TenantMediaStats represents storage statistics for a tenant
type TenantMediaStats struct {
	TenantID  int            `json:"tenant_id"`
	FileCount int            `json:"file_count"`
	TotalSize int64          `json:"total_size"`
	FileTypes map[string]int `json:"file_types"`
}

// Backward compatibility - implement the Store interface
func (ts *TenantStore) Put(filename string, file io.ReadSeeker) (string, error) {
	// For backward compatibility, assume default tenant (ID: 1)
	return ts.Put(1, filename, file)
}

func (ts *TenantStore) Get(filename string) (io.ReadCloser, error) {
	// Try to extract tenant ID from filename
	tenantID, err := ts.extractTenantFromPath(filename)
	if err != nil {
		// Fallback to allowing access (for backward compatibility)
		return ts.store.Get(filename)
	}
	
	return ts.Get(tenantID, filename)
}

func (ts *TenantStore) Delete(filename string) error {
	// Try to extract tenant ID from filename
	tenantID, err := ts.extractTenantFromPath(filename)
	if err != nil {
		// Fallback to direct deletion (for backward compatibility)
		return ts.store.Delete(filename)
	}
	
	return ts.Delete(tenantID, filename)
}

func (ts *TenantStore) GetURL(filename string) string {
	// Try to extract tenant ID from filename
	tenantID, err := ts.extractTenantFromPath(filename)
	if err != nil {
		// Fallback to direct URL generation
		return ts.store.GetURL(filename)
	}
	
	return ts.GetURL(tenantID, filename)
}

func (ts *TenantStore) List() ([]Media, error) {
	if !ts.enabled {
		return ts.store.List()
	}
	
	// In multi-tenant mode, this should not be used
	// Return empty list or error to prevent data leakage
	return []Media{}, fmt.Errorf("use List(tenantID) for tenant-aware file listing")
}