-- Rollback script for multi-tenancy migration
-- WARNING: This will remove all tenant isolation. Only use if you need to revert the multi-tenancy changes.

-- =====================================================
-- 1. DISABLE ROW-LEVEL SECURITY
-- =====================================================
ALTER TABLE subscribers DISABLE ROW LEVEL SECURITY;
ALTER TABLE lists DISABLE ROW LEVEL SECURITY;
ALTER TABLE campaigns DISABLE ROW LEVEL SECURITY;
ALTER TABLE templates DISABLE ROW LEVEL SECURITY;
ALTER TABLE media DISABLE ROW LEVEL SECURITY;
ALTER TABLE bounces DISABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_settings DISABLE ROW LEVEL SECURITY;

-- Drop RLS policies
DROP POLICY IF EXISTS tenant_isolation_subscribers ON subscribers;
DROP POLICY IF EXISTS tenant_isolation_lists ON lists;
DROP POLICY IF EXISTS tenant_isolation_campaigns ON campaigns;
DROP POLICY IF EXISTS tenant_isolation_templates ON templates;
DROP POLICY IF EXISTS tenant_isolation_media ON media;
DROP POLICY IF EXISTS tenant_isolation_bounces ON bounces;
DROP POLICY IF EXISTS tenant_isolation_tenant_settings ON tenant_settings;

-- =====================================================
-- 2. DROP HELPER FUNCTIONS
-- =====================================================
DROP FUNCTION IF EXISTS set_current_tenant(INTEGER);
DROP FUNCTION IF EXISTS get_current_tenant();

-- =====================================================
-- 3. RESTORE ORIGINAL MATERIALIZED VIEWS
-- =====================================================
DROP MATERIALIZED VIEW IF EXISTS mat_tenant_dashboard_counts CASCADE;

-- Restore original dashboard counts view
CREATE MATERIALIZED VIEW mat_dashboard_counts AS
    WITH subs AS (
        SELECT COUNT(*) AS num, status FROM subscribers GROUP BY status
    )
    SELECT NOW() AS updated_at,
        JSON_BUILD_OBJECT(
            'subscribers', JSON_BUILD_OBJECT(
                'total', (SELECT SUM(num) FROM subs),
                'blocklisted', (SELECT num FROM subs WHERE status='blocklisted'),
                'orphans', (
                    SELECT COUNT(id) FROM subscribers
                    LEFT JOIN subscriber_lists ON (subscribers.id = subscriber_lists.subscriber_id)
                    WHERE subscriber_lists.subscriber_id IS NULL
                )
            ),
            'lists', JSON_BUILD_OBJECT(
                'total', (SELECT COUNT(*) FROM lists),
                'private', (SELECT COUNT(*) FROM lists WHERE type='private'),
                'public', (SELECT COUNT(*) FROM lists WHERE type='public'),
                'optin_single', (SELECT COUNT(*) FROM lists WHERE optin='single'),
                'optin_double', (SELECT COUNT(*) FROM lists WHERE optin='double')
            ),
            'campaigns', JSON_BUILD_OBJECT(
                'total', (SELECT COUNT(*) FROM campaigns),
                'by_status', (
                    SELECT JSON_OBJECT_AGG (status, num) FROM
                    (SELECT status, COUNT(*) AS num FROM campaigns GROUP BY status) r
                )
            ),
            'messages', (SELECT SUM(sent) AS messages FROM campaigns)
        ) AS data;
CREATE UNIQUE INDEX mat_dashboard_stats_idx ON mat_dashboard_counts (updated_at);

-- =====================================================
-- 4. REMOVE TENANT COLUMNS FROM SESSIONS
-- =====================================================
ALTER TABLE sessions DROP COLUMN IF EXISTS tenant_id;

-- =====================================================
-- 5. DROP USER-TENANT RELATIONSHIP
-- =====================================================
DROP TABLE IF EXISTS user_tenants CASCADE;

-- =====================================================
-- 6. RESTORE ORIGINAL SETTINGS TABLE
-- =====================================================
-- Drop tenant_settings and restore original settings
DROP TABLE IF EXISTS tenant_settings CASCADE;
ALTER TABLE global_settings RENAME TO settings;

-- =====================================================
-- 7. REMOVE TENANT_ID FROM TABLES
-- =====================================================

-- Remove from bounces
ALTER TABLE bounces DROP CONSTRAINT IF EXISTS fk_bounces_tenant;
ALTER TABLE bounces DROP COLUMN IF EXISTS tenant_id;

-- Remove from media
ALTER TABLE media DROP CONSTRAINT IF EXISTS fk_media_tenant;
ALTER TABLE media DROP COLUMN IF EXISTS tenant_id;

-- Remove from templates
ALTER TABLE templates DROP CONSTRAINT IF EXISTS fk_templates_tenant;
ALTER TABLE templates DROP COLUMN IF EXISTS tenant_id;

-- Remove from campaigns
ALTER TABLE campaigns DROP CONSTRAINT IF EXISTS fk_campaigns_tenant;
ALTER TABLE campaigns DROP COLUMN IF EXISTS tenant_id;

-- Remove from lists
ALTER TABLE lists DROP CONSTRAINT IF EXISTS fk_lists_tenant;
ALTER TABLE lists DROP COLUMN IF EXISTS tenant_id;

-- Remove from subscribers (restore original unique constraint)
DROP INDEX IF EXISTS idx_subs_tenant_email;
ALTER TABLE subscribers DROP CONSTRAINT IF EXISTS fk_subscribers_tenant;
ALTER TABLE subscribers DROP COLUMN IF EXISTS tenant_id;
CREATE UNIQUE INDEX idx_subs_email ON subscribers(LOWER(email));

-- =====================================================
-- 8. DROP TENANTS TABLE
-- =====================================================
DROP TABLE IF EXISTS tenants CASCADE;

-- =====================================================
-- 9. DROP TENANT INDEXES
-- =====================================================
DROP INDEX IF EXISTS idx_subs_tenant_id;
DROP INDEX IF EXISTS idx_lists_tenant_id;
DROP INDEX IF EXISTS idx_campaigns_tenant_id;
DROP INDEX IF EXISTS idx_templates_tenant_id;
DROP INDEX IF EXISTS idx_media_tenant_id;
DROP INDEX IF EXISTS idx_bounces_tenant_id;
DROP INDEX IF EXISTS idx_tenant_settings_tenant_id;
DROP INDEX IF EXISTS idx_user_tenants_user_id;
DROP INDEX IF EXISTS idx_user_tenants_tenant_id;

VACUUM ANALYZE;