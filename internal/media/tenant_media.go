package media

import (
    "bytes"
    "fmt"
    "io"
    "path/filepath"
    "strings"
)

// TenantStore wraps a media Store with tenant-aware functionality.
type TenantStore struct {
    store   Store
    enabled bool
    basePath string
}

// NewTenantStore creates a new tenant-aware media store.
func NewTenantStore(store Store, tenantModeEnabled bool, basePath string) *TenantStore {
    if basePath == "" {
        basePath = "uploads"
    }
    return &TenantStore{
        store:   store,
        enabled: tenantModeEnabled,
        basePath: basePath,
    }
}

// tenantPath generates a tenant-specific path for media files.
func (ts *TenantStore) tenantPath(tenantID int, filename string) string {
    if !ts.enabled || tenantID <= 0 {
        return filename
    }
    tenantDir := fmt.Sprintf("tenants/%d/media", tenantID)
    return filepath.Join(tenantDir, filename)
}

// validateTenantAccess ensures the file belongs to the specified tenant.
func (ts *TenantStore) validateTenantAccess(tenantID int, filename string) error {
    if !ts.enabled {
        return nil
    }
    if tenantID <= 0 {
        return fmt.Errorf("invalid tenant ID: %d", tenantID)
    }
    expectedPrefix := fmt.Sprintf("tenants/%d/media/", tenantID)
    if !strings.HasPrefix(filename, expectedPrefix) && !strings.HasPrefix(filename, "/"+expectedPrefix) {
        return fmt.Errorf("file does not belong to tenant %d: %s", tenantID, filename)
    }
    return nil
}

// Put stores a file with tenant isolation.
func (ts *TenantStore) Put(tenantID int, filename, cType string, file io.ReadSeeker) (string, error) {
    tenantPath := ts.tenantPath(tenantID, filename)
    savedPath, err := ts.store.Put(tenantPath, cType, file)
    if err != nil {
        return "", fmt.Errorf("failed to store file for tenant %d: %w", tenantID, err)
    }
    return savedPath, nil
}

// Get retrieves a file with tenant validation.
func (ts *TenantStore) Get(tenantID int, filename string) (io.ReadCloser, error) {
    if err := ts.validateTenantAccess(tenantID, filename); err != nil {
        return nil, err
    }
    b, err := ts.store.GetBlob(filename)
    if err != nil {
        return nil, err
    }
    return io.NopCloser(bytes.NewReader(b)), nil
}

// Delete removes a file with tenant validation.
func (ts *TenantStore) Delete(tenantID int, filename string) error {
    if err := ts.validateTenantAccess(tenantID, filename); err != nil {
        return err
    }
    return ts.store.Delete(filename)
}

// GetURL generates a URL for a file with tenant validation.
func (ts *TenantStore) GetURL(tenantID int, filename string) string {
    if err := ts.validateTenantAccess(tenantID, filename); err != nil {
        fmt.Printf("Warning: Potential cross-tenant access to %s by tenant %d: %v\n", filename, tenantID, err)
    }
    return ts.store.GetURL(filename)
}

// ValidateMediaAccess validates that a media file belongs to the specified tenant.
func (ts *TenantStore) ValidateMediaAccess(tenantID int, filename string) bool {
    return ts.validateTenantAccess(tenantID, filename) == nil
}

// GetTenantPath returns the tenant-specific path for a filename.
func (ts *TenantStore) GetTenantPath(tenantID int, filename string) string {
    return ts.tenantPath(tenantID, filename)
}

// IsEnabled returns whether tenant mode is enabled.
func (ts *TenantStore) IsEnabled() bool {
    return ts.enabled
}

