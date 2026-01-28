#!/bin/bash
#
# Installation script for xw
#
# This script installs xw binaries and configures the systemd service.
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

# Binary path
XW_BINARY="bin/xw"

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

# Check if binary exists
check_binary() {
    if [ ! -f "$XW_BINARY" ]; then
        print_error "Binary not found. Please build first:"
        echo "  make build"
        exit 1
    fi
}

# Create xw user
create_user() {
    if id "xw" &>/dev/null; then
        print_info "User 'xw' already exists"
    else
        print_info "Creating system user 'xw'..."
        useradd --system --no-create-home --shell /bin/false xw
        print_info "✓ User 'xw' created"
    fi
}

# Create directories
create_directories() {
    print_info "Creating directories..."
    
    install -d -m 755 "$CONFIG_DIR"
    install -d -m 755 -o xw -g xw "$DATA_DIR"
    install -d -m 755 -o xw -g xw "$DATA_DIR/models"
    install -d -m 755 -o xw -g xw "$LOG_DIR"
    
    print_info "✓ Directories created"
}

# Install binary
install_binary() {
    print_info "Installing binary..."
    
    install -m 755 "$XW_BINARY" "$INSTALL_DIR/xw"
    
    print_info "✓ Binary installed to $INSTALL_DIR"
}

# Install systemd service
install_service() {
    print_info "Installing systemd service..."
    
    install -m 644 systemd/xw-server.service "$SYSTEMD_DIR/xw-server.service"
    systemctl daemon-reload
    
    print_info "✓ Systemd service installed"
}

# Main installation
main() {
    echo "==============================================="
    echo "  XW Installation Script"
    echo "==============================================="
    echo ""
    
    check_root
    check_binary
    
    create_user
    create_directories
    install_binary
    install_service
    
    echo ""
    echo "==============================================="
    echo -e "${GREEN}Installation completed successfully!${NC}"
    echo "==============================================="
    echo ""
    echo "Next steps:"
    echo "  1. Start the service:    sudo systemctl start xw-server"
    echo "  2. Enable on boot:       sudo systemctl enable xw-server"
    echo "  3. Check status:         sudo systemctl status xw-server"
    echo "  4. View logs:            sudo journalctl -u xw-server -f"
    echo ""
    echo "Test the CLI:"
    echo "  xw version"
    echo "  xw ls -a"
    echo ""
}

main "$@"

