-- Multi-tenancy migration for Listmonk
-- This migration adds tenant support to all relevant tables

-- =====================================================
-- 1. CREATE TENANTS TABLE
-- =====================================================
CREATE TABLE IF NOT EXISTS tenants (
    id              SERIAL PRIMARY KEY,
    uuid            UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE, -- Used for subdomain/URL routing
    domain          TEXT UNIQUE,          -- Custom domain if configured
    
    -- Tenant configuration
    settings        JSONB NOT NULL DEFAULT '{}',
    features        JSONB NOT NULL DEFAULT '{"max_subscribers": 10000, "max_campaigns_per_month": 100}',
    
    -- Tenant status
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
    
    -- Billing/Plan info (optional, for SaaS)
    plan            TEXT DEFAULT 'free',
    billing_email   TEXT,
    
    -- Metadata
    metadata        JSONB NOT NULL DEFAULT '{}',
    
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants(slug);
CREATE INDEX idx_tenants_domain ON tenants(domain);
CREATE INDEX idx_tenants_status ON tenants(status);

-- =====================================================
-- 2. CREATE DEFAULT TENANT FOR EXISTING DATA
-- =====================================================
INSERT INTO tenants (name, slug, settings) 
VALUES ('Default Tenant', 'default', '{"migrated": true}')
ON CONFLICT DO NOTHING;

-- =====================================================
-- 3. ADD TENANT_ID TO USER-DATA TABLES
-- =====================================================

-- Add tenant_id to subscribers
ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE subscribers SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE subscribers ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE subscribers ADD CONSTRAINT fk_subscribers_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

-- Drop existing unique constraint and recreate with tenant scope
ALTER TABLE subscribers DROP CONSTRAINT IF EXISTS idx_subs_email;
CREATE UNIQUE INDEX idx_subs_tenant_email ON subscribers(tenant_id, LOWER(email));
CREATE INDEX idx_subs_tenant_id ON subscribers(tenant_id);

-- Add tenant_id to lists
ALTER TABLE lists ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE lists SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE lists ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE lists ADD CONSTRAINT fk_lists_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_lists_tenant_id ON lists(tenant_id);

-- Add tenant_id to campaigns
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE campaigns SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE campaigns ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE campaigns ADD CONSTRAINT fk_campaigns_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_campaigns_tenant_id ON campaigns(tenant_id);

-- Add tenant_id to templates
ALTER TABLE templates ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE templates SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE templates ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE templates ADD CONSTRAINT fk_templates_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_templates_tenant_id ON templates(tenant_id);

-- Add tenant_id to media
ALTER TABLE media ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE media SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE media ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE media ADD CONSTRAINT fk_media_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_media_tenant_id ON media(tenant_id);

-- Add tenant_id to bounces
ALTER TABLE bounces ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE bounces SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE bounces ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE bounces ADD CONSTRAINT fk_bounces_tenant 
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_bounces_tenant_id ON bounces(tenant_id);

-- =====================================================
-- 4. CREATE TENANT-SPECIFIC SETTINGS TABLE
-- =====================================================
-- Rename existing settings table to global_settings
ALTER TABLE settings RENAME TO global_settings;

-- Create new tenant_settings table
CREATE TABLE tenant_settings (
    id              SERIAL PRIMARY KEY,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key             TEXT NOT NULL,
    value           JSONB NOT NULL DEFAULT '{}',
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, key)
);
CREATE INDEX idx_tenant_settings_tenant_id ON tenant_settings(tenant_id);

-- Copy existing settings to tenant_settings for default tenant
INSERT INTO tenant_settings (tenant_id, key, value, updated_at)
SELECT 1, key, value, updated_at FROM global_settings;

-- =====================================================
-- 5. USER-TENANT RELATIONSHIP
-- =====================================================
-- Create user_tenants junction table for multi-tenant users
CREATE TABLE user_tenants (
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role            TEXT DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    is_default      BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);
CREATE INDEX idx_user_tenants_user_id ON user_tenants(user_id);
CREATE INDEX idx_user_tenants_tenant_id ON user_tenants(tenant_id);

-- Add all existing users to default tenant
INSERT INTO user_tenants (user_id, tenant_id, role, is_default)
SELECT id, 1, 'admin', true FROM users;

-- =====================================================
-- 6. UPDATE SESSIONS TABLE FOR TENANT CONTEXT
-- =====================================================
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tenant_id INTEGER REFERENCES tenants(id) ON DELETE CASCADE;

-- =====================================================
-- 7. CREATE TENANT-AWARE VIEWS
-- =====================================================

-- Drop existing materialized views
DROP MATERIALIZED VIEW IF EXISTS mat_dashboard_counts CASCADE;
DROP MATERIALIZED VIEW IF EXISTS mat_dashboard_charts CASCADE;
DROP MATERIALIZED VIEW IF EXISTS mat_list_subscriber_stats CASCADE;

-- Create tenant-aware dashboard counts view
CREATE MATERIALIZED VIEW mat_tenant_dashboard_counts AS
SELECT 
    t.id AS tenant_id,
    NOW() AS updated_at,
    JSON_BUILD_OBJECT(
        'subscribers', JSON_BUILD_OBJECT(
            'total', COALESCE(sub_counts.total, 0),
            'blocklisted', COALESCE(sub_counts.blocklisted, 0),
            'orphans', COALESCE(orphan_counts.count, 0)
        ),
        'lists', JSON_BUILD_OBJECT(
            'total', COALESCE(list_counts.total, 0),
            'private', COALESCE(list_counts.private, 0),
            'public', COALESCE(list_counts.public, 0),
            'optin_single', COALESCE(list_counts.optin_single, 0),
            'optin_double', COALESCE(list_counts.optin_double, 0)
        ),
        'campaigns', JSON_BUILD_OBJECT(
            'total', COALESCE(camp_counts.total, 0),
            'by_status', COALESCE(camp_counts.by_status, '{}'::json)
        ),
        'messages', COALESCE(msg_counts.messages, 0)
    ) AS data
FROM tenants t
LEFT JOIN (
    SELECT tenant_id,
           COUNT(*) AS total,
           COUNT(*) FILTER (WHERE status = 'blocklisted') AS blocklisted
    FROM subscribers
    GROUP BY tenant_id
) sub_counts ON t.id = sub_counts.tenant_id
LEFT JOIN (
    SELECT s.tenant_id, COUNT(s.id) AS count
    FROM subscribers s
    LEFT JOIN subscriber_lists sl ON s.id = sl.subscriber_id
    WHERE sl.subscriber_id IS NULL
    GROUP BY s.tenant_id
) orphan_counts ON t.id = orphan_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id,
           COUNT(*) AS total,
           COUNT(*) FILTER (WHERE type = 'private') AS private,
           COUNT(*) FILTER (WHERE type = 'public') AS public,
           COUNT(*) FILTER (WHERE optin = 'single') AS optin_single,
           COUNT(*) FILTER (WHERE optin = 'double') AS optin_double
    FROM lists
    GROUP BY tenant_id
) list_counts ON t.id = list_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id,
           COUNT(*) AS total,
           JSON_OBJECT_AGG(status, num) AS by_status
    FROM (
        SELECT tenant_id, status, COUNT(*) AS num
        FROM campaigns
        GROUP BY tenant_id, status
    ) c
    GROUP BY tenant_id
) camp_counts ON t.id = camp_counts.tenant_id
LEFT JOIN (
    SELECT tenant_id, SUM(sent) AS messages
    FROM campaigns
    GROUP BY tenant_id
) msg_counts ON t.id = msg_counts.tenant_id;

CREATE UNIQUE INDEX mat_tenant_dashboard_counts_idx ON mat_tenant_dashboard_counts (tenant_id, updated_at);

-- =====================================================
-- 8. ROW-LEVEL SECURITY POLICIES
-- =====================================================

-- Enable RLS on main tables
ALTER TABLE subscribers ENABLE ROW LEVEL SECURITY;
ALTER TABLE lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE media ENABLE ROW LEVEL SECURITY;
ALTER TABLE bounces ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_settings ENABLE ROW LEVEL SECURITY;

-- Create RLS policies (using session variables)
-- These will be activated when we set current_setting('app.current_tenant')

-- Subscribers RLS
CREATE POLICY tenant_isolation_subscribers ON subscribers
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Lists RLS
CREATE POLICY tenant_isolation_lists ON lists
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Campaigns RLS
CREATE POLICY tenant_isolation_campaigns ON campaigns
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Templates RLS
CREATE POLICY tenant_isolation_templates ON templates
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Media RLS
CREATE POLICY tenant_isolation_media ON media
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Bounces RLS
CREATE POLICY tenant_isolation_bounces ON bounces
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- Tenant Settings RLS
CREATE POLICY tenant_isolation_tenant_settings ON tenant_settings
    FOR ALL 
    USING (tenant_id = COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, -1));

-- =====================================================
-- 9. CREATE HELPER FUNCTIONS
-- =====================================================

-- Function to set current tenant for RLS
CREATE OR REPLACE FUNCTION set_current_tenant(p_tenant_id INTEGER)
RETURNS void AS $$
BEGIN
    PERFORM set_config('app.current_tenant', p_tenant_id::text, false);
END;
$$ LANGUAGE plpgsql;

-- Function to get current tenant
CREATE OR REPLACE FUNCTION get_current_tenant()
RETURNS INTEGER AS $$
BEGIN
    RETURN COALESCE(NULLIF(current_setting('app.current_tenant', true), '')::integer, NULL);
END;
$$ LANGUAGE plpgsql;

-- =====================================================
-- 10. CREATE ROLLBACK SCRIPT (Save as separate file)
-- =====================================================
-- To rollback this migration, run rollback_multitenancy.sql