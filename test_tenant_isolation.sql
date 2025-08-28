-- Tenant Isolation Testing Script
-- This script validates that multi-tenancy isolation is working correctly

-- =====================================================
-- TEST SETUP: Create test tenants and data
-- =====================================================

\echo '=== TENANT ISOLATION TESTING ==='
\echo ''

-- Create test tenants
INSERT INTO tenants (uuid, name, slug, status) VALUES
    ('00000000-0000-0000-0000-000000000001', 'Test Tenant 1', 'test1', 'active'),
    ('00000000-0000-0000-0000-000000000002', 'Test Tenant 2', 'test2', 'active'),
    ('00000000-0000-0000-0000-000000000003', 'Test Tenant 3', 'test3', 'suspended')
ON CONFLICT (uuid) DO NOTHING;

-- Get tenant IDs
\set tenant1 `SELECT id FROM tenants WHERE slug = 'test1'`
\set tenant2 `SELECT id FROM tenants WHERE slug = 'test2'`
\set tenant3 `SELECT id FROM tenants WHERE slug = 'test3'`

\echo 'Created test tenants:'
SELECT id, name, slug, status FROM tenants WHERE slug LIKE 'test%';
\echo ''

-- Create test data for each tenant
\echo 'Creating test data...'

-- Test data for tenant 1
INSERT INTO subscribers (uuid, email, name, tenant_id) VALUES
    (gen_random_uuid(), 'user1@tenant1.com', 'User 1 Tenant 1', :tenant1),
    (gen_random_uuid(), 'user2@tenant1.com', 'User 2 Tenant 1', :tenant1)
ON CONFLICT DO NOTHING;

INSERT INTO lists (uuid, name, type, tenant_id) VALUES
    (gen_random_uuid(), 'List 1 Tenant 1', 'public', :tenant1),
    (gen_random_uuid(), 'List 2 Tenant 1', 'private', :tenant1)
ON CONFLICT DO NOTHING;

-- Test data for tenant 2  
INSERT INTO subscribers (uuid, email, name, tenant_id) VALUES
    (gen_random_uuid(), 'user1@tenant2.com', 'User 1 Tenant 2', :tenant2),
    (gen_random_uuid(), 'user2@tenant2.com', 'User 2 Tenant 2', :tenant2)
ON CONFLICT DO NOTHING;

INSERT INTO lists (uuid, name, type, tenant_id) VALUES
    (gen_random_uuid(), 'List 1 Tenant 2', 'public', :tenant2),
    (gen_random_uuid(), 'List 2 Tenant 2', 'private', :tenant2)
ON CONFLICT DO NOTHING;

-- Test data for tenant 3 (suspended)
INSERT INTO subscribers (uuid, email, name, tenant_id) VALUES
    (gen_random_uuid(), 'user1@tenant3.com', 'User 1 Tenant 3', :tenant3)
ON CONFLICT DO NOTHING;

INSERT INTO lists (uuid, name, type, tenant_id) VALUES
    (gen_random_uuid(), 'List 1 Tenant 3', 'public', :tenant3)
ON CONFLICT DO NOTHING;

\echo 'Test data created successfully.'
\echo ''

-- =====================================================
-- TEST 1: Verify Row-Level Security is Active
-- =====================================================

\echo '=== TEST 1: Row-Level Security Status ==='
\echo ''

-- Check RLS is enabled on tables
SELECT schemaname, tablename, rowsecurity 
FROM pg_tables 
WHERE tablename IN ('subscribers', 'lists', 'campaigns', 'templates', 'media', 'bounces')
AND schemaname = 'public';

-- Check RLS policies exist
\echo ''
\echo 'RLS Policies:'
SELECT schemaname, tablename, policyname, permissive, roles, cmd, qual 
FROM pg_policies 
WHERE policyname LIKE '%tenant%';

\echo ''

-- =====================================================
-- TEST 2: Basic Tenant Context Functionality  
-- =====================================================

\echo '=== TEST 2: Tenant Context Functionality ==='
\echo ''

-- Test setting tenant context
SELECT set_current_tenant(:tenant1);
\echo 'Set current tenant to:' :tenant1

-- Verify current tenant
SELECT get_current_tenant() as current_tenant_id;

-- Test tenant switching
SELECT set_current_tenant(:tenant2);
SELECT get_current_tenant() as current_tenant_id;

\echo ''

-- =====================================================
-- TEST 3: Data Isolation - Subscribers
-- =====================================================

\echo '=== TEST 3: Subscriber Data Isolation ==='
\echo ''

-- Set tenant context to tenant 1
SELECT set_current_tenant(:tenant1);
\echo 'Testing tenant 1 isolation:'
SELECT COUNT(*) as tenant1_subscriber_count FROM subscribers;
SELECT email FROM subscribers ORDER BY email;

\echo ''

-- Set tenant context to tenant 2  
SELECT set_current_tenant(:tenant2);
\echo 'Testing tenant 2 isolation:'
SELECT COUNT(*) as tenant2_subscriber_count FROM subscribers;
SELECT email FROM subscribers ORDER BY email;

\echo ''

-- Clear tenant context - should return no data due to RLS
SELECT set_current_tenant(-1);
\echo 'Testing with invalid tenant (should return 0 rows):'
SELECT COUNT(*) as invalid_tenant_count FROM subscribers;

\echo ''

-- =====================================================
-- TEST 4: Data Isolation - Lists
-- =====================================================

\echo '=== TEST 4: List Data Isolation ==='
\echo ''

-- Test list isolation for tenant 1
SELECT set_current_tenant(:tenant1);
\echo 'Tenant 1 lists:'
SELECT name, type FROM lists ORDER BY name;

-- Test list isolation for tenant 2
SELECT set_current_tenant(:tenant2);
\echo 'Tenant 2 lists:'
SELECT name, type FROM lists ORDER BY name;

\echo ''

-- =====================================================
-- TEST 5: Cross-Tenant Access Prevention
-- =====================================================

\echo '=== TEST 5: Cross-Tenant Access Prevention ==='
\echo ''

-- Try to access tenant 1 data while in tenant 2 context
SELECT set_current_tenant(:tenant2);
\echo 'Attempting to access tenant 1 subscriber from tenant 2 context:'

-- This should return 0 rows even though we know the IDs exist
WITH tenant1_sub_id AS (
    -- Get a subscriber ID from tenant 1 (this will be done outside RLS context)
    SELECT s.id FROM subscribers s WHERE s.tenant_id = :tenant1 LIMIT 1
)
SELECT COUNT(*) as cross_tenant_access_count
FROM subscribers s 
WHERE s.id = (SELECT id FROM tenant1_sub_id);

\echo 'If isolation works, count should be 0'
\echo ''

-- =====================================================
-- TEST 6: JOIN Query Isolation
-- =====================================================

\echo '=== TEST 6: JOIN Query Isolation ==='
\echo ''

-- Test that JOINs respect tenant boundaries
SELECT set_current_tenant(:tenant1);
\echo 'Tenant 1 - Subscriber-List joins:'
SELECT s.email, l.name as list_name
FROM subscribers s
JOIN subscriber_lists sl ON s.id = sl.subscriber_id
JOIN lists l ON sl.list_id = l.id
ORDER BY s.email;

SELECT set_current_tenant(:tenant2);
\echo 'Tenant 2 - Subscriber-List joins:'  
SELECT s.email, l.name as list_name
FROM subscribers s
JOIN subscriber_lists sl ON s.id = sl.subscriber_id
JOIN lists l ON sl.list_id = l.id
ORDER BY s.email;

\echo ''

-- =====================================================
-- TEST 7: INSERT/UPDATE Tenant Context
-- =====================================================

\echo '=== TEST 7: INSERT/UPDATE Operations ==='
\echo ''

-- Test that inserts get correct tenant_id
SELECT set_current_tenant(:tenant1);
INSERT INTO subscribers (uuid, email, name, tenant_id) 
VALUES (gen_random_uuid(), 'test-insert@tenant1.com', 'Test Insert User', :tenant1);

\echo 'Inserted subscriber for tenant 1'
SELECT COUNT(*) as tenant1_count FROM subscribers WHERE email = 'test-insert@tenant1.com';

-- Switch to tenant 2 and verify we can't see the subscriber
SELECT set_current_tenant(:tenant2);
\echo 'Checking if insert is visible from tenant 2 (should be 0):'
SELECT COUNT(*) as cross_tenant_visibility FROM subscribers WHERE email = 'test-insert@tenant1.com';

\echo ''

-- =====================================================
-- TEST 8: Aggregation Queries
-- =====================================================

\echo '=== TEST 8: Aggregation Query Isolation ==='
\echo ''

-- Test that aggregate functions respect tenant boundaries
SELECT set_current_tenant(:tenant1);
\echo 'Tenant 1 aggregations:'
SELECT 
    COUNT(*) as total_subscribers,
    COUNT(DISTINCT email) as unique_emails
FROM subscribers;

SELECT set_current_tenant(:tenant2);
\echo 'Tenant 2 aggregations:'
SELECT 
    COUNT(*) as total_subscribers,
    COUNT(DISTINCT email) as unique_emails
FROM subscribers;

\echo ''

-- =====================================================
-- TEST 9: Materialized View Isolation
-- =====================================================

\echo '=== TEST 9: Materialized View Isolation ==='
\echo ''

-- Refresh materialized views
REFRESH MATERIALIZED VIEW mat_tenant_dashboard_counts;

\echo 'Tenant dashboard counts:'
SELECT tenant_id, data->>'subscribers' as subscriber_data
FROM mat_tenant_dashboard_counts
WHERE tenant_id IN (:tenant1, :tenant2, :tenant3)
ORDER BY tenant_id;

\echo ''

-- =====================================================
-- TEST 10: Settings Isolation
-- =====================================================

\echo '=== TEST 10: Settings Isolation ==='
\echo ''

-- Insert tenant-specific settings
INSERT INTO tenant_settings (tenant_id, key, value) VALUES
    (:tenant1, 'test.setting', '"tenant1_value"'::jsonb),
    (:tenant2, 'test.setting', '"tenant2_value"'::jsonb)
ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value;

-- Test settings isolation
SELECT set_current_tenant(:tenant1);
\echo 'Tenant 1 settings:'
SELECT key, value FROM tenant_settings WHERE key = 'test.setting';

SELECT set_current_tenant(:tenant2);
\echo 'Tenant 2 settings:'
SELECT key, value FROM tenant_settings WHERE key = 'test.setting';

\echo ''

-- =====================================================
-- TEST 11: Suspended Tenant Access
-- =====================================================

\echo '=== TEST 11: Suspended Tenant Access ==='
\echo ''

-- Test access to suspended tenant
SELECT set_current_tenant(:tenant3);
\echo 'Suspended tenant data access:'
SELECT COUNT(*) as suspended_tenant_count FROM subscribers;
SELECT status FROM tenants WHERE id = :tenant3;

\echo 'Note: RLS allows access to suspended tenant data.'
\echo 'Application layer should block suspended tenant access.'
\echo ''

-- =====================================================
-- TEST 12: Performance Impact
-- =====================================================

\echo '=== TEST 12: Performance Analysis ==='
\echo ''

-- Analyze query plans with tenant filtering
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) 
SELECT * FROM subscribers WHERE tenant_id = :tenant1;

\echo ''

-- =====================================================
-- TEST RESULTS SUMMARY
-- =====================================================

\echo '=== TEST SUMMARY ==='
\echo ''

-- Final verification
SELECT set_current_tenant(:tenant1);
SELECT 'Tenant 1' as tenant, COUNT(*) as subscribers FROM subscribers
UNION ALL
(SELECT set_current_tenant(:tenant2), 'switch', 0)
UNION ALL  
(SELECT 'Tenant 2', COUNT(*) FROM subscribers)
UNION ALL
(SELECT set_current_tenant(:tenant3), 'switch', 0)
UNION ALL
(SELECT 'Tenant 3', COUNT(*) FROM subscribers);

\echo ''
\echo 'Tenant isolation testing completed!'
\echo ''
\echo 'Expected Results:'
\echo '- Each tenant should only see their own data'
\echo '- Cross-tenant queries should return 0 rows'
\echo '- JOINs should respect tenant boundaries'
\echo '- Aggregations should be tenant-scoped'
\echo '- Settings should be isolated per tenant'
\echo ''

-- =====================================================
-- CLEANUP (Optional)
-- =====================================================

\echo 'To clean up test data, run:'
\echo 'DELETE FROM tenant_settings WHERE tenant_id IN (' :tenant1 ',' :tenant2 ',' :tenant3 ');'  
\echo 'DELETE FROM subscribers WHERE tenant_id IN (' :tenant1 ',' :tenant2 ',' :tenant3 ');'
\echo 'DELETE FROM lists WHERE tenant_id IN (' :tenant1 ',' :tenant2 ',' :tenant3 ');'
\echo 'DELETE FROM tenants WHERE slug LIKE ''test%'';'
\echo ''

-- Reset tenant context
SELECT set_current_tenant(1);