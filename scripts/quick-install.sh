#!/bin/bash
#
# xw Quick Install Script
# 
# This script automatically detects system architecture and installs xw.
#
# Usage:
#   curl -o- https://xw.tsingmao.com/install.sh | bash
#
# Or:
#   curl -fsSL https://xw.tsingmao.com/install.sh | bash
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Version to install
VERSION="1.0.0"
BASE_URL="https://xw.tsingmao.com"

# Print functions
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Detect system architecture
detect_arch() {
    local arch=$(uname -m)
    
    case "$arch" in
        x86_64|amd64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        *)
            print_error "Unsupported architecture: $arch"
            print_error "Supported architectures: x86_64/amd64, aarch64/arm64"
            exit 1
            ;;
    esac
}

# Detect operating system
detect_os() {
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        echo "linux"
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        echo "darwin"
    else
        print_error "Unsupported operating system: $OSTYPE"
        print_error "Supported OS: Linux, macOS"
        exit 1
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check prerequisites
check_prerequisites() {
    print_info "Checking prerequisites..."
    
    # Check for required commands
    local missing_cmds=()
    
    for cmd in curl tar; do
        if ! command_exists "$cmd"; then
            missing_cmds+=("$cmd")
        fi
    done
    
    if [ ${#missing_cmds[@]} -gt 0 ]; then
        print_error "Missing required commands: ${missing_cmds[*]}"
        print_error "Please install them and try again."
        exit 1
    fi
    
    print_success "All prerequisites satisfied"
}

# Download file with progress
download_file() {
    local url=$1
    local output=$2
    
    print_info "Downloading from: $url"
    
    if command_exists curl; then
        curl -fSL --progress-bar "$url" -o "$output"
    else
        print_error "curl is not available"
        exit 1
    fi
}

# Main installation function
install_xw() {
    print_info "Starting xw installation..."
    echo ""
    
    # Detect system information
    local arch=$(detect_arch)
    local os=$(detect_os)
    
    print_info "Detected architecture: $arch"
    print_info "Detected OS: $os"
    echo ""
    
    # Check prerequisites
    check_prerequisites
    echo ""
    
    # Construct download URL
    local package_name="xw-${VERSION}-${arch}.tar.gz"
    local download_url="${BASE_URL}/${package_name}"
    
    # Create temporary directory
    local tmp_dir=$(mktemp -d -t xw-install.XXXXXXXXXX)
    trap "rm -rf '$tmp_dir'" EXIT
    
    print_info "Using temporary directory: $tmp_dir"
    echo ""
    
    # Download package
    local download_file="${tmp_dir}/${package_name}"
    download_file "$download_url" "$download_file"
    print_success "Downloaded successfully"
    echo ""
    
    # Extract package
    print_info "Extracting package..."
    tar -xzf "$download_file" -C "$tmp_dir"
    print_success "Extracted successfully"
    echo ""
    
    # Find extracted directory
    local extract_dir="${tmp_dir}/xw-${VERSION}-${arch}"
    if [ ! -d "$extract_dir" ]; then
        # Try alternative directory name
        extract_dir=$(find "$tmp_dir" -maxdepth 1 -type d -name "xw-*" | head -n 1)
    fi
    
    if [ ! -d "$extract_dir" ]; then
        print_error "Failed to find extracted directory"
        exit 1
    fi
    
    print_info "Extracted to: $extract_dir"
    
    # Check if install script exists (in scripts/ subdirectory)
    local install_script="${extract_dir}/scripts/install.sh"
    if [ ! -f "$install_script" ]; then
        print_error "install.sh not found in package at: $install_script"
        exit 1
    fi
    
    # Make install script executable
    chmod +x "$install_script"
    
    # Always use user-mode installation by default
    print_info "Using user-mode installation (recommended)"
    print_info "Installation path: ~/.local/bin"
    if [ "$EUID" -eq 0 ]; then
        print_warning "Running as root, but installing to user directory"
        print_info "For system-wide installation, run install.sh directly without --user flag"
    fi
    echo ""
    
    # Run installation with --user flag
    print_info "Running installation..."
    cd "$extract_dir"
    ./scripts/install.sh --user
    
    echo ""
    print_success "xw ${VERSION} installed successfully!"
    echo ""
    
    # Verify installation
    if command_exists xw; then
        print_info "Installation verified. Version:"
        xw version || xw --version || true
    else
        print_warning "xw command not found in PATH"
        print_info "You may need to:"
        print_info "  1. Start a new terminal session"
        print_info "  2. Or run: source ~/.bashrc (or ~/.zshrc)"
        print_info "  3. Or add ~/.local/bin to your PATH"
    fi
    echo ""
    
    print_info "Get started with:"
    echo "  xw version          # Show version"
    echo "  xw device list      # List AI accelerators"
    echo "  xw ls               # List available models"
    echo "  xw pull <model>     # Download a model"
    echo "  xw run <model>      # Run interactive chat"
    echo ""
    print_info "Documentation: https://xw.tsingmao.com"
    print_info "GitHub: https://github.com/tsingmaoai/xw-cli"
}

# Run installation
main() {
    cat << "EOF"
    
 __  ____      __
 \ \/ /\ \ /\ / /
  \  /  \ V  V / 
  /  \   \_/\_/  
 /_/\_\           

xw - AI Inference Platform
Version: 1.0.0

EOF

    install_xw
}

# Check if script is being piped or executed
if [ -t 0 ]; then
    # Interactive mode
    main "$@"
else
    # Piped mode (curl | bash)
    main "$@"
fi

