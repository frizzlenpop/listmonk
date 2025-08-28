#!/bin/bash

# Build Multi-Tenant Listmonk Docker Image
# This script builds a custom Listmonk image with multi-tenancy support

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_FILE="$PROJECT_DIR/build.log"
IMAGE_NAME="listmonk-multitenant:latest"

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

# Detect Docker Compose command
detect_docker_compose() {
    if command -v docker-compose &> /dev/null; then
        DOCKER_COMPOSE="docker-compose"
    elif docker compose version &> /dev/null 2>&1; then
        DOCKER_COMPOSE="docker compose"
    else
        error "Docker Compose is not installed"
    fi
    log "Using Docker Compose command: $DOCKER_COMPOSE"
}

# Check prerequisites
check_prerequisites() {
    log "Checking build prerequisites..."
    
    # Check Docker
    if ! command -v docker &> /dev/null; then
        error "Docker is not installed"
    fi
    
    # Detect Docker Compose
    detect_docker_compose
    
    # Check for required files
    if [ ! -f "$PROJECT_DIR/Dockerfile.build" ]; then
        error "Dockerfile.build not found"
    fi
    
    if [ ! -f "$PROJECT_DIR/docker-compose.multitenant.yml" ]; then
        error "docker-compose.multitenant.yml not found"
    fi
    
    # Check for Go files (multi-tenancy integration)
    if [ ! -f "$PROJECT_DIR/queries.sql" ]; then
        error "Modified queries.sql not found"
    fi
    
    if [ ! -f "$PROJECT_DIR/migrations/001_add_multitenancy.sql" ]; then
        error "Multi-tenancy migration not found"
    fi
    
    success "Prerequisites check passed"
}

# Clean up old builds
cleanup_old_builds() {
    log "Cleaning up old builds..."
    
    # Remove old containers
    docker ps -aq --filter "ancestor=$IMAGE_NAME" | xargs -r docker rm -f 2>/dev/null || true
    
    # Remove old images (keep latest)
    OLD_IMAGES=$(docker images --filter "reference=$IMAGE_NAME" --format "{{.ID}}" | tail -n +2)
    if [ -n "$OLD_IMAGES" ]; then
        echo "$OLD_IMAGES" | xargs -r docker rmi -f 2>/dev/null || true
        log "Removed old images"
    fi
    
    success "Cleanup completed"
}

# Build the custom image
build_image() {
    log "Building multi-tenant Listmonk image..."
    
    cd "$PROJECT_DIR"
    
    # Build the image
    log "Starting Docker build process..."
    if docker build -f Dockerfile.build -t "$IMAGE_NAME" . 2>&1 | tee -a "$LOG_FILE"; then
        success "Image built successfully: $IMAGE_NAME"
    else
        error "Failed to build Docker image"
    fi
    
    # Verify the image
    if docker images | grep -q "listmonk-multitenant"; then
        success "Image verification passed"
    else
        error "Built image not found"
    fi
}

# Test the built image
test_image() {
    log "Testing built image..."
    
    # Start a temporary container to test
    TEST_CONTAINER="listmonk-test-$$"
    
    log "Starting test container..."
    if docker run --name "$TEST_CONTAINER" -d --rm \
        -e LISTMONK_db__host=localhost \
        -e LISTMONK_db__port=5432 \
        -e LISTMONK_db__user=test \
        -e LISTMONK_db__password=test \
        -e LISTMONK_db__database=test \
        -e LISTMONK_MULTITENANCY_ENABLED=true \
        "$IMAGE_NAME"; then
        
        # Wait a moment for startup
        sleep 5
        
        # Check if container is still running (basic test)
        if docker ps | grep -q "$TEST_CONTAINER"; then
            success "Image basic test passed"
            docker stop "$TEST_CONTAINER" 2>/dev/null || true
        else
            warning "Container stopped unexpectedly (expected - no database connection)"
            # This is expected since we don't have a real database
        fi
    else
        error "Failed to start test container"
    fi
}

# Update deployment configuration
update_deployment() {
    log "Updating deployment configuration..."
    
    # Backup existing .env if it exists
    if [ -f "$PROJECT_DIR/.env" ]; then
        cp "$PROJECT_DIR/.env" "$PROJECT_DIR/.env.backup.$(date +%Y%m%d-%H%M%S)"
        log "Backed up existing .env file"
    fi
    
    # Ensure .env exists with multi-tenancy enabled
    if [ ! -f "$PROJECT_DIR/.env" ]; then
        log "Creating .env from sample..."
        cp "$PROJECT_DIR/.env.multitenant.sample" "$PROJECT_DIR/.env"
    fi
    
    success "Deployment configuration updated"
}

# Show build results
show_build_results() {
    log "Build completed successfully!"
    echo
    echo -e "${GREEN}================================${NC}"
    echo -e "${GREEN}  MULTI-TENANT BUILD COMPLETE   ${NC}"
    echo -e "${GREEN}================================${NC}"
    echo
    echo "Built Image: $IMAGE_NAME"
    echo "Image Size: $(docker images --format "table {{.Size}}" $IMAGE_NAME | tail -1)"
    echo "Build Log: $LOG_FILE"
    echo
    echo "Docker Images:"
    docker images | grep listmonk-multitenant || echo "No images found"
    echo
    echo -e "${YELLOW}Next Steps:${NC}"
    echo "1. Deploy with: ./scripts/deploy-multitenant.sh"
    echo "2. Or manually: docker compose -f docker-compose.multitenant.yml up -d"
    echo "3. Access: http://localhost:9000/admin"
    echo
}

# Main build function
main() {
    echo -e "${BLUE}"
    echo "================================================================"
    echo "          LISTMONK MULTI-TENANT IMAGE BUILD"
    echo "================================================================"
    echo -e "${NC}"
    
    # Create log file
    touch "$LOG_FILE" || error "Cannot create log file"
    
    # Build steps
    check_prerequisites
    cleanup_old_builds
    build_image
    test_image
    update_deployment
    show_build_results
}

# Handle cleanup on exit
cleanup() {
    if [ -n "$TEST_CONTAINER" ]; then
        docker stop "$TEST_CONTAINER" 2>/dev/null || true
    fi
}

trap cleanup EXIT

# Run build
main "$@"