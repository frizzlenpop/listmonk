#!/bin/bash
set -euo pipefail

# Enhanced Docker entrypoint script for multi-tenant Listmonk
# This script handles tenant initialization, security setup, and proper startup

# Default values
export PUID=${PUID:-1001}
export PGID=${PGID:-1001}
export GROUP_NAME="listmonk"
export USER_NAME="listmonk"

# Multi-tenancy configuration
export LISTMONK_MULTITENANCY_ENABLED=${LISTMONK_MULTITENANCY_ENABLED:-true}
export LISTMONK_DEFAULT_TENANT_ID=${LISTMONK_DEFAULT_TENANT_ID:-1}
export LISTMONK_TENANT_UPLOADS_SEGREGATED=${LISTMONK_TENANT_UPLOADS_SEGREGATED:-true}

# Logging configuration
export LOG_LEVEL=${LOG_LEVEL:-info}
export LOG_FORMAT=${LOG_FORMAT:-json}

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_debug() {
    if [[ "${LOG_LEVEL}" == "debug" ]]; then
        echo -e "${BLUE}[DEBUG]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

# Function to wait for database
wait_for_database() {
    log_info "Waiting for database to be ready..."
    local host="${LISTMONK_db__host:-localhost}"
    local port="${LISTMONK_db__port:-5432}"
    local user="${LISTMONK_db__user:-listmonk}"
    local database="${LISTMONK_db__database:-listmonk}"
    
    local max_attempts=60
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if pg_isready -h "$host" -p "$port" -U "$user" -d "$database" >/dev/null 2>&1; then
            log_info "Database is ready"
            return 0
        fi
        
        log_debug "Database not ready, attempt $attempt/$max_attempts"
        sleep 2
        attempt=$((attempt + 1))
    done
    
    log_error "Database failed to become ready after $max_attempts attempts"
    return 1
}

# Function to wait for Redis (if configured)
wait_for_redis() {
    local redis_host="${LISTMONK_TENANT_REDIS_HOST:-}"
    
    if [[ -n "$redis_host" ]]; then
        log_info "Waiting for Redis to be ready..."
        local host=$(echo "$redis_host" | cut -d: -f1)
        local port=$(echo "$redis_host" | cut -d: -f2)
        port=${port:-6379}
        
        local max_attempts=30
        local attempt=1
        
        while [ $attempt -le $max_attempts ]; do
            if redis-cli -h "$host" -p "$port" ping >/dev/null 2>&1; then
                log_info "Redis is ready"
                return 0
            fi
            
            log_debug "Redis not ready, attempt $attempt/$max_attempts"
            sleep 2
            attempt=$((attempt + 1))
        done
        
        log_warn "Redis failed to become ready, continuing without Redis cache"
    fi
}

# Function to create user and group if running as root
setup_user() {
    if [ "$(id -u)" = "0" ]; then
        log_info "Setting up user and group (PUID=$PUID, PGID=$PGID)"
        
        # Create group if it doesn't exist
        if ! getent group ${PGID} > /dev/null 2>&1; then
            addgroup -g ${PGID} ${GROUP_NAME}
            log_debug "Created group ${GROUP_NAME} with GID ${PGID}"
        else
            existing_group=$(getent group ${PGID} | cut -d: -f1)
            export GROUP_NAME=${existing_group}
            log_debug "Using existing group ${GROUP_NAME} with GID ${PGID}"
        fi
        
        # Create user if it doesn't exist
        if ! getent passwd ${PUID} > /dev/null 2>&1; then
            adduser -u ${PUID} -G ${GROUP_NAME} -s /bin/sh -D ${USER_NAME}
            log_debug "Created user ${USER_NAME} with UID ${PUID}"
        else
            existing_user=$(getent passwd ${PUID} | cut -d: -f1)
            export USER_NAME=${existing_user}
            log_debug "Using existing user ${USER_NAME} with UID ${PUID}"
        fi
    else
        log_debug "Not running as root, using current user $(whoami)"
    fi
}

# Function to load secrets from files
load_secret_files() {
    log_info "Loading secrets from environment files..."
    
    # Save and restore IFS for proper line processing
    old_ifs="$IFS"
    IFS=$'\n'
    
    # Process all LISTMONK_*_FILE environment variables
    for line in $(env | grep '^LISTMONK_.*_FILE=' || true); do
        var="${line%%=*}"
        fpath="${line#*=}"
        
        # Skip empty file paths
        if [[ -z "$fpath" ]]; then
            continue
        fi
        
        # Check if file exists and is readable
        if [ -f "$fpath" ] && [ -r "$fpath" ]; then
            new_var="${var%_FILE}"
            export "$new_var"="$(cat "$fpath")"
            log_debug "Loaded secret for $new_var from $fpath"
        else
            log_warn "Secret file not found or not readable: $fpath (for $var)"
        fi
    done
    
    IFS="$old_ifs"
}

# Function to set up tenant-specific directories
setup_tenant_directories() {
    if [[ "${LISTMONK_TENANT_UPLOADS_SEGREGATED}" == "true" ]]; then
        log_info "Setting up tenant-segregated upload directories..."
        
        # Create tenant directories structure
        mkdir -p /listmonk/uploads/{tenants,shared,temp}
        
        # Set proper permissions
        if ! chown -R ${PUID}:${PGID} /listmonk/uploads 2>/dev/null; then
            log_warn "Failed to change ownership of uploads directory (readonly volume?)"
        fi
        
        # Create directory for tenant 1 (default tenant) if it doesn't exist
        if [[ ! -d "/listmonk/uploads/tenants/1" ]]; then
            mkdir -p /listmonk/uploads/tenants/1
            log_debug "Created uploads directory for default tenant (ID: 1)"
        fi
    fi
    
    # Set up logging directory
    mkdir -p /var/log/listmonk
    if ! chown -R ${PUID}:${PGID} /var/log/listmonk 2>/dev/null; then
        log_warn "Failed to change ownership of log directory"
    fi
}

# Function to validate multi-tenancy configuration
validate_config() {
    log_info "Validating multi-tenancy configuration..."
    
    if [[ "${LISTMONK_MULTITENANCY_ENABLED}" == "true" ]]; then
        # Check required environment variables for multi-tenancy
        local required_vars=(
            "LISTMONK_db__host"
            "LISTMONK_db__user"
            "LISTMONK_db__database"
        )
        
        local missing_vars=()
        for var in "${required_vars[@]}"; do
            if [[ -z "${!var:-}" ]]; then
                missing_vars+=("$var")
            fi
        done
        
        if [[ ${#missing_vars[@]} -gt 0 ]]; then
            log_error "Missing required environment variables for multi-tenancy:"
            for var in "${missing_vars[@]}"; do
                log_error "  - $var"
            done
            return 1
        fi
        
        # Validate tenant resolution settings
        if [[ "${LISTMONK_TENANT_RESOLUTION_SUBDOMAIN:-}" != "true" ]] && \
           [[ "${LISTMONK_TENANT_RESOLUTION_DOMAIN:-}" != "true" ]] && \
           [[ "${LISTMONK_TENANT_RESOLUTION_HEADER:-}" != "true" ]]; then
            log_warn "No tenant resolution methods enabled - this may cause issues"
        fi
        
        log_info "Multi-tenancy configuration validated successfully"
    else
        log_info "Multi-tenancy disabled, skipping tenant-specific validation"
    fi
}

# Function to run database migrations
run_migrations() {
    log_info "Running database migrations..."
    
    # First, run the standard Listmonk installation/migration
    if ! ./listmonk --install --idempotent --yes --config '' > /var/log/listmonk/migration.log 2>&1; then
        log_error "Standard Listmonk migration failed. Check /var/log/listmonk/migration.log"
        return 1
    fi
    
    # If multi-tenancy is enabled and migration file exists, run multi-tenancy migration
    if [[ "${LISTMONK_MULTITENANCY_ENABLED}" == "true" ]]; then
        local migration_file="/listmonk/migrations/001_add_multitenancy.sql"
        if [[ -f "$migration_file" ]]; then
            log_info "Applying multi-tenancy database migration..."
            
            PGPASSWORD="${LISTMONK_db__password}" psql \
                -h "${LISTMONK_db__host}" \
                -p "${LISTMONK_db__port:-5432}" \
                -U "${LISTMONK_db__user}" \
                -d "${LISTMONK_db__database}" \
                -f "$migration_file" \
                >> /var/log/listmonk/migration.log 2>&1
            
            if [[ $? -eq 0 ]]; then
                log_info "Multi-tenancy migration completed successfully"
            else
                log_error "Multi-tenancy migration failed. Check /var/log/listmonk/migration.log"
                return 1
            fi
        else
            log_warn "Multi-tenancy migration file not found: $migration_file"
        fi
    fi
}

# Function to initialize default tenant
init_default_tenant() {
    if [[ "${LISTMONK_MULTITENANCY_ENABLED}" == "true" ]]; then
        log_info "Initializing default tenant..."
        
        # Use the init-tenants.sh script if available
        if [[ -f "/usr/local/bin/init-tenants.sh" ]]; then
            if /usr/local/bin/init-tenants.sh; then
                log_info "Default tenant initialized successfully"
            else
                log_warn "Failed to initialize default tenant using init script"
            fi
        else
            log_debug "Tenant initialization script not found, skipping"
        fi
    fi
}

# Function to perform health check
health_check() {
    log_info "Performing application health check..."
    
    local max_attempts=30
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if curl -f -s "http://localhost:9000/api/health" > /dev/null 2>&1; then
            log_info "Application health check passed"
            return 0
        fi
        
        log_debug "Health check failed, attempt $attempt/$max_attempts"
        sleep 2
        attempt=$((attempt + 1))
    done
    
    log_error "Application failed health check after $max_attempts attempts"
    return 1
}

# Function to cleanup on exit
cleanup() {
    log_info "Shutting down gracefully..."
    # Add any cleanup tasks here
}

# Trap cleanup function
trap cleanup EXIT INT TERM

# Main execution starts here
main() {
    log_info "Starting multi-tenant Listmonk container..."
    log_info "Container version: multi-tenant-1.0"
    log_info "Multi-tenancy enabled: ${LISTMONK_MULTITENANCY_ENABLED}"
    
    # Step 1: Setup user and group
    setup_user
    
    # Step 2: Load secrets from files
    load_secret_files
    
    # Step 3: Validate configuration
    validate_config
    
    # Step 4: Wait for dependencies
    wait_for_database
    wait_for_redis
    
    # Step 5: Setup directories and permissions
    setup_tenant_directories
    
    # Step 6: Run migrations if this is the first run or upgrade
    if [[ "${1:-}" == *"install"* ]] || [[ "${1:-}" == *"upgrade"* ]] || [[ ! -f "/listmonk/.initialized" ]]; then
        run_migrations
        init_default_tenant
        
        # Mark as initialized
        touch /listmonk/.initialized
        log_info "Container initialization completed"
    else
        log_info "Container already initialized, skipping setup"
    fi
    
    # Step 7: Set ownership of application directory
    if ! chown -R ${PUID}:${PGID} /listmonk 2>/dev/null; then
        log_warn "Failed to change ownership of application directory (readonly volume?)"
    fi
    
    log_info "Launching Listmonk with user=[${USER_NAME}] group=[${GROUP_NAME}] PUID=[${PUID}] PGID=[${PGID}]"
    
    # Step 8: Execute the main application
    if [ "$(id -u)" = "0" ] && [ "${PUID}" != "0" ]; then
        # Running as root but need to switch to non-root user
        exec su-exec ${PUID}:${PGID} "$@"
    else
        # Already running as correct user or staying as root
        exec "$@"
    fi
}

# Execute main function with all arguments
main "$@"