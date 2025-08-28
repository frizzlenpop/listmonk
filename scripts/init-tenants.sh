#!/bin/bash
set -euo pipefail

# Tenant Initialization Script for Multi-Tenant Listmonk
# This script initializes the default tenant and sets up the multi-tenancy environment

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[TENANT-INIT]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_warn() {
    echo -e "${YELLOW}[TENANT-INIT]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_error() {
    echo -e "${RED}[TENANT-INIT]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_debug() {
    if [[ "${LOG_LEVEL:-info}" == "debug" ]]; then
        echo -e "${BLUE}[TENANT-INIT]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

# Database connection parameters
DB_HOST="${LISTMONK_db__host:-localhost}"
DB_PORT="${LISTMONK_db__port:-5432}"
DB_USER="${LISTMONK_db__user:-listmonk}"
DB_PASSWORD="${LISTMONK_db__password:-}"
DB_NAME="${LISTMONK_db__database:-listmonk}"

# Tenant configuration
DEFAULT_TENANT_ID="${LISTMONK_DEFAULT_TENANT_ID:-1}"
DEFAULT_TENANT_NAME="${LISTMONK_DEFAULT_TENANT_NAME:-Default Organization}"
DEFAULT_TENANT_SLUG="${LISTMONK_DEFAULT_TENANT_SLUG:-default}"
DEFAULT_TENANT_DOMAIN="${LISTMONK_DEFAULT_TENANT_DOMAIN:-}"
SUPER_ADMIN_EMAIL="${LISTMONK_SUPER_ADMIN_EMAIL:-admin@localhost}"

# Function to execute SQL command
execute_sql() {
    local sql_query="$1"
    local description="${2:-SQL Query}"
    
    log_debug "Executing: $description"
    
    PGPASSWORD="$DB_PASSWORD" psql \
        -h "$DB_HOST" \
        -p "$DB_PORT" \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        -c "$sql_query" \
        -q \
        2>/dev/null
    
    return $?
}

# Function to check if tenant exists
tenant_exists() {
    local tenant_id="$1"
    local count
    
    count=$(execute_sql "SELECT COUNT(*) FROM tenants WHERE id = $tenant_id;" "Check tenant existence")
    
    if [[ "$count" == *"1"* ]]; then
        return 0  # Tenant exists
    else
        return 1  # Tenant doesn't exist
    fi
}

# Function to check if multi-tenancy tables exist
check_multitenancy_tables() {
    log_info "Checking if multi-tenancy tables exist..."
    
    local tables_exist
    tables_exist=$(execute_sql "SELECT COUNT(*) FROM information_schema.tables WHERE table_name IN ('tenants', 'user_tenants', 'tenant_settings');" "Check multi-tenancy tables")
    
    if [[ "$tables_exist" == *"3"* ]]; then
        log_info "Multi-tenancy tables found"
        return 0
    else
        log_warn "Multi-tenancy tables not found - migration may not have run"
        return 1
    fi
}

# Function to create default tenant
create_default_tenant() {
    log_info "Creating default tenant..."
    
    if tenant_exists "$DEFAULT_TENANT_ID"; then
        log_info "Default tenant (ID: $DEFAULT_TENANT_ID) already exists"
        return 0
    fi
    
    # Generate UUID for tenant
    local tenant_uuid
    tenant_uuid=$(execute_sql "SELECT gen_random_uuid();" "Generate tenant UUID" | grep -v "gen_random_uuid" | tr -d ' \n')
    
    if [[ -z "$tenant_uuid" ]]; then
        log_error "Failed to generate UUID for tenant"
        return 1
    fi
    
    # Build the INSERT statement
    local domain_clause=""
    if [[ -n "$DEFAULT_TENANT_DOMAIN" ]]; then
        domain_clause=", domain = '$DEFAULT_TENANT_DOMAIN'"
    fi
    
    local sql_query="
        INSERT INTO tenants (id, uuid, name, slug, status, plan, billing_email, features, settings, metadata, created_at, updated_at)
        VALUES (
            $DEFAULT_TENANT_ID,
            '$tenant_uuid',
            '$DEFAULT_TENANT_NAME',
            '$DEFAULT_TENANT_SLUG',
            'active',
            'enterprise',
            '$SUPER_ADMIN_EMAIL',
            '{
                \"max_subscribers\": -1,
                \"max_campaigns_per_month\": -1,
                \"max_lists\": -1,
                \"max_templates\": -1,
                \"max_users\": -1,
                \"custom_domain\": true,
                \"api_access\": true,
                \"webhooks_enabled\": true,
                \"advanced_analytics\": true,
                \"bulk_import\": true
            }'::jsonb,
            '{
                \"default_tenant\": true,
                \"created_by\": \"system\",
                \"migration_source\": \"docker-init\"
            }'::jsonb,
            '{
                \"initialized_by\": \"docker-entrypoint\",
                \"container_version\": \"multi-tenant-1.0\"
            }'::jsonb,
            NOW(),
            NOW()
        )
        ON CONFLICT (id) DO UPDATE SET
            updated_at = NOW(),
            metadata = tenants.metadata || '{\"last_updated_by\": \"docker-entrypoint\"}'::jsonb
        $domain_clause;
    "
    
    if execute_sql "$sql_query" "Create default tenant"; then
        log_info "Default tenant created successfully (ID: $DEFAULT_TENANT_ID, Name: '$DEFAULT_TENANT_NAME')"
    else
        log_error "Failed to create default tenant"
        return 1
    fi
}

# Function to migrate existing data to default tenant
migrate_existing_data() {
    log_info "Migrating existing data to default tenant..."
    
    # List of tables that need tenant_id populated
    local tables=(
        "subscribers"
        "lists"
        "campaigns"
        "templates"
        "media"
        "bounces"
    )
    
    for table in "${tables[@]}"; do
        # Check if table exists and has tenant_id column
        local has_tenant_id
        has_tenant_id=$(execute_sql "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = '$table' AND column_name = 'tenant_id';" "Check $table tenant_id column")
        
        if [[ "$has_tenant_id" == *"1"* ]]; then
            # Update records without tenant_id
            local updated_count
            updated_count=$(execute_sql "UPDATE $table SET tenant_id = $DEFAULT_TENANT_ID WHERE tenant_id IS NULL;" "Migrate $table data")
            
            if [[ $? -eq 0 ]]; then
                log_debug "Migrated data in table: $table"
            else
                log_warn "Failed to migrate data in table: $table"
            fi
        else
            log_debug "Table $table doesn't have tenant_id column, skipping"
        fi
    done
    
    log_info "Data migration completed"
}

# Function to assign super admin to default tenant
assign_super_admin() {
    log_info "Assigning super admin to default tenant..."
    
    # Find the super admin user by email
    local user_id
    user_id=$(execute_sql "SELECT id FROM users WHERE email = '$SUPER_ADMIN_EMAIL';" "Find super admin user")
    
    if [[ -z "$user_id" ]] || [[ "$user_id" == *"0 rows"* ]]; then
        log_warn "Super admin user not found with email: $SUPER_ADMIN_EMAIL"
        log_warn "Super admin assignment will be skipped - assign manually after first login"
        return 0
    fi
    
    # Clean the user_id (remove any whitespace/newlines)
    user_id=$(echo "$user_id" | tr -d ' \n' | grep -o '^[0-9]\+')
    
    if [[ -z "$user_id" ]]; then
        log_warn "Could not parse user ID for super admin"
        return 0
    fi
    
    # Assign user to default tenant as owner
    local sql_query="
        INSERT INTO user_tenants (user_id, tenant_id, role, is_default, created_at)
        VALUES ($user_id, $DEFAULT_TENANT_ID, 'owner', true, NOW())
        ON CONFLICT (user_id, tenant_id) 
        DO UPDATE SET 
            role = 'owner',
            is_default = true;
    "
    
    if execute_sql "$sql_query" "Assign super admin to tenant"; then
        log_info "Super admin (ID: $user_id) assigned to default tenant as owner"
    else
        log_warn "Failed to assign super admin to default tenant"
    fi
}

# Function to set up tenant-specific settings
setup_tenant_settings() {
    log_info "Setting up default tenant settings..."
    
    # Create tenant-specific settings
    local settings_queries=(
        "INSERT INTO tenant_settings (tenant_id, key, value, updated_at) VALUES ($DEFAULT_TENANT_ID, 'app.site_name', '\"$DEFAULT_TENANT_NAME\"', NOW()) ON CONFLICT (tenant_id, key) DO NOTHING;"
        "INSERT INTO tenant_settings (tenant_id, key, value, updated_at) VALUES ($DEFAULT_TENANT_ID, 'app.from_email', '\"noreply@localhost\"', NOW()) ON CONFLICT (tenant_id, key) DO NOTHING;"
        "INSERT INTO tenant_settings (tenant_id, key, value, updated_at) VALUES ($DEFAULT_TENANT_ID, 'privacy.individual_tracking', 'false', NOW()) ON CONFLICT (tenant_id, key) DO NOTHING;"
        "INSERT INTO tenant_settings (tenant_id, key, value, updated_at) VALUES ($DEFAULT_TENANT_ID, 'privacy.unsubscribe_header', 'true', NOW()) ON CONFLICT (tenant_id, key) DO NOTHING;"
    )
    
    for query in "${settings_queries[@]}"; do
        if execute_sql "$query" "Set tenant setting"; then
            log_debug "Tenant setting configured"
        else
            log_warn "Failed to configure a tenant setting"
        fi
    done
    
    log_info "Tenant settings configured"
}

# Function to create upload directories for default tenant
setup_upload_directories() {
    if [[ "${LISTMONK_TENANT_UPLOADS_SEGREGATED:-true}" == "true" ]]; then
        log_info "Setting up upload directories for default tenant..."
        
        local tenant_upload_dir="/listmonk/uploads/tenants/$DEFAULT_TENANT_ID"
        
        # Create tenant-specific directories
        mkdir -p "$tenant_upload_dir"/{media,campaigns,subscribers,temp}
        
        # Set permissions
        chmod -R 755 "$tenant_upload_dir" 2>/dev/null || log_warn "Could not set permissions on upload directory"
        
        log_info "Upload directories created for tenant $DEFAULT_TENANT_ID"
    fi
}

# Function to verify tenant initialization
verify_initialization() {
    log_info "Verifying tenant initialization..."
    
    # Check if default tenant exists and is active
    local tenant_status
    tenant_status=$(execute_sql "SELECT status FROM tenants WHERE id = $DEFAULT_TENANT_ID;" "Check tenant status")
    
    if [[ "$tenant_status" == *"active"* ]]; then
        log_info "Default tenant is active and ready"
    else
        log_error "Default tenant is not active: $tenant_status"
        return 1
    fi
    
    # Check if tenant has data
    local subscriber_count
    subscriber_count=$(execute_sql "SELECT COUNT(*) FROM subscribers WHERE tenant_id = $DEFAULT_TENANT_ID;" "Count tenant subscribers")
    
    log_info "Default tenant has $subscriber_count subscribers"
    
    # Check tenant settings
    local settings_count
    settings_count=$(execute_sql "SELECT COUNT(*) FROM tenant_settings WHERE tenant_id = $DEFAULT_TENANT_ID;" "Count tenant settings")
    
    log_info "Default tenant has $settings_count custom settings"
    
    return 0
}

# Function to enable Row Level Security if not already enabled
enable_rls() {
    log_info "Enabling Row Level Security on tenant tables..."
    
    local tables=(
        "subscribers"
        "lists"
        "campaigns"
        "templates"
        "media"
        "bounces"
        "sessions"
    )
    
    for table in "${tables[@]}"; do
        # Check if table exists
        local table_exists
        table_exists=$(execute_sql "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = '$table';" "Check if $table exists")
        
        if [[ "$table_exists" == *"1"* ]]; then
            # Enable RLS if not already enabled
            execute_sql "ALTER TABLE $table ENABLE ROW LEVEL SECURITY;" "Enable RLS on $table" || log_debug "RLS already enabled on $table"
            log_debug "RLS enabled on table: $table"
        else
            log_debug "Table $table does not exist, skipping RLS"
        fi
    done
    
    log_info "Row Level Security configuration completed"
}

# Main initialization function
main() {
    log_info "Starting tenant initialization process..."
    log_info "Target tenant ID: $DEFAULT_TENANT_ID"
    log_info "Target tenant name: $DEFAULT_TENANT_NAME"
    log_info "Target tenant slug: $DEFAULT_TENANT_SLUG"
    
    # Step 1: Check if multi-tenancy is enabled
    if [[ "${LISTMONK_MULTITENANCY_ENABLED:-false}" != "true" ]]; then
        log_info "Multi-tenancy is disabled, skipping tenant initialization"
        return 0
    fi
    
    # Step 2: Check if multi-tenancy tables exist
    if ! check_multitenancy_tables; then
        log_error "Multi-tenancy tables not found. Please run the migration first."
        return 1
    fi
    
    # Step 3: Create default tenant
    if ! create_default_tenant; then
        log_error "Failed to create default tenant"
        return 1
    fi
    
    # Step 4: Migrate existing data
    if ! migrate_existing_data; then
        log_error "Failed to migrate existing data"
        return 1
    fi
    
    # Step 5: Enable Row Level Security
    if ! enable_rls; then
        log_warn "Failed to enable RLS on some tables"
    fi
    
    # Step 6: Set up tenant settings
    if ! setup_tenant_settings; then
        log_warn "Failed to set up some tenant settings"
    fi
    
    # Step 7: Set up upload directories
    setup_upload_directories
    
    # Step 8: Assign super admin (if user exists)
    assign_super_admin
    
    # Step 9: Verify initialization
    if ! verify_initialization; then
        log_error "Tenant initialization verification failed"
        return 1
    fi
    
    log_info "Tenant initialization completed successfully!"
    log_info "Default tenant '$DEFAULT_TENANT_NAME' (ID: $DEFAULT_TENANT_ID) is ready"
    
    return 0
}

# Execute main function
main "$@"