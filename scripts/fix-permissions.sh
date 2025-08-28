#!/bin/bash

# Fix permissions for Listmonk multi-tenant deployment
# Run this script with sudo to fix ownership issues

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "Fixing permissions for Listmonk multi-tenant deployment..."

# Get the actual user (not root when using sudo)
if [ -n "$SUDO_USER" ]; then
    ACTUAL_USER=$SUDO_USER
    ACTUAL_GROUP=$(id -gn $SUDO_USER)
else
    ACTUAL_USER=$(whoami)
    ACTUAL_GROUP=$(id -gn)
fi

echo "Setting ownership to: $ACTUAL_USER:$ACTUAL_GROUP"

# Change ownership of the entire project directory
chown -R $ACTUAL_USER:$ACTUAL_GROUP "$PROJECT_DIR"

# Make sure scripts are executable
chmod +x "$PROJECT_DIR/scripts/"*.sh

# Create necessary directories with correct permissions
mkdir -p "$PROJECT_DIR/backups"
mkdir -p "$PROJECT_DIR/uploads"
mkdir -p "$PROJECT_DIR/logs"

# Set proper permissions
chmod 755 "$PROJECT_DIR/backups"
chmod 755 "$PROJECT_DIR/uploads" 
chmod 755 "$PROJECT_DIR/logs"

echo "Permissions fixed successfully!"
echo "You can now run the deployment script as a regular user:"
echo "./scripts/deploy-multitenant.sh"