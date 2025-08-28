#!/bin/bash

# Listmonk Multi-Tenant Deployment Script
# This script handles the complete deployment of multi-tenant Listmonk

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BACKUP_DIR="${PROJECT_DIR}/backups"
LOG_FILE="${PROJECT_DIR}/deployment.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging function
log() {
    echo -e "${BLUE}[$(date '+%Y-%m-%d %H:%M:%S')]${NC} $1" | tee -a "$LOG_FILE"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" | tee -a "$LOG_FILE"
    exit 1
}

warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1" | tee -a "$LOG_FILE"
}

success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1" | tee -a "$LOG_FILE"
}

# Fix permissions if running with sudo or having permission issues
fix_permissions() {
    log "Checking and fixing permissions..."
    
    # Check if we need to fix permissions
    if [ "$EUID" -eq 0 ] && [ -n "$SUDO_USER" ]; then
        log "Running as sudo, fixing file ownership..."
        ACTUAL_USER=$SUDO_USER
        ACTUAL_GROUP=$(id -gn $SUDO_USER)
        
        # Change ownership of the entire project directory
        chown -R $ACTUAL_USER:$ACTUAL_GROUP "$PROJECT_DIR"
        log "Changed ownership to $ACTUAL_USER:$ACTUAL_GROUP"
        
    elif [ "$EUID" -eq 0 ]; then
        warning "Running as root without sudo - files will be owned by root"
    fi
    
    # Create necessary directories
    mkdir -p "$BACKUP_DIR"
    mkdir -p "$PROJECT_DIR/uploads"
    mkdir -p "$(dirname "$LOG_FILE")"
    
    # Ensure log file is writable
    touch "$LOG_FILE" 2>/dev/null || {
        if [ "$EUID" -eq 0 ] && [ -n "$SUDO_USER" ]; then
            touch "$LOG_FILE"
            chown $ACTUAL_USER:$ACTUAL_GROUP "$LOG_FILE"
        else
            error "Cannot create log file $LOG_FILE - check permissions"
        fi
    }
    
    # Make scripts executable
    chmod +x "$SCRIPT_DIR"/*.sh 2>/dev/null || true
    
    success "Permissions fixed"
}

# Detect Docker Compose command
detect_docker_compose() {
    if command -v docker-compose &> /dev/null; then
        DOCKER_COMPOSE="docker-compose"
    elif docker compose version &> /dev/null 2>&1; then
        DOCKER_COMPOSE="docker compose"
    else
        error "Docker Compose is not installed (tried both docker-compose and docker compose)"
    fi
    log "Using Docker Compose command: $DOCKER_COMPOSE"
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check Docker
    if ! command -v docker &> /dev/null; then
        error "Docker is not installed"
    fi
    
    # Detect Docker Compose version
    detect_docker_compose
    
    # Check for required files
    if [ ! -f "$PROJECT_DIR/docker-compose.multitenant.yml" ]; then
        error "docker-compose.multitenant.yml not found"
    fi
    
    if [ ! -f "$PROJECT_DIR/.env.multitenant.sample" ]; then
        error ".env.multitenant.sample not found"
    fi
    
    if [ ! -f "$PROJECT_DIR/migrations/001_add_multitenancy.sql" ]; then
        error "migrations/001_add_multitenancy.sql not found"
    fi
    
    success "Prerequisites check passed"
}

# Create environment file
create_env_file() {
    log "Creating environment configuration..."
    
    if [ ! -f "$PROJECT_DIR/.env" ]; then
        log "Copying .env.multitenant.sample to .env"
        cp "$PROJECT_DIR/.env.multitenant.sample" "$PROJECT_DIR/.env"
        warning "Please edit .env file with your configuration before continuing"
        read -p "Press Enter after editing .env file..."
    else
        log ".env file already exists"
    fi
}

# Create backup
create_backup() {
    log "Creating backup..."
    
    mkdir -p "$BACKUP_DIR"
    BACKUP_NAME="listmonk-backup-$(date +%Y%m%d-%H%M%S)"
    BACKUP_PATH="$BACKUP_DIR/$BACKUP_NAME"
    
    # If containers are running, backup database
    if docker ps --format "table {{.Names}}" | grep -q "listmonk_db_mt"; then
        log "Creating database backup..."
        docker exec listmonk_db_mt pg_dump -U listmonk listmonk > "$BACKUP_PATH.sql"
        
        # Backup uploads if they exist
        if [ -d "$PROJECT_DIR/uploads" ]; then
            log "Creating uploads backup..."
            tar -czf "$BACKUP_PATH-uploads.tar.gz" -C "$PROJECT_DIR" uploads
        fi
        
        success "Backup created: $BACKUP_PATH"
    else
        log "No running containers found, skipping database backup"
    fi
}

# Deploy application
deploy_application() {
    log "Deploying multi-tenant Listmonk..."
    
    cd "$PROJECT_DIR"
    
    # Build custom multi-tenant image
    log "Building multi-tenant Listmonk image..."
    $DOCKER_COMPOSE -f docker-compose.multitenant.yml build --no-cache
    
    # Pull other images (database, redis)
    log "Pulling supporting Docker images..."
    $DOCKER_COMPOSE -f docker-compose.multitenant.yml pull db redis
    
    # Start services
    log "Starting services..."
    $DOCKER_COMPOSE -f docker-compose.multitenant.yml up -d
    
    # Wait for database to be ready
    log "Waiting for database to be ready..."
    for i in {1..30}; do
        if docker exec listmonk_db_mt pg_isready -U listmonk &> /dev/null; then
            success "Database is ready"
            break
        fi
        sleep 2
    done
    
    # Check if services are healthy
    log "Checking service health..."
    sleep 10
    
    if docker ps --format "table {{.Names}}\t{{.Status}}" | grep -q "Up"; then
        success "Services are running"
    else
        error "Some services failed to start"
    fi
}

# Initialize tenants
initialize_tenants() {
    log "Initializing tenant system..."
    
    # Wait for app to be fully ready
    log "Waiting for application to be ready..."
    for i in {1..30}; do
        if curl -s http://localhost:9000/api/health > /dev/null 2>&1; then
            success "Application is ready"
            break
        fi
        sleep 2
    done
    
    # Apply the multi-tenancy migration
    log "Applying multi-tenancy database migration..."
    if docker exec -i listmonk_db_mt psql -U listmonk -d listmonk < "$PROJECT_DIR/migrations/001_add_multitenancy.sql"; then
        success "Multi-tenancy migration applied successfully"
    else
        warning "Migration may have failed or was already applied"
    fi
    
    # Verify default tenant exists
    log "Verifying default tenant..."
    TENANT_COUNT=$(docker exec listmonk_db_mt psql -U listmonk -d listmonk -t -c "SELECT COUNT(*) FROM tenants WHERE id = 1;" 2>/dev/null | tr -d ' ')
    if [ "$TENANT_COUNT" = "1" ]; then
        success "Default tenant exists"
    elif [ "$TENANT_COUNT" = "0" ]; then
        log "Default tenant not found, will be created on first admin login"
    else
        warning "Could not verify tenant status"
    fi
}

# Run health checks
run_health_checks() {
    log "Running health checks..."
    
    # Check application health
    APP_URL="http://localhost:9000"
    if curl -s "$APP_URL/api/health" > /dev/null; then
        success "Application health check passed"
    else
        error "Application health check failed"
    fi
    
    # Check tenant health
    if curl -s "$APP_URL/api/health/tenants" > /dev/null; then
        success "Tenant health check passed"
    else
        warning "Tenant health check failed - may be normal if admin login required"
    fi
    
    # Check database connection
    if docker exec listmonk_db_mt psql -U listmonk -d listmonk -c "SELECT 1;" > /dev/null; then
        success "Database connection check passed"
    else
        error "Database connection check failed"
    fi
}

# Migrate existing data
migrate_existing_data() {
    read -p "Do you want to migrate existing single-tenant data? (y/N): " migrate_choice
    
    if [[ $migrate_choice =~ ^[Yy]$ ]]; then
        log "Migrating existing data to multi-tenant structure..."
        
        # Run migration script
        docker exec listmonk_app_mt /app/scripts/migrate-to-multitenancy.sh
        
        success "Data migration completed"
    else
        log "Skipping data migration"
    fi
}

# Display deployment information
show_deployment_info() {
    log "Deployment completed successfully!"
    echo
    echo -e "${GREEN}================================${NC}"
    echo -e "${GREEN}  LISTMONK MULTI-TENANT SETUP   ${NC}"
    echo -e "${GREEN}================================${NC}"
    echo
    echo "Application URL: http://localhost:9000"
    echo "Admin Panel: http://localhost:9000/admin"
    echo "Health Check: http://localhost:9000/api/health/tenants"
    echo
    echo "Docker Services:"
    $DOCKER_COMPOSE -f docker-compose.multitenant.yml ps
    echo
    echo "Logs: $DOCKER_COMPOSE -f docker-compose.multitenant.yml logs -f"
    echo "Stop: $DOCKER_COMPOSE -f docker-compose.multitenant.yml down"
    echo
    echo "Configuration:"
    echo "- Environment: .env"
    echo "- Deployment log: $LOG_FILE"
    echo "- Backups: $BACKUP_DIR"
    echo
    echo -e "${YELLOW}Next Steps:${NC}"
    echo "1. Login to admin panel and create your first tenant"
    echo "2. Configure DNS for subdomain routing (see DOCKER_MULTITENANT_SETUP.md)"
    echo "3. Set up SSL certificates for production"
    echo "4. Configure monitoring and backups"
    echo
}

# Rollback function
rollback_deployment() {
    log "Rolling back deployment..."
    
    # Detect docker compose command
    detect_docker_compose
    
    # Stop services
    $DOCKER_COMPOSE -f docker-compose.multitenant.yml down
    
    # Restore from backup if available
    LATEST_BACKUP=$(ls -t "$BACKUP_DIR"/*.sql 2>/dev/null | head -1)
    if [ -n "$LATEST_BACKUP" ]; then
        read -p "Restore database from backup $LATEST_BACKUP? (y/N): " restore_choice
        if [[ $restore_choice =~ ^[Yy]$ ]]; then
            log "Restoring database from backup..."
            docker exec -i listmonk_db_mt psql -U listmonk -d listmonk < "$LATEST_BACKUP"
            success "Database restored from backup"
        fi
    fi
    
    success "Rollback completed"
}

# Main deployment function
main() {
    echo -e "${BLUE}"
    echo "================================================================"
    echo "            LISTMONK MULTI-TENANT DEPLOYMENT"
    echo "================================================================"
    echo -e "${NC}"
    
    # Fix permissions first (before creating log file)
    fix_permissions
    
    # Check if this is a rollback
    if [ "$1" = "--rollback" ]; then
        rollback_deployment
        exit 0
    fi
    
    # Check if this is a fresh install or upgrade
    INSTALL_TYPE="fresh"
    if docker ps --format "table {{.Names}}" | grep -q "listmonk"; then
        INSTALL_TYPE="upgrade"
        warning "Existing Listmonk installation detected"
    fi
    
    # Deployment steps
    check_prerequisites
    create_env_file
    
    if [ "$INSTALL_TYPE" = "upgrade" ]; then
        create_backup
        migrate_existing_data
    fi
    
    deploy_application
    initialize_tenants
    run_health_checks
    show_deployment_info
}

# Handle signals for cleanup
trap 'error "Deployment interrupted"' INT TERM

# Run deployment
main "$@"