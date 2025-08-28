# Listmonk Multi-Tenancy Setup Guide

This guide walks you through setting up multi-tenancy in your Listmonk installation. This implementation adds support for multiple isolated tenants within a single Listmonk instance.

## ⚠️ Important Warnings

- **Backup your database before proceeding** - This migration makes significant schema changes
- **Test in a development environment first** - Multi-tenancy changes core application behavior
- **Downtime required** - The migration process requires stopping the application
- **Irreversible changes** - Some schema changes cannot be easily rolled back

## Prerequisites

- Listmonk v2.4.0 or later
- PostgreSQL 12+ (required for advanced features)
- Database administrator access
- Ability to restart the Listmonk service

## Architecture Overview

The multi-tenancy implementation uses:
- **Row-Level Security (RLS)** for data isolation
- **Tenant Context Middleware** for request scoping
- **Tenant-Aware Core Services** for business logic
- **Subdomain/Domain-based Tenant Resolution** for routing

## Installation Steps

### Step 1: Backup Your Database

```bash
# Create a complete backup
pg_dump -h localhost -U listmonk_user listmonk > listmonk_backup_$(date +%Y%m%d).sql

# Verify backup
wc -l listmonk_backup_*.sql
```

### Step 2: Stop Listmonk Service

```bash
# If using systemd
sudo systemctl stop listmonk

# If using Docker
docker stop listmonk

# If running directly
# Kill the listmonk process
```

### Step 3: Apply Database Migration

```bash
# Connect to PostgreSQL as superuser or database owner
psql -h localhost -U listmonk_user -d listmonk

# Apply the migration
\i migrations/001_add_multitenancy.sql

# Verify migration
SELECT COUNT(*) FROM tenants;  -- Should return 1 (default tenant)
SELECT tenant_id, COUNT(*) FROM subscribers GROUP BY tenant_id;  -- All should be tenant 1

# Exit psql
\q
```

### Step 4: Update Application Configuration

Add the following environment variables to your Listmonk configuration:

```bash
# Required: Enable multi-tenancy
LISTMONK_MULTITENANCY_ENABLED=true

# Optional: Default tenant resolution (development only)
LISTMONK_DEFAULT_TENANT_ID=1

# Optional: Super admin user for tenant management
LISTMONK_SUPER_ADMIN_EMAIL=admin@yourdomain.com

# Optional: Tenant domain suffix for subdomain resolution
LISTMONK_TENANT_DOMAIN_SUFFIX=.listmonk.yourdomain.com
```

### Step 5: Update Application Routes

The application needs to be updated to include tenant routes. Add these to your main router setup:

```go
// In main.go or your router setup file
import "github.com/knadh/listmonk/internal/middleware"

// Add tenant middleware
tenantMiddleware := middleware.NewTenantMiddleware(db, queries)
app.Use(tenantMiddleware.Middleware())

// Add tenant management routes (admin only)
g := e.Group("/api/tenants", adminRequired)
g.GET("", handleGetTenants)
g.POST("", handleCreateTenant)
g.GET("/:id", handleGetTenant)
g.PUT("/:id", handleUpdateTenant)
g.DELETE("/:id", handleDeleteTenant)
g.GET("/:id/stats", handleGetTenantStats)
g.GET("/:id/settings", handleGetTenantSettings)
g.PUT("/:id/settings", handleUpdateTenantSettings)

// User tenant management
u := e.Group("/api/user/tenants", authRequired)
u.GET("", handleGetUserTenants)
u.POST("/switch/:id", handleSwitchTenant)
```

### Step 6: Start Listmonk Service

```bash
# If using systemd
sudo systemctl start listmonk
sudo systemctl status listmonk

# If using Docker
docker start listmonk

# Check logs for any errors
tail -f /var/log/listmonk/listmonk.log
```

## Tenant Management

### Creating Your First Additional Tenant

1. **Via API** (recommended):
```bash
curl -X POST http://localhost:9000/api/tenants \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -d '{
    "name": "Acme Corporation",
    "slug": "acme",
    "domain": "mail.acme.com",
    "plan": "premium",
    "billing_email": "billing@acme.com",
    "features": {
      "max_subscribers": 50000,
      "max_campaigns_per_month": 500,
      "custom_domain": true,
      "api_access": true
    }
  }'
```

2. **Via Database** (if needed):
```sql
INSERT INTO tenants (uuid, name, slug, domain, plan, features) 
VALUES (
  gen_random_uuid(),
  'Acme Corporation', 
  'acme',
  'mail.acme.com',
  'premium',
  '{"max_subscribers": 50000, "max_campaigns_per_month": 500}'::jsonb
);
```

### Tenant Access Methods

Tenants can be accessed via:

1. **Subdomain**: `https://acme.listmonk.yourdomain.com`
2. **Custom Domain**: `https://mail.acme.com`
3. **Header**: `X-Tenant-ID: 2`
4. **Query Parameter**: `?tenant=acme` (development only)

## User Management

### Adding Users to Tenants

```bash
# Add a user to a tenant with admin role
curl -X POST http://localhost:9000/api/tenants/2/users \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -d '{
    "user_id": 3,
    "role": "admin",
    "is_default": true
  }'
```

### Available Roles

- **owner**: Full control over tenant (can delete tenant)
- **admin**: Can manage all tenant resources and settings
- **member**: Can manage campaigns, lists, and subscribers
- **viewer**: Read-only access to tenant data

## Configuration Options

### Tenant Features Configuration

Each tenant can have different feature limits:

```json
{
  "max_subscribers": 10000,
  "max_campaigns_per_month": 100,
  "max_lists": 50,
  "max_templates": 20,
  "max_users": 10,
  "custom_domain": true,
  "api_access": true,
  "webhooks_enabled": true,
  "advanced_analytics": false
}
```

### Tenant Settings

Tenant-specific settings override global settings:

```json
{
  "app.site_name": "Acme Newsletter",
  "app.from_email": "newsletter@acme.com",
  "smtp": [{
    "enabled": true,
    "host": "smtp.acme.com",
    "port": 587,
    "username": "newsletter@acme.com",
    "password": "tenant-specific-password"
  }]
}
```

## DNS Configuration

### Subdomain Setup (Wildcard DNS)

Add a wildcard DNS record:
```
*.listmonk.yourdomain.com    CNAME    your-listmonk-server.com
```

### Custom Domain Setup

For each tenant with a custom domain:
```
mail.acme.com               CNAME    your-listmonk-server.com
```

## Monitoring and Maintenance

### Health Checks

Check tenant isolation:
```sql
-- Verify no cross-tenant data access
SET app.current_tenant = '1';
SELECT COUNT(*) FROM subscribers;  -- Should only show tenant 1 data

SET app.current_tenant = '2';  
SELECT COUNT(*) FROM subscribers;  -- Should only show tenant 2 data
```

### Performance Monitoring

Monitor query performance with tenant filtering:
```sql
-- Check index usage
SELECT schemaname, tablename, indexname, idx_scan, idx_tup_read 
FROM pg_stat_user_indexes 
WHERE indexname LIKE '%tenant%';

-- Check slow queries
SELECT query, mean_time, calls 
FROM pg_stat_statements 
WHERE query LIKE '%tenant_id%' 
ORDER BY mean_time DESC;
```

### Backup Strategies

1. **Per-tenant backups**:
```bash
# Export data for specific tenant
pg_dump -h localhost -U listmonk_user listmonk \
  --where="tenant_id = 2" \
  -t subscribers -t lists -t campaigns \
  > tenant_2_backup.sql
```

2. **Full multi-tenant backup**:
```bash
pg_dump -h localhost -U listmonk_user listmonk > full_backup.sql
```

## Troubleshooting

### Common Issues

1. **"Tenant not found" errors**:
   - Check DNS configuration
   - Verify tenant exists in database
   - Check middleware configuration

2. **Cross-tenant data visible**:
   - Verify RLS policies are enabled
   - Check current_tenant session variable
   - Review query modifications

3. **Performance issues**:
   - Add indexes on tenant_id columns
   - Consider partitioning for large tenants
   - Monitor query plans

### Debug Commands

```bash
# Check tenant resolution
curl -H "X-Debug: true" http://acme.listmonk.yourdomain.com/api/subscribers

# Verify RLS policies
SELECT schemaname, tablename, policyname, permissive, roles, cmd, qual 
FROM pg_policies WHERE policyname LIKE '%tenant%';

# Check session variables
SELECT current_setting('app.current_tenant', true) as current_tenant;
```

## Migration Rollback

If you need to rollback the multi-tenancy changes:

```bash
# ⚠️ WARNING: This will remove all tenant isolation
psql -h localhost -U listmonk_user -d listmonk
\i migrations/rollback_multitenancy.sql
```

## Security Considerations

1. **Data Isolation**: Verify no queries can access other tenants' data
2. **Session Security**: Ensure tenant context can't be manipulated
3. **API Security**: Validate tenant access on all endpoints
4. **File Storage**: Segregate uploaded media by tenant
5. **Logs**: Avoid logging sensitive cross-tenant information

## Performance Optimization

1. **Indexing**: Add composite indexes on (tenant_id, frequently_queried_columns)
2. **Partitioning**: Consider table partitioning for large tenants
3. **Connection Pooling**: Use tenant-aware connection pools
4. **Caching**: Implement tenant-aware caching strategies
5. **Materialized Views**: Refresh tenant-specific views regularly

## Support

For issues specific to this multi-tenancy implementation:

1. Check the `MULTITENANCY_TODO.md` file for known issues
2. Review logs with tenant context information
3. Test with single tenant first, then add more tenants
4. Use the rollback script if major issues arise

## Next Steps

After successful setup:

1. Create additional tenants as needed
2. Set up monitoring for tenant-specific metrics
3. Configure automated backups
4. Train users on tenant switching
5. Monitor performance and optimize as needed

---

**Remember**: Multi-tenancy significantly changes how the application works. Test thoroughly and monitor closely after deployment.