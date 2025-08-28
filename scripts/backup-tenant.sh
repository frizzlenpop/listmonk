#!/bin/bash
set -euo pipefail

# Multi-Tenant Listmonk Backup Script
# This script creates backups of specific tenants or all tenants

# Default configuration
BACKUP_DIR="${BACKUP_DIR:-/backup}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"
COMPRESS_BACKUPS="${COMPRESS_BACKUPS:-true}"
LOG_LEVEL="${LOG_LEVEL:-info}"

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    if [[ "$LOG_LEVEL" != "quiet" ]]; then
        echo -e "${GREEN}[BACKUP]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

log_warn() {
    if [[ "$LOG_LEVEL" != "quiet" ]]; then
        echo -e "${YELLOW}[BACKUP]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

log_error() {
    echo -e "${RED}[BACKUP]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1" >&2
}

log_debug() {
    if [[ "${LOG_LEVEL}" == "debug" ]]; then
        echo -e "${BLUE}[BACKUP]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

# Database connection parameters
DB_HOST="${LISTMONK_db__host:-localhost}"
DB_PORT="${LISTMONK_db__port:-5432}"
DB_USER="${LISTMONK_db__user:-listmonk}"
DB_PASSWORD="${LISTMONK_db__password:-}"
DB_NAME="${LISTMONK_db__database:-listmonk}"

# Function to execute SQL command and return result
execute_sql() {
    local sql_query="$1"
    local description="${2:-SQL Query}"
    
    log_debug "Executing: $description"
    
    PGPASSWORD="$DB_PASSWORD" psql \
        -h "$DB_HOST" \
        -p "$DB_PORT" \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        -t -c "$sql_query" \
        2>/dev/null | tr -d ' \n'
}

# Function to get list of all tenants
get_all_tenants() {
    log_debug "Getting list of all tenants..."
    
    local tenant_list
    tenant_list=$(PGPASSWORD="$DB_PASSWORD" psql \
        -h "$DB_HOST" \
        -p "$DB_PORT" \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        -t -c "SELECT id FROM tenants WHERE status = 'active' ORDER BY id;" \
        2>/dev/null | tr -d ' ')
    
    echo "$tenant_list"
}

# Function to get tenant information
get_tenant_info() {
    local tenant_id="$1"
    
    local tenant_info
    tenant_info=$(PGPASSWORD="$DB_PASSWORD" psql \
        -h "$DB_HOST" \
        -p "$DB_PORT" \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        -t -c "SELECT name, slug FROM tenants WHERE id = $tenant_id;" \
        2>/dev/null)
    
    echo "$tenant_info"
}

# Function to create database backup for specific tenant
backup_tenant_database() {
    local tenant_id="$1"
    local backup_file="$2"
    
    log_info "Creating database backup for tenant $tenant_id..."
    
    # List of tables that contain tenant data
    local tenant_tables=(
        "subscribers"
        "lists"
        "campaigns"
        "campaign_lists"
        "campaign_views"
        "link_clicks"
        "templates"
        "media"
        "bounces"
        "user_tenants"
        "tenant_settings"
    )
    
    # Create temporary SQL file
    local temp_sql="/tmp/tenant_backup_${tenant_id}_$$.sql"
    
    # Start with tenant metadata
    echo "-- Tenant $tenant_id Database Backup" > "$temp_sql"
    echo "-- Created: $(date)" >> "$temp_sql"
    echo "-- Multi-tenant Listmonk Backup" >> "$temp_sql"
    echo "" >> "$temp_sql"
    
    # Backup tenant record first
    echo "-- Tenant metadata" >> "$temp_sql"
    PGPASSWORD="$DB_PASSWORD" pg_dump \
        -h "$DB_HOST" \
        -p "$DB_PORT" \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        --data-only \
        --inserts \
        --table=tenants \
        --where="id = $tenant_id" \
        >> "$temp_sql" 2>/dev/null
    
    echo "" >> "$temp_sql"
    
    # Backup each tenant table
    for table in "${tenant_tables[@]}"; do
        # Check if table exists
        local table_exists
        table_exists=$(execute_sql "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = '$table';" "Check if $table exists")
        
        if [[ "$table_exists" == "1" ]]; then
            # Check if table has tenant_id column
            local has_tenant_id
            has_tenant_id=$(execute_sql "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = '$table' AND column_name = 'tenant_id';" "Check $table tenant_id column")
            
            if [[ "$has_tenant_id" == "1" ]]; then
                log_debug "Backing up table: $table"
                
                echo "-- Table: $table" >> "$temp_sql"
                PGPASSWORD="$DB_PASSWORD" pg_dump \
                    -h "$DB_HOST" \
                    -p "$DB_PORT" \
                    -U "$DB_USER" \
                    -d "$DB_NAME" \
                    --data-only \
                    --inserts \
                    --table="$table" \
                    --where="tenant_id = $tenant_id" \
                    >> "$temp_sql" 2>/dev/null || log_warn "Failed to backup table: $table"
                
                echo "" >> "$temp_sql"
            else
                log_debug "Table $table doesn't have tenant_id column, skipping"
            fi
        else
            log_debug "Table $table doesn't exist, skipping"
        fi
    done
    
    # Move to final location
    if [[ "$COMPRESS_BACKUPS" == "true" ]]; then
        gzip < "$temp_sql" > "$backup_file"
    else
        cp "$temp_sql" "$backup_file"
    fi
    
    # Clean up
    rm -f "$temp_sql"
    
    if [[ -f "$backup_file" ]]; then
        local file_size
        file_size=$(du -h "$backup_file" | cut -f1)
        log_info "Database backup completed: $backup_file ($file_size)"
        return 0
    else
        log_error "Database backup failed for tenant $tenant_id"
        return 1
    fi
}

# Function to backup tenant files
backup_tenant_files() {
    local tenant_id="$1"
    local backup_file="$2"
    
    local uploads_dir="/listmonk/uploads"
    local tenant_dir="$uploads_dir/tenants/$tenant_id"
    
    if [[ ! -d "$tenant_dir" ]]; then
        log_warn "Tenant upload directory not found: $tenant_dir"
        return 0
    fi
    
    log_info "Creating files backup for tenant $tenant_id..."
    
    if [[ "$COMPRESS_BACKUPS" == "true" ]]; then
        if tar -czf "$backup_file" -C "$uploads_dir/tenants" "$tenant_id" 2>/dev/null; then
            local file_size
            file_size=$(du -h "$backup_file" | cut -f1)
            log_info "Files backup completed: $backup_file ($file_size)"
            return 0
        else
            log_error "Files backup failed for tenant $tenant_id"
            return 1
        fi
    else
        local target_dir="${backup_file%/*}/tenant_${tenant_id}_files"
        if cp -r "$tenant_dir" "$target_dir" 2>/dev/null; then
            log_info "Files backup completed: $target_dir"
            return 0
        else
            log_error "Files backup failed for tenant $tenant_id"
            return 1
        fi
    fi
}

# Function to cleanup old backups
cleanup_old_backups() {
    log_info "Cleaning up backups older than $BACKUP_RETENTION_DAYS days..."
    
    local deleted_count=0
    
    # Find and delete old backup files
    while IFS= read -r -d '' file; do
        rm -f "$file"
        deleted_count=$((deleted_count + 1))
        log_debug "Deleted old backup: $(basename "$file")"
    done < <(find "$BACKUP_DIR" -name "tenant_*_backup_*.sql*" -type f -mtime +"$BACKUP_RETENTION_DAYS" -print0 2>/dev/null)
    
    # Find and delete old file backups
    while IFS= read -r -d '' file; do
        rm -rf "$file"
        deleted_count=$((deleted_count + 1))
        log_debug "Deleted old file backup: $(basename "$file")"
    done < <(find "$BACKUP_DIR" -name "tenant_*_files_*.tar.gz" -type f -mtime +"$BACKUP_RETENTION_DAYS" -print0 2>/dev/null)
    
    if [[ $deleted_count -gt 0 ]]; then
        log_info "Cleaned up $deleted_count old backup files"
    else
        log_debug "No old backups to clean up"
    fi
}

# Function to backup single tenant
backup_single_tenant() {
    local tenant_id="$1"
    
    # Get tenant information
    local tenant_info
    tenant_info=$(get_tenant_info "$tenant_id")
    
    if [[ -z "$tenant_info" ]]; then
        log_error "Tenant $tenant_id not found"
        return 1
    fi
    
    local tenant_name=$(echo "$tenant_info" | cut -d'|' -f1 | tr -d ' ')
    local tenant_slug=$(echo "$tenant_info" | cut -d'|' -f2 | tr -d ' ')
    
    log_info "Starting backup for tenant: $tenant_name (ID: $tenant_id, Slug: $tenant_slug)"
    
    # Create backup directory
    mkdir -p "$BACKUP_DIR"
    
    # Generate backup filenames with timestamp
    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local db_backup_file
    local files_backup_file
    
    if [[ "$COMPRESS_BACKUPS" == "true" ]]; then
        db_backup_file="$BACKUP_DIR/tenant_${tenant_id}_backup_${timestamp}.sql.gz"
        files_backup_file="$BACKUP_DIR/tenant_${tenant_id}_files_${timestamp}.tar.gz"
    else
        db_backup_file="$BACKUP_DIR/tenant_${tenant_id}_backup_${timestamp}.sql"
        files_backup_file="$BACKUP_DIR/tenant_${tenant_id}_files_${timestamp}"
    fi
    
    local success=true
    
    # Backup database
    if ! backup_tenant_database "$tenant_id" "$db_backup_file"; then
        success=false
    fi
    
    # Backup files
    if ! backup_tenant_files "$tenant_id" "$files_backup_file"; then
        success=false
    fi
    
    if [[ "$success" == "true" ]]; then
        log_info "Tenant backup completed successfully: $tenant_name (ID: $tenant_id)"
        return 0
    else
        log_error "Tenant backup completed with errors: $tenant_name (ID: $tenant_id)"
        return 1
    fi
}

# Function to backup all tenants
backup_all_tenants() {
    log_info "Starting backup for all active tenants..."
    
    local tenant_list
    tenant_list=$(get_all_tenants)
    
    if [[ -z "$tenant_list" ]]; then
        log_warn "No active tenants found"
        return 0
    fi
    
    local success_count=0
    local total_count=0
    
    # Process each tenant
    while IFS= read -r tenant_id; do
        if [[ -n "$tenant_id" ]]; then
            total_count=$((total_count + 1))
            
            if backup_single_tenant "$tenant_id"; then
                success_count=$((success_count + 1))
            fi
        fi
    done <<< "$tenant_list"
    
    log_info "Backup completed: $success_count/$total_count tenants backed up successfully"
    
    if [[ $success_count -eq $total_count ]]; then
        return 0
    else
        return 1
    fi
}

# Function to display usage information
show_usage() {
    echo "Multi-Tenant Listmonk Backup Script"
    echo ""
    echo "Usage: $0 [OPTIONS] [TENANT_ID|all]"
    echo ""
    echo "Arguments:"
    echo "  TENANT_ID    - ID of specific tenant to backup"
    echo "  all          - Backup all active tenants (default)"
    echo ""
    echo "Options:"
    echo "  --backup-dir DIR         - Backup directory (default: /backup)"
    echo "  --retention-days DAYS    - Keep backups for N days (default: 30)"
    echo "  --no-compress           - Don't compress backup files"
    echo "  --cleanup-only          - Only perform cleanup, no backup"
    echo "  --log-level LEVEL       - Logging level (quiet|info|debug, default: info)"
    echo "  --help, -h              - Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  BACKUP_DIR               - Backup directory"
    echo "  BACKUP_RETENTION_DAYS    - Retention period in days"
    echo "  COMPRESS_BACKUPS         - Compress backups (true|false)"
    echo "  LOG_LEVEL               - Logging level"
    echo ""
    echo "Database connection (use existing Listmonk environment variables):"
    echo "  LISTMONK_db__host"
    echo "  LISTMONK_db__port"
    echo "  LISTMONK_db__user"
    echo "  LISTMONK_db__password"
    echo "  LISTMONK_db__database"
    echo ""
    echo "Examples:"
    echo "  $0                      # Backup all tenants"
    echo "  $0 1                    # Backup tenant with ID 1"
    echo "  $0 --cleanup-only       # Clean up old backups only"
    echo "  $0 all --no-compress    # Backup all tenants without compression"
}

# Main function
main() {
    local tenant_to_backup="all"
    local cleanup_only=false
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --backup-dir)
                BACKUP_DIR="$2"
                shift 2
                ;;
            --retention-days)
                BACKUP_RETENTION_DAYS="$2"
                shift 2
                ;;
            --no-compress)
                COMPRESS_BACKUPS="false"
                shift
                ;;
            --cleanup-only)
                cleanup_only=true
                shift
                ;;
            --log-level)
                LOG_LEVEL="$2"
                shift 2
                ;;
            --help|-h)
                show_usage
                exit 0
                ;;
            -*)
                log_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
            *)
                tenant_to_backup="$1"
                shift
                ;;
        esac
    done
    
    # Validate backup directory
    if [[ ! -d "$BACKUP_DIR" ]]; then
        log_info "Creating backup directory: $BACKUP_DIR"
        if ! mkdir -p "$BACKUP_DIR"; then
            log_error "Failed to create backup directory: $BACKUP_DIR"
            exit 1
        fi
    fi
    
    if [[ ! -w "$BACKUP_DIR" ]]; then
        log_error "Backup directory is not writable: $BACKUP_DIR"
        exit 1
    fi
    
    # Perform cleanup
    cleanup_old_backups
    
    if [[ "$cleanup_only" == "true" ]]; then
        log_info "Cleanup completed"
        exit 0
    fi
    
    # Check if multi-tenancy is enabled
    if [[ "${LISTMONK_MULTITENANCY_ENABLED:-false}" != "true" ]]; then
        log_error "Multi-tenancy is not enabled"
        exit 1
    fi
    
    # Perform backup
    if [[ "$tenant_to_backup" == "all" ]]; then
        backup_all_tenants
    elif [[ "$tenant_to_backup" =~ ^[0-9]+$ ]]; then
        backup_single_tenant "$tenant_to_backup"
    else
        log_error "Invalid tenant ID: $tenant_to_backup (must be a number or 'all')"
        exit 1
    fi
}

# Execute main function with all arguments
main "$@"