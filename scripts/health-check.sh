#!/bin/bash
set -euo pipefail

# Multi-Tenant Listmonk Health Check Script
# This script performs comprehensive health checks for multi-tenant deployments

# Default configuration
HEALTH_CHECK_TIMEOUT=${HEALTH_CHECK_TIMEOUT:-10}
HEALTH_CHECK_URL=${HEALTH_CHECK_URL:-"http://localhost:9000"}
LOG_LEVEL=${LOG_LEVEL:-"info"}

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    if [[ "$LOG_LEVEL" != "quiet" ]]; then
        echo -e "${GREEN}[HEALTH]${NC} $(date '+%H:%M:%S') - $1"
    fi
}

log_warn() {
    if [[ "$LOG_LEVEL" != "quiet" ]]; then
        echo -e "${YELLOW}[HEALTH]${NC} $(date '+%H:%M:%S') - $1"
    fi
}

log_error() {
    echo -e "${RED}[HEALTH]${NC} $(date '+%H:%M:%S') - $1" >&2
}

log_debug() {
    if [[ "${LOG_LEVEL}" == "debug" ]]; then
        echo -e "${BLUE}[HEALTH]${NC} $(date '+%H:%M:%S') - $1"
    fi
}

# Exit codes
EXIT_SUCCESS=0
EXIT_HTTP_FAILED=1
EXIT_DB_FAILED=2
EXIT_TENANT_FAILED=3
EXIT_REDIS_FAILED=4
EXIT_FILESYSTEM_FAILED=5

# Function to check HTTP endpoint health
check_http_health() {
    log_debug "Checking HTTP health endpoint..."
    
    local response
    local http_code
    
    # Try to get health endpoint with timeout
    if ! response=$(curl -s -w "%{http_code}" --max-time "$HEALTH_CHECK_TIMEOUT" "$HEALTH_CHECK_URL/api/health" 2>/dev/null); then
        log_error "Failed to connect to health endpoint"
        return $EXIT_HTTP_FAILED
    fi
    
    # Extract HTTP code (last 3 characters)
    http_code="${response: -3}"
    response="${response%???}"
    
    if [[ "$http_code" != "200" ]]; then
        log_error "Health endpoint returned HTTP $http_code"
        return $EXIT_HTTP_FAILED
    fi
    
    log_debug "HTTP health check passed (200 OK)"
    return 0
}

# Function to check database connectivity
check_database_health() {
    log_debug "Checking database connectivity..."
    
    # Check if we have database connection parameters
    local db_host="${LISTMONK_db__host:-localhost}"
    local db_port="${LISTMONK_db__port:-5432}"
    local db_user="${LISTMONK_db__user:-listmonk}"
    local db_name="${LISTMONK_db__database:-listmonk}"
    
    if ! pg_isready -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" -t "$HEALTH_CHECK_TIMEOUT" >/dev/null 2>&1; then
        log_error "Database is not ready"
        return $EXIT_DB_FAILED
    fi
    
    log_debug "Database connectivity check passed"
    return 0
}

# Function to check Redis connectivity (if configured)
check_redis_health() {
    local redis_host="${LISTMONK_TENANT_REDIS_HOST:-}"
    
    if [[ -n "$redis_host" ]]; then
        log_debug "Checking Redis connectivity..."
        
        local host=$(echo "$redis_host" | cut -d: -f1)
        local port=$(echo "$redis_host" | cut -d: -f2)
        port=${port:-6379}
        
        local redis_password="${LISTMONK_TENANT_REDIS_PASSWORD:-}"
        local auth_args=""
        if [[ -n "$redis_password" ]]; then
            auth_args="-a $redis_password"
        fi
        
        if ! timeout "$HEALTH_CHECK_TIMEOUT" redis-cli -h "$host" -p "$port" $auth_args ping >/dev/null 2>&1; then
            log_warn "Redis is not available (non-critical)"
            return 0  # Redis is optional, don't fail health check
        fi
        
        log_debug "Redis connectivity check passed"
    fi
    
    return 0
}

# Function to check tenant context (if multi-tenancy is enabled)
check_tenant_health() {
    if [[ "${LISTMONK_MULTITENANCY_ENABLED:-false}" == "true" ]]; then
        log_debug "Checking tenant functionality..."
        
        # Try to access the tenant endpoint
        local response
        local http_code
        
        if ! response=$(curl -s -w "%{http_code}" --max-time "$HEALTH_CHECK_TIMEOUT" \
                           -H "X-Tenant-ID: ${LISTMONK_DEFAULT_TENANT_ID:-1}" \
                           "$HEALTH_CHECK_URL/api/config" 2>/dev/null); then
            log_error "Failed to check tenant functionality"
            return $EXIT_TENANT_FAILED
        fi
        
        http_code="${response: -3}"
        
        if [[ "$http_code" != "200" ]]; then
            log_error "Tenant context check failed (HTTP $http_code)"
            return $EXIT_TENANT_FAILED
        fi
        
        log_debug "Tenant functionality check passed"
    fi
    
    return 0
}

# Function to check filesystem health
check_filesystem_health() {
    log_debug "Checking filesystem health..."
    
    # Check uploads directory
    local uploads_dir="/listmonk/uploads"
    if [[ ! -d "$uploads_dir" ]]; then
        log_error "Uploads directory not found: $uploads_dir"
        return $EXIT_FILESYSTEM_FAILED
    fi
    
    if [[ ! -w "$uploads_dir" ]]; then
        log_warn "Uploads directory is not writable: $uploads_dir"
        # Not critical for health check, continue
    fi
    
    # Check tenant-segregated directories if enabled
    if [[ "${LISTMONK_TENANT_UPLOADS_SEGREGATED:-true}" == "true" ]]; then
        local tenant_dir="$uploads_dir/tenants/${LISTMONK_DEFAULT_TENANT_ID:-1}"
        if [[ ! -d "$tenant_dir" ]]; then
            log_warn "Default tenant upload directory not found: $tenant_dir"
            # Not critical during startup
        fi
    fi
    
    # Check log directory
    local log_dir="/var/log/listmonk"
    if [[ -d "$log_dir" && ! -w "$log_dir" ]]; then
        log_warn "Log directory is not writable: $log_dir"
    fi
    
    log_debug "Filesystem health check completed"
    return 0
}

# Function to perform comprehensive health check
perform_health_check() {
    local checks_passed=0
    local total_checks=5
    local exit_code=0
    
    log_info "Starting comprehensive health check..."
    
    # HTTP Health Check (critical)
    if check_http_health; then
        checks_passed=$((checks_passed + 1))
    else
        exit_code=$EXIT_HTTP_FAILED
    fi
    
    # Database Health Check (critical)
    if check_database_health; then
        checks_passed=$((checks_passed + 1))
    else
        exit_code=$EXIT_DB_FAILED
    fi
    
    # Redis Health Check (non-critical)
    if check_redis_health; then
        checks_passed=$((checks_passed + 1))
    else
        # Don't update exit code for Redis failures
        log_debug "Redis check failed but continuing (non-critical)"
        checks_passed=$((checks_passed + 1))  # Count as passed since it's optional
    fi
    
    # Tenant Health Check (critical if multi-tenancy is enabled)
    if check_tenant_health; then
        checks_passed=$((checks_passed + 1))
    else
        if [[ "${LISTMONK_MULTITENANCY_ENABLED:-false}" == "true" ]]; then
            exit_code=$EXIT_TENANT_FAILED
        else
            checks_passed=$((checks_passed + 1))  # Skip if multi-tenancy disabled
        fi
    fi
    
    # Filesystem Health Check (non-critical)
    if check_filesystem_health; then
        checks_passed=$((checks_passed + 1))
    else
        # Don't update exit code for filesystem warnings
        log_debug "Filesystem check had warnings but continuing"
        checks_passed=$((checks_passed + 1))
    fi
    
    # Summary
    if [[ $exit_code -eq 0 ]]; then
        log_info "Health check PASSED ($checks_passed/$total_checks checks successful)"
    else
        log_error "Health check FAILED ($checks_passed/$total_checks checks successful, exit code: $exit_code)"
    fi
    
    return $exit_code
}

# Function to run minimal health check (for frequent checks)
perform_minimal_health_check() {
    log_debug "Running minimal health check..."
    
    # Just check if the application responds
    if check_http_health; then
        log_info "Minimal health check PASSED"
        return 0
    else
        log_error "Minimal health check FAILED"
        return 1
    fi
}

# Function to display health check status
display_health_status() {
    echo "==================== LISTMONK HEALTH STATUS ===================="
    echo "Timestamp: $(date)"
    echo "Multi-tenancy enabled: ${LISTMONK_MULTITENANCY_ENABLED:-false}"
    echo "Default tenant ID: ${LISTMONK_DEFAULT_TENANT_ID:-1}"
    echo "Health check URL: $HEALTH_CHECK_URL"
    echo "=============================================================="
}

# Main function
main() {
    local check_type="${1:-full}"
    
    case "$check_type" in
        "full"|"comprehensive")
            if [[ "$LOG_LEVEL" != "quiet" ]]; then
                display_health_status
            fi
            perform_health_check
            ;;
        "minimal"|"quick")
            perform_minimal_health_check
            ;;
        "status")
            display_health_status
            exit 0
            ;;
        "--help"|"-h"|"help")
            echo "Usage: $0 [full|minimal|status|help]"
            echo ""
            echo "Options:"
            echo "  full        - Comprehensive health check (default)"
            echo "  minimal     - Quick HTTP endpoint check only"
            echo "  status      - Display health status information"
            echo "  help        - Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  HEALTH_CHECK_TIMEOUT    - Timeout in seconds (default: 10)"
            echo "  HEALTH_CHECK_URL        - Base URL to check (default: http://localhost:9000)"
            echo "  LOG_LEVEL              - Logging level (quiet|info|debug, default: info)"
            echo ""
            echo "Exit codes:"
            echo "  0 - Success"
            echo "  1 - HTTP health check failed"
            echo "  2 - Database health check failed"
            echo "  3 - Tenant functionality failed"
            echo "  4 - Redis health check failed"
            echo "  5 - Filesystem health check failed"
            exit 0
            ;;
        *)
            log_error "Unknown health check type: $check_type"
            echo "Use '$0 help' for usage information"
            exit 1
            ;;
    esac
}

# Execute main function with all arguments
main "$@"