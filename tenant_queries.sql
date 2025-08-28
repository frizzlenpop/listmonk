-- Tenant Management Queries
-- This file contains all SQL queries for multi-tenant operations

-- name: get-tenants
-- Get all tenants (admin only).
SELECT t.*,
    COALESCE(sub_counts.total, 0) AS subscriber_count,
    COALESCE(camp_counts.total, 0) AS campaign_count,
    COALESCE(list_counts.total, 0) AS list_count,
    COALESCE(user_counts.total, 0) AS user_count
FROM tenants t
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM subscribers GROUP BY tenant_id
) sub_counts ON t.id = sub_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM campaigns GROUP BY tenant_id
) camp_counts ON t.id = camp_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM lists GROUP BY tenant_id
) list_counts ON t.id = list_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM user_tenants GROUP BY tenant_id
) user_counts ON t.id = user_counts.tenant_id
WHERE t.status != 'deleted'
ORDER BY t.created_at DESC;

-- name: get-tenant
-- Get a single tenant by ID.
SELECT t.*,
    COALESCE(sub_counts.total, 0) AS subscriber_count,
    COALESCE(camp_counts.total, 0) AS campaign_count,
    COALESCE(list_counts.total, 0) AS list_count,
    COALESCE(user_counts.total, 0) AS user_count
FROM tenants t
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM subscribers WHERE tenant_id = $1 GROUP BY tenant_id
) sub_counts ON t.id = sub_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM campaigns WHERE tenant_id = $1 GROUP BY tenant_id
) camp_counts ON t.id = camp_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM lists WHERE tenant_id = $1 GROUP BY tenant_id
) list_counts ON t.id = list_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, COUNT(*) AS total FROM user_tenants WHERE tenant_id = $1 GROUP BY tenant_id
) user_counts ON t.id = user_counts.tenant_id
WHERE t.id = $1 AND t.status != 'deleted';

-- name: get-tenant-by-slug
-- Get a tenant by slug.
SELECT * FROM tenants WHERE slug = $1 AND status != 'deleted';

-- name: get-tenant-by-domain  
-- Get a tenant by custom domain.
SELECT * FROM tenants WHERE domain = $1 AND status != 'deleted';

-- name: create-tenant
-- Create a new tenant.
INSERT INTO tenants (uuid, name, slug, domain, plan, billing_email, settings, features)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb, $8::jsonb)
RETURNING *;

-- name: update-tenant
-- Update a tenant.
UPDATE tenants SET
    name = $2,
    slug = $3,
    domain = NULLIF($4, ''),
    status = $5,
    plan = NULLIF($6, ''),
    billing_email = NULLIF($7, ''),
    settings = $8::jsonb,
    features = $9::jsonb,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: delete-tenant
-- Soft delete a tenant.
UPDATE tenants SET status = 'deleted', updated_at = NOW() WHERE id = $1;

-- User-Tenant Relationship Queries

-- name: get-user-tenants
-- Get all tenants a user has access to.
SELECT ut.*,
    u.name AS user_name,
    u.email AS user_email,
    t.name AS tenant_name,
    t.slug AS tenant_slug
FROM user_tenants ut
JOIN users u ON ut.user_id = u.id
JOIN tenants t ON ut.tenant_id = t.id
WHERE ut.user_id = $1 AND t.status != 'deleted'
ORDER BY ut.is_default DESC, t.name ASC;

-- name: get-user-default-tenant
-- Get a user's default tenant.
SELECT t.* FROM tenants t
JOIN user_tenants ut ON t.id = ut.tenant_id
WHERE ut.user_id = $1 AND ut.is_default = true AND t.status != 'deleted'
LIMIT 1;

-- name: get-tenant-users
-- Get all users for a tenant.
SELECT ut.*,
    u.name AS user_name,
    u.email AS user_email,
    u.status AS user_status
FROM user_tenants ut
JOIN users u ON ut.user_id = u.id
WHERE ut.tenant_id = $1
ORDER BY ut.role, u.name;

-- name: add-user-to-tenant
-- Add a user to a tenant with a role.
INSERT INTO user_tenants (user_id, tenant_id, role, is_default)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: update-user-tenant-role
-- Update a user's role in a tenant.
UPDATE user_tenants SET
    role = $3,
    is_default = COALESCE($4, is_default)
WHERE user_id = $1 AND tenant_id = $2
RETURNING *;

-- name: remove-user-from-tenant
-- Remove a user from a tenant.
DELETE FROM user_tenants WHERE user_id = $1 AND tenant_id = $2;

-- name: check-user-tenant-access
-- Check if a user has access to a tenant.
SELECT COUNT(*) FROM user_tenants 
WHERE user_id = $1 AND tenant_id = $2;

-- name: get-user-tenant-role
-- Get a user's role in a specific tenant.
SELECT role FROM user_tenants 
WHERE user_id = $1 AND tenant_id = $2;

-- name: set-user-default-tenant
-- Set a user's default tenant.
BEGIN;
UPDATE user_tenants SET is_default = false WHERE user_id = $1;
UPDATE user_tenants SET is_default = true WHERE user_id = $1 AND tenant_id = $2;
COMMIT;

-- Tenant Settings Queries

-- name: get-tenant-settings
-- Get all settings for a tenant.
SELECT key, value FROM tenant_settings WHERE tenant_id = $1;

-- name: get-tenant-setting
-- Get a specific setting for a tenant.
SELECT value FROM tenant_settings WHERE tenant_id = $1 AND key = $2;

-- name: upsert-tenant-setting
-- Insert or update a tenant setting.
INSERT INTO tenant_settings (tenant_id, key, value, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (tenant_id, key) 
DO UPDATE SET value = $3, updated_at = NOW()
RETURNING *;

-- name: delete-tenant-setting
-- Delete a tenant setting.
DELETE FROM tenant_settings WHERE tenant_id = $1 AND key = $2;

-- Tenant-aware Core Entity Queries
-- These queries include tenant filtering for core entities

-- Tenant-aware subscriber queries

-- name: get-tenant-subscriber
-- Get a subscriber by ID, ensuring it belongs to the current tenant.
SELECT * FROM subscribers 
WHERE tenant_id = $1 AND (
    CASE
        WHEN $2 > 0 THEN id = $2
        WHEN $3 != '' THEN uuid = $3::UUID
        WHEN $4 != '' THEN email = $4
    END
);

-- name: get-tenant-subscribers
-- Get subscribers for a specific tenant with filtering.
SELECT s.*, array_to_json(array_agg(
    CASE WHEN sl.list_id IS NOT NULL
    THEN json_build_object('subscription_id', sl.list_id, 'name', l.name, 'status', sl.status)
    ELSE NULL END
)) AS lists FROM subscribers s
LEFT JOIN subscriber_lists sl ON s.id = sl.subscriber_id  
LEFT JOIN lists l ON sl.list_id = l.id
WHERE s.tenant_id = $1
    AND ($2 = '' OR s.name ILIKE $2 OR s.email ILIKE $2)
    AND ($3 = '' OR s.status = $3)
    AND (CARDINALITY($4::int[]) = 0 OR s.id IN (
        SELECT subscriber_id FROM subscriber_lists WHERE list_id = ANY($4::int[])
    ))
GROUP BY s.id
ORDER BY 
    CASE WHEN $5 = 'name' AND $6 = 'asc' THEN s.name END ASC,
    CASE WHEN $5 = 'name' AND $6 = 'desc' THEN s.name END DESC,
    CASE WHEN $5 = 'email' AND $6 = 'asc' THEN s.email END ASC,
    CASE WHEN $5 = 'email' AND $6 = 'desc' THEN s.email END DESC,
    CASE WHEN $5 = 'created_at' AND $6 = 'asc' THEN s.created_at END ASC,
    CASE WHEN $5 = 'created_at' AND $6 = 'desc' THEN s.created_at END DESC,
    s.id
OFFSET $7 LIMIT $8;

-- name: count-tenant-subscribers
-- Count subscribers for a tenant with optional filtering.
SELECT COUNT(*) FROM subscribers s
WHERE s.tenant_id = $1
    AND ($2 = '' OR s.name ILIKE $2 OR s.email ILIKE $2)
    AND ($3 = '' OR s.status = $3)
    AND (CARDINALITY($4::int[]) = 0 OR s.id IN (
        SELECT subscriber_id FROM subscriber_lists WHERE list_id = ANY($4::int[])
    ));

-- Tenant-aware list queries

-- name: get-tenant-lists
-- Get lists for a specific tenant.
SELECT l.*, 
    COALESCE(sub_counts.total, 0) AS total_subscribers,
    COALESCE(sub_counts.confirmed, 0) AS confirmed_subscribers
FROM lists l
LEFT JOIN (
    SELECT sl.list_id, 
        COUNT(*) AS total,
        COUNT(*) FILTER (WHERE sl.status = 'confirmed') AS confirmed
    FROM subscriber_lists sl 
    JOIN subscribers s ON sl.subscriber_id = s.id
    WHERE s.tenant_id = $1
    GROUP BY sl.list_id
) sub_counts ON l.id = sub_counts.list_id
WHERE l.tenant_id = $1
    AND ($2 = '' OR l.name ILIKE $2 OR l.description ILIKE $2)
ORDER BY 
    CASE WHEN $3 = 'name' AND $4 = 'asc' THEN l.name END ASC,
    CASE WHEN $3 = 'name' AND $4 = 'desc' THEN l.name END DESC,
    CASE WHEN $3 = 'created_at' AND $4 = 'asc' THEN l.created_at END ASC,
    CASE WHEN $3 = 'created_at' AND $4 = 'desc' THEN l.created_at END DESC,
    l.id
OFFSET $5 LIMIT $6;

-- name: get-tenant-list
-- Get a specific list for a tenant.
SELECT l.*, 
    COALESCE(sub_counts.total, 0) AS total_subscribers,
    COALESCE(sub_counts.confirmed, 0) AS confirmed_subscribers
FROM lists l
LEFT JOIN (
    SELECT sl.list_id, 
        COUNT(*) AS total,
        COUNT(*) FILTER (WHERE sl.status = 'confirmed') AS confirmed
    FROM subscriber_lists sl 
    JOIN subscribers s ON sl.subscriber_id = s.id
    WHERE s.tenant_id = $1
    GROUP BY sl.list_id
) sub_counts ON l.id = sub_counts.list_id
WHERE l.tenant_id = $1 AND (l.id = $2 OR l.uuid = $3);

-- Tenant-aware campaign queries

-- name: get-tenant-campaigns
-- Get campaigns for a specific tenant.
SELECT c.* FROM campaigns c
WHERE c.tenant_id = $1
    AND ($2 = '' OR c.name ILIKE $2 OR c.subject ILIKE $2)
    AND (CARDINALITY($3::text[]) = 0 OR c.status = ANY($3::text[]))
ORDER BY 
    CASE WHEN $4 = 'name' AND $5 = 'asc' THEN c.name END ASC,
    CASE WHEN $4 = 'name' AND $5 = 'desc' THEN c.name END DESC,
    CASE WHEN $4 = 'created_at' AND $5 = 'asc' THEN c.created_at END ASC,
    CASE WHEN $4 = 'created_at' AND $5 = 'desc' THEN c.created_at END DESC,
    c.id
OFFSET $6 LIMIT $7;

-- name: get-tenant-campaign
-- Get a specific campaign for a tenant.
SELECT c.* FROM campaigns c
WHERE c.tenant_id = $1 AND (c.id = $2 OR c.uuid = $3);

-- Tenant-aware template queries

-- name: get-tenant-templates
-- Get templates for a specific tenant.
SELECT t.* FROM templates t
WHERE t.tenant_id = $1
    AND ($2 = '' OR t.name ILIKE $2 OR t.body ILIKE $2)
ORDER BY 
    CASE WHEN $3 = 'name' AND $4 = 'asc' THEN t.name END ASC,
    CASE WHEN $3 = 'name' AND $4 = 'desc' THEN t.name END DESC,
    CASE WHEN $3 = 'created_at' AND $4 = 'asc' THEN t.created_at END ASC,
    CASE WHEN $3 = 'created_at' AND $4 = 'desc' THEN t.created_at END DESC,
    t.id
OFFSET $5 LIMIT $6;

-- name: get-tenant-template
-- Get a specific template for a tenant.
SELECT t.* FROM templates t
WHERE t.tenant_id = $1 AND t.id = $2;

-- Tenant Statistics Queries

-- name: get-tenant-dashboard-stats
-- Get dashboard statistics for a tenant.
SELECT * FROM mat_tenant_dashboard_counts WHERE tenant_id = $1;

-- name: refresh-tenant-dashboard-stats
-- Refresh materialized view for a specific tenant.
REFRESH MATERIALIZED VIEW CONCURRENTLY mat_tenant_dashboard_counts;

-- Utility Queries

-- name: validate-tenant-lists
-- Validate that all given list IDs belong to a tenant.
SELECT COUNT(*) FROM lists 
WHERE tenant_id = $1 AND id = ANY($2::int[]);

-- name: validate-tenant-list-uuids
-- Validate that all given list UUIDs belong to a tenant.
SELECT COUNT(*) FROM lists 
WHERE tenant_id = $1 AND uuid = ANY($2::uuid[]);

-- name: get-tenant-limits
-- Get current usage counts for tenant limits.
SELECT 
    (SELECT COUNT(*) FROM subscribers WHERE tenant_id = $1) AS subscriber_count,
    (SELECT COUNT(*) FROM lists WHERE tenant_id = $1) AS list_count,
    (SELECT COUNT(*) FROM templates WHERE tenant_id = $1) AS template_count,
    (SELECT COUNT(*) FROM campaigns WHERE tenant_id = $1 
     AND created_at >= date_trunc('month', CURRENT_DATE)) AS monthly_campaign_count,
    (SELECT COUNT(*) FROM user_tenants WHERE tenant_id = $1) AS user_count;

-- Migration helper queries

-- name: migrate-existing-data-to-tenant
-- Migrate existing data to default tenant (run once during migration).
BEGIN;
UPDATE subscribers SET tenant_id = 1 WHERE tenant_id IS NULL;
UPDATE lists SET tenant_id = 1 WHERE tenant_id IS NULL;
UPDATE campaigns SET tenant_id = 1 WHERE tenant_id IS NULL;
UPDATE templates SET tenant_id = 1 WHERE tenant_id IS NULL;
UPDATE media SET tenant_id = 1 WHERE tenant_id IS NULL;
UPDATE bounces SET tenant_id = 1 WHERE tenant_id IS NULL;
COMMIT;