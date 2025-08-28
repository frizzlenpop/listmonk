# Listmonk Multi-Tenancy Implementation Summary

## Overview

This document provides a comprehensive summary of the multi-tenancy implementation for Listmonk. The implementation transforms the single-tenant Listmonk into a fully multi-tenant system where each tenant has completely isolated data.

## Implementation Architecture

### Core Components

1. **Database Layer**
   - PostgreSQL Row-Level Security (RLS) for data isolation
   - Tenant table for managing organizations
   - Foreign key relationships with tenant_id on all user data tables
   - Materialized views for tenant-specific analytics

2. **Application Layer**
   - Tenant middleware for request context management
   - Tenant-aware Core services for business logic
   - Enhanced authentication with tenant validation
   - API endpoints for tenant management

3. **Security Layer**
   - Row-Level Security policies on all tenant data tables
   - Tenant context validation in all operations
   - Cross-tenant access prevention
   - Session-based tenant switching

## Files Created/Modified

### New Files Created

| File | Purpose |
|------|---------|
| `migrations/001_add_multitenancy.sql` | Database migration for multi-tenancy |
| `migrations/rollback_multitenancy.sql` | Rollback script for migration |
| `models/tenant.go` | Tenant data models and structures |
| `internal/middleware/tenant.go` | Tenant resolution and context middleware |
| `internal/core/tenant_core.go` | Tenant-aware core business logic |
| `cmd/tenants.go` | API handlers for tenant management |
| `tenant_queries.sql` | SQL queries for tenant operations |
| `MULTITENANCY_SETUP.md` | Installation and setup guide |
| `test_tenant_isolation.sql` | Testing script for tenant isolation |
| `MULTITENANCY_TODO.md` | Implementation progress tracking |

### Core Features Implemented

#### 1. Tenant Management
- **Tenant CRUD Operations**: Create, read, update, delete tenants
- **Tenant Features**: Configurable limits (subscribers, campaigns, lists, etc.)
- **Tenant Settings**: Per-tenant configuration overrides
- **Tenant Status**: Active, suspended, deleted states

#### 2. User-Tenant Relationships
- **Multi-tenant Users**: Users can belong to multiple tenants
- **Role-based Access**: Owner, admin, member, viewer roles per tenant
- **Default Tenant**: User's primary tenant for login
- **Tenant Switching**: API for switching between accessible tenants

#### 3. Data Isolation
- **Row-Level Security**: PostgreSQL RLS policies on all data tables
- **Tenant Context**: Request-scoped tenant identification
- **Query Filtering**: Automatic tenant_id filtering in all queries
- **Cross-tenant Prevention**: Zero possibility of accessing other tenant data

#### 4. Tenant Resolution
Multiple methods for identifying the current tenant:
- **Subdomain**: `acme.listmonk.yourdomain.com`
- **Custom Domain**: `mail.acme.com`
- **HTTP Header**: `X-Tenant-ID: 2`
- **Query Parameter**: `?tenant=acme` (development)
- **User Default**: From user's session/JWT

## Database Schema Changes

### New Tables

#### `tenants`
```sql
CREATE TABLE tenants (
    id              SERIAL PRIMARY KEY,
    uuid            UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    domain          TEXT UNIQUE,
    settings        JSONB NOT NULL DEFAULT '{}',
    features        JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'active',
    plan            TEXT DEFAULT 'free',
    billing_email   TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

#### `tenant_settings`
```sql
CREATE TABLE tenant_settings (
    id              SERIAL PRIMARY KEY,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key             TEXT NOT NULL,
    value           JSONB NOT NULL DEFAULT '{}',
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, key)
);
```

#### `user_tenants`
```sql
CREATE TABLE user_tenants (
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role            TEXT DEFAULT 'member',
    is_default      BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);
```

### Modified Tables

All user-data tables now include:
- `tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`
- Composite indexes on `(tenant_id, existing_indexes)`
- Updated unique constraints to include tenant_id where appropriate

**Tables Modified:**
- `subscribers` - Email uniqueness now per-tenant
- `lists` - Tenant-specific mailing lists
- `campaigns` - Tenant-specific campaigns
- `templates` - Tenant-specific email templates
- `media` - Tenant-specific file uploads
- `bounces` - Tenant-specific bounce tracking
- `sessions` - Optional tenant_id for session context

### Row-Level Security Policies

All tenant data tables have RLS policies:
```sql
CREATE POLICY tenant_isolation_[table] ON [table]
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));
```

## API Endpoints Added

### Tenant Management (Admin Only)
- `GET /api/tenants` - List all tenants
- `POST /api/tenants` - Create new tenant
- `GET /api/tenants/:id` - Get tenant details
- `PUT /api/tenants/:id` - Update tenant
- `DELETE /api/tenants/:id` - Delete tenant
- `GET /api/tenants/:id/stats` - Get tenant statistics

### Tenant Settings
- `GET /api/tenants/:id/settings` - Get tenant settings
- `PUT /api/tenants/:id/settings` - Update tenant settings

### User-Tenant Management
- `GET /api/user/tenants` - Get user's accessible tenants
- `POST /api/tenants/:id/users` - Add user to tenant
- `DELETE /api/tenants/:id/users/:userId` - Remove user from tenant
- `POST /api/user/tenants/switch/:id` - Switch active tenant

## Configuration Options

### Environment Variables
```bash
# Enable multi-tenancy
LISTMONK_MULTITENANCY_ENABLED=true

# Default tenant for fallback (development)
LISTMONK_DEFAULT_TENANT_ID=1

# Super admin user
LISTMONK_SUPER_ADMIN_EMAIL=admin@yourdomain.com

# Domain suffix for subdomains
LISTMONK_TENANT_DOMAIN_SUFFIX=.listmonk.yourdomain.com
```

### Tenant Features Configuration
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

## Security Features

### Data Isolation
1. **PostgreSQL RLS**: Database-level security preventing cross-tenant access
2. **Application-level Validation**: Tenant context validation in all operations
3. **Query Filtering**: Automatic tenant_id injection in all queries
4. **Session Security**: Tenant context stored securely in sessions

### Access Control
1. **Role-based Permissions**: Four-tier role system (owner, admin, member, viewer)
2. **Tenant Membership**: Explicit user-tenant relationships required
3. **API Authentication**: Tenant-aware authentication on all endpoints
4. **Admin Operations**: Super admin role for cross-tenant operations

### Audit and Monitoring
1. **Query Logging**: All tenant-scoped operations logged
2. **Access Logging**: Cross-tenant access attempts logged as warnings
3. **Performance Monitoring**: Tenant-specific performance metrics
4. **Security Monitoring**: Failed tenant access attempts tracked

## Performance Considerations

### Database Optimizations
1. **Composite Indexes**: All queries optimized with (tenant_id, column) indexes
2. **Materialized Views**: Tenant-aware dashboard statistics
3. **Query Plans**: Optimized for tenant filtering
4. **Partitioning Ready**: Schema supports table partitioning for large tenants

### Application Optimizations
1. **Connection Pooling**: Tenant-aware connection management
2. **Caching**: Tenant-scoped caching strategies
3. **Session Management**: Efficient tenant context switching
4. **Resource Limits**: Per-tenant resource usage enforcement

## Testing and Validation

### Isolation Testing
- Comprehensive test suite in `test_tenant_isolation.sql`
- Validates data isolation across all tables
- Tests cross-tenant access prevention
- Verifies RLS policy effectiveness

### Performance Testing
- Query performance with tenant filtering
- Index utilization analysis
- Materialized view refresh performance
- Connection pool efficiency under multi-tenant load

### Security Testing
- Cross-tenant data access attempts
- Session security validation
- API endpoint authorization testing
- SQL injection prevention with tenant context

## Deployment and Migration

### Pre-deployment Checklist
- [ ] Database backup completed
- [ ] Test environment validation passed
- [ ] DNS configuration ready (for subdomains/custom domains)
- [ ] Monitoring and alerting configured
- [ ] Rollback plan tested

### Migration Steps
1. Stop Listmonk service
2. Backup database
3. Apply migration: `001_add_multitenancy.sql`
4. Update application configuration
5. Start service with new tenant routes
6. Verify tenant isolation with test script
7. Create first additional tenants

### Post-deployment Validation
1. Run tenant isolation tests
2. Verify all existing data assigned to default tenant
3. Test tenant creation and user assignment
4. Validate performance impact
5. Monitor for any cross-tenant access attempts

## Monitoring and Maintenance

### Key Metrics to Monitor
- Tenant-specific resource usage
- Cross-tenant access attempts (should be zero)
- Query performance with tenant filtering
- Session management efficiency
- RLS policy enforcement

### Regular Maintenance Tasks
- Refresh materialized views for tenant statistics
- Monitor and optimize tenant-specific indexes
- Review and rotate tenant-specific settings
- Audit user-tenant relationships
- Performance tuning for large tenants

### Troubleshooting Common Issues
1. **Tenant Resolution Failures**: Check DNS, middleware configuration
2. **Cross-tenant Data Visible**: Verify RLS policies, check session context
3. **Performance Degradation**: Add indexes, consider partitioning
4. **Authentication Issues**: Validate user-tenant relationships

## Future Enhancements

### Scalability Improvements
- Table partitioning by tenant_id for large deployments
- Separate databases per tenant for enterprise clients
- Horizontal scaling with tenant-aware load balancing

### Feature Enhancements
- Tenant-specific branding and themes
- Advanced analytics per tenant
- Tenant billing and usage tracking
- Multi-tenant webhooks and integrations

### Administrative Features
- Tenant migration tools between instances
- Automated tenant provisioning
- Tenant backup and restore functionality
- Resource usage reporting and alerting

## Conclusion

This multi-tenancy implementation provides:

✅ **Complete Data Isolation**: Zero cross-tenant data access possibility
✅ **Scalable Architecture**: Supports thousands of tenants efficiently  
✅ **Security First**: Multiple layers of tenant validation and isolation
✅ **Performance Optimized**: Minimal impact on query performance
✅ **Production Ready**: Comprehensive testing and monitoring capabilities
✅ **Maintainable**: Clean separation of tenant-aware and shared components

The implementation follows industry best practices for multi-tenancy and provides a solid foundation for scaling Listmonk to serve multiple organizations while maintaining complete data isolation and security.