#!/bin/bash
#
# Uninstallation script for xw
#
# This script removes xw binaries and systemd service.
# It must be run with root privileges.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Installation directories
INSTALL_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"
CONFIG_DIR="/etc/xw"
DATA_DIR="/opt/xw"
LOG_DIR="/var/log/xw"

# Print functions
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "This script must be run as root"
        echo "Please run: sudo $0"
        exit 1
    fi
}

# Stop and disable service
stop_service() {
    print_info "Stopping xw-server service..."
    
    if systemctl is-active --quiet xw-server; then
        systemctl stop xw-server
        print_info "✓ Service stopped"
    else
        print_info "Service not running"
    fi
    
    if systemctl is-enabled --quiet xw-server 2>/dev/null; then
        systemctl disable xw-server
        print_info "✓ Service disabled"
    fi
}

# Remove binary
remove_binary() {
    print_info "Removing binary..."
    
    rm -f "$INSTALL_DIR/xw"
    
    print_info "✓ Binary removed"
}

# Remove systemd service
remove_service() {
    print_info "Removing systemd service..."
    
    rm -f "$SYSTEMD_DIR/xw-server.service"
    systemctl daemon-reload
    
    print_info "✓ Systemd service removed"
}

# Ask about data removal
ask_remove_data() {
    echo ""
    echo "Do you want to remove user data and logs?"
    echo "This will delete:"
    echo "  - $DATA_DIR"
    echo "  - $LOG_DIR"
    echo "  - $CONFIG_DIR"
    echo ""
    read -p "Remove data? (y/N): " -n 1 -r
    echo
    
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Removing data directories..."
        rm -rf "$DATA_DIR"
        rm -rf "$LOG_DIR"
        rm -rf "$CONFIG_DIR"
        print_info "✓ Data removed"
        
        echo ""
        read -p "Remove xw user? (y/N): " -n 1 -r
        echo
        
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            if id "xw" &>/dev/null; then
                userdel xw
                print_info "✓ User removed"
            fi
        fi
    else
        print_info "Data preserved in $DATA_DIR"
    fi
}

# Main uninstallation
main() {
    echo "==============================================="
    echo "  XW Uninstallation Script"
    echo "==============================================="
    echo ""
    
    check_root
    
    stop_service
    remove_binary
    remove_service
    ask_remove_data
    
    echo ""
    echo "==============================================="
    echo -e "${GREEN}Uninstallation completed!${NC}"
    echo "==============================================="
    echo ""
}

main "$@"

