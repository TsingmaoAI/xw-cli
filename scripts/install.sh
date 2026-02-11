#!/bin/bash
#
# Installation script for xw
#
# This script installs xw binaries and optionally configures the systemd service.
# It supports both user-level and system-level installations.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Installation mode (default to system install)
SYSTEM_INSTALL=true

# Installation directories (will be set based on mode)
INSTALL_DIR=""
CONFIG_DIR=""
DATA_DIR=""
SYSTEMD_DIR="/etc/systemd/system"
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

# Usage information
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Install xw - AI Inference on Domestic Chips

OPTIONS:
    --user          Install for current user only (no root required)
                    Binary: ~/.local/bin
                    Config: ~/.xw
                    Data: ~/.xw/data
                    
    (no options)    System installation (requires root)
                    Binary: /usr/local/bin
                    Config: /etc/xw
                    Data: /var/lib/xw
                    Systemd service: enabled
    
    -h, --help      Show this help message

EXAMPLES:
    # System installation (default, requires root)
    sudo bash scripts/install.sh
    
    # User installation
    bash scripts/install.sh --user

EOF
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --user)
                SYSTEM_INSTALL=false
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

# Check if running as root (required for system install)
check_root() {
    if [ "$SYSTEM_INSTALL" = true ] && [ "$EUID" -ne 0 ]; then
        print_error "System installation requires root privileges"
        echo "Please run: sudo $0"
        echo "Or use --user flag for user installation: $0 --user"
        exit 1
    fi
    
    if [ "$SYSTEM_INSTALL" = false ] && [ "$EUID" -eq 0 ]; then
        print_warn "Running as root in --user mode"
        print_warn "This will install for root user's home, not system-wide"
        sleep 2
    fi
}

# Set installation directories based on mode
set_directories() {
    if [ "$SYSTEM_INSTALL" = true ]; then
        print_info "System installation mode"
        INSTALL_DIR="/usr/local/bin"
        CONFIG_DIR="/etc/xw"
        DATA_DIR="/var/lib/xw"
    else
        print_info "User installation mode"
        INSTALL_DIR="$HOME/.local/bin"
        CONFIG_DIR="$HOME/.xw"
        DATA_DIR="$HOME/.xw/data"
    fi
    
    print_info "Installation directories:"
    print_info "  Binary: $INSTALL_DIR"
    print_info "  Config: $CONFIG_DIR"
    print_info "  Data:   $DATA_DIR"
    echo ""
}

# Check if binary exists
check_binary() {
    if [ ! -f "$XW_BINARY" ]; then
        print_error "Binary not found at: $XW_BINARY"
        echo "Please make sure you're running this from the extracted package directory"
        exit 1
    fi
}

# Create xw system user (only for system install)
create_user() {
    if [ "$SYSTEM_INSTALL" = false ]; then
        return
    fi
    
    if id "xw" &>/dev/null; then
        print_info "System user 'xw' already exists"
    else
        print_info "Creating system user 'xw'..."
        useradd --system --no-create-home --shell /bin/false xw
        print_info "✓ System user 'xw' created"
    fi
}

# Create directories
create_directories() {
    print_info "Creating directories..."
    
    if [ "$SYSTEM_INSTALL" = true ]; then
        # System directories
        install -d -m 755 "$CONFIG_DIR"
        install -d -m 755 -o xw -g xw "$DATA_DIR"
        install -d -m 755 -o xw -g xw "$DATA_DIR/models"
        install -d -m 755 -o xw -g xw "$LOG_DIR"
    else
        # User directories
        mkdir -p "$INSTALL_DIR"
        mkdir -p "$CONFIG_DIR"
        mkdir -p "$DATA_DIR"
        mkdir -p "$DATA_DIR/models"
    fi
    
    print_info "✓ Directories created"
}

# Install configuration files
install_configs() {
    print_info "Installing configuration files..."
    
    # Find version directory in configs/
    if [ ! -d "configs" ]; then
        print_warn "Configuration directory not found in package, skipping"
        return
    fi
    
    # Get the version directory (should be only one version directory)
    local version_dir=$(ls -1 configs/ | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
    
    if [ -z "$version_dir" ]; then
        print_error "No version directory found in configs/"
        return 1
    fi
    
    print_info "Installing config version: $version_dir"
    
    # Check if this version already exists
    if [ -d "$CONFIG_DIR/$version_dir" ]; then
        print_warn "Configuration version $version_dir already exists"
        print_info "Creating backup..."
        mv "$CONFIG_DIR/$version_dir" "$CONFIG_DIR/${version_dir}.backup.$(date +%Y%m%d_%H%M%S)"
    fi
    
    # Copy entire version directory
    cp -r "configs/$version_dir" "$CONFIG_DIR/"
    print_info "✓ Configuration version $version_dir installed to $CONFIG_DIR/$version_dir"
}

# Install binary
install_binary() {
    print_info "Installing binary..."
    
    # Check if binary already exists
    if [ -f "$INSTALL_DIR/xw" ]; then
        print_warn "Existing binary found at $INSTALL_DIR/xw, will be overwritten"
    fi
    
    # Install (will overwrite if exists)
    install -m 755 "$XW_BINARY" "$INSTALL_DIR/xw"
    
    print_info "✓ Binary installed to $INSTALL_DIR/xw"
    
    # Add to PATH for user installation
    if [ "$SYSTEM_INSTALL" = false ]; then
        if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
            print_info "Adding $INSTALL_DIR to PATH..."
            
            # Detect shell and update rc file
            if [ -n "$BASH_VERSION" ] && [ -f "$HOME/.bashrc" ]; then
                echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.bashrc"
                print_info "Added to ~/.bashrc"
            elif [ -n "$ZSH_VERSION" ] && [ -f "$HOME/.zshrc" ]; then
                echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.zshrc"
                print_info "Added to ~/.zshrc"
            fi
            
            export PATH="$INSTALL_DIR:$PATH"
        fi
    fi
}

# Install systemd service (only for system install)
install_service() {
    if [ "$SYSTEM_INSTALL" = false ]; then
        return
    fi
    
    print_info "Installing systemd service..."
    
    if [ ! -f "systemd/xw-server.service" ]; then
        print_warn "Systemd service file not found, skipping"
        return
    fi
    
    install -m 644 systemd/xw-server.service "$SYSTEMD_DIR/xw-server.service"
    
    # Update service file paths if needed
    sed -i "s|/etc/xw|$CONFIG_DIR|g" "$SYSTEMD_DIR/xw-server.service"
    sed -i "s|/var/lib/xw|$DATA_DIR|g" "$SYSTEMD_DIR/xw-server.service"
    
    systemctl daemon-reload
    
    print_info "✓ Systemd service installed"
}

# Main installation
main() {
    echo "==============================================="
    echo "  XW Installation Script"
    echo "==============================================="
    echo ""
    
    parse_args "$@"
    check_root
    set_directories
    check_binary
    
    create_user
    create_directories
    install_configs
    install_binary
    install_service
    
    echo ""
    echo "==============================================="
    echo -e "${GREEN}Installation completed successfully!${NC}"
    echo "==============================================="
    echo ""
    
    if [ "$SYSTEM_INSTALL" = true ]; then
        echo "System installation complete!"
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
    else
        echo "User installation complete!"
        echo ""
        echo "Configuration:"
        echo "  Config: $CONFIG_DIR"
        echo "  Data:   $DATA_DIR"
        echo ""
        echo "To start the server manually:"
        echo "  xw serve"
        echo ""
        echo "Test the CLI:"
        echo "  xw version"
        echo "  xw ls -a"
        echo ""
        echo "Note: You may need to reload your shell or run:"
        echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    fi
    echo ""
}

main "$@"
