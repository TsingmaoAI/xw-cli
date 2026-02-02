#!/bin/bash
#
# Package script for xw - Creates distributable packages
#
# This script creates tar.gz packages for distribution.
# It includes binaries, systemd service, scripts, and documentation.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Version and build info
VERSION="${VERSION:-1.0.0}"
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u '+%Y%m%d')

# Directories
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$PROJECT_DIR/bin"
RELEASE_DIR="$BIN_DIR/release"
PACKAGE_DIR="$BIN_DIR/packages"

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

# Check if release binaries exist
check_binaries() {
    print_info "Checking release binaries..."
    
    if [ ! -d "$RELEASE_DIR" ] || [ -z "$(ls -A $RELEASE_DIR 2>/dev/null)" ]; then
        print_error "Release binaries not found. Please build first:"
        echo "  make release"
        exit 1
    fi
    
    print_info "✓ Release binaries found"
}

# Create package directory structure
create_package_structure() {
    local arch=$1
    local pkg_name="xw-${VERSION}-${arch}"
    local pkg_dir="${PACKAGE_DIR}/${pkg_name}"
    
    print_info "Creating package structure for ${arch}..."
    
    # Create directories
    mkdir -p "${pkg_dir}/bin"
    mkdir -p "${pkg_dir}/systemd"
    mkdir -p "${pkg_dir}/scripts"
    mkdir -p "${pkg_dir}/docs"
    mkdir -p "${pkg_dir}/configs"
    
    # Copy binary
    cp "${RELEASE_DIR}/xw-linux-${arch}" "${pkg_dir}/bin/xw"
    chmod +x "${pkg_dir}/bin/xw"
    
    # Copy systemd service
    cp "${PROJECT_DIR}/systemd/xw-server.service" "${pkg_dir}/systemd/"
    
    # Copy configuration files
    cp "${PROJECT_DIR}/configs/devices.yaml" "${pkg_dir}/configs/"
    cp "${PROJECT_DIR}/configs/models.yaml" "${pkg_dir}/configs/"
    cp "${PROJECT_DIR}/configs/runtime_params.yaml" "${pkg_dir}/configs/"
    
    # Copy scripts
    cp "${PROJECT_DIR}/scripts/install.sh" "${pkg_dir}/scripts/"
    cp "${PROJECT_DIR}/scripts/uninstall.sh" "${pkg_dir}/scripts/"
    chmod +x "${pkg_dir}/scripts/"*.sh
    
    # Copy documentation
    cp "${PROJECT_DIR}/README.md" "${pkg_dir}/docs/"
    [ -f "${PROJECT_DIR}/LICENSE" ] && cp "${PROJECT_DIR}/LICENSE" "${pkg_dir}/docs/"
    
    # Create INSTALL file
    cat > "${pkg_dir}/INSTALL" <<'EOF'
XW Installation Guide
=====================

Quick Install:
--------------
1. Extract the package:
   tar -xzf xw-*.tar.gz
   cd xw-*

2. Choose installation mode:

   # User installation (no sudo required)
   bash scripts/install.sh
   
   # System installation (requires sudo, installs as service)
   sudo bash scripts/install.sh --system

Installation Modes:
-------------------
User Installation (default):
  - Binary: ~/.local/bin/xw
  - Config: ~/.xw/*.yaml
  - Data:   ~/.xw/data/
  - No systemd service
  - Run manually: xw serve

System Installation (--system):
  - Binary: /usr/local/bin/xw
  - Config: /etc/xw/*.yaml
  - Data:   /var/lib/xw/
  - Systemd service installed
  - Auto-start: systemctl enable xw-server

Manual Install:
---------------
1. Copy binary:
   sudo install -m 755 bin/xw /usr/local/bin/xw

2. Create user and directories:
   sudo useradd --system --no-create-home --shell /bin/false xw
   sudo mkdir -p /etc/xw /opt/xw /var/log/xw
   sudo chown xw:xw /opt/xw /var/log/xw

3. Install systemd service:
   sudo cp systemd/xw-server.service /etc/systemd/system/
   sudo systemctl daemon-reload

4. Start service:
   sudo systemctl start xw-server
   sudo systemctl enable xw-server

Test Installation:
------------------
xw version
xw ls -a

Documentation:
--------------
See docs/README.md for full documentation.

EOF
    
    # Create version info file
    cat > "${pkg_dir}/VERSION" <<EOF
VERSION=${VERSION}
GIT_COMMIT=${GIT_COMMIT}
BUILD_DATE=${BUILD_DATE}
ARCH=${arch}
EOF
    
    print_info "✓ Package structure created: ${pkg_name}"
    echo "$pkg_dir"
}

# Create tar.gz package
create_tarball() {
    local pkg_dir=$1
    local pkg_name=$(basename "$pkg_dir")
    local tarball="${PACKAGE_DIR}/${pkg_name}.tar.gz"
    
    print_info "Creating tarball..."
    
    cd "$PACKAGE_DIR"
    tar -czf "${pkg_name}.tar.gz" "$pkg_name"
    cd - > /dev/null
    
    # Calculate checksum
    cd "$PACKAGE_DIR"
    sha256sum "${pkg_name}.tar.gz" > "${pkg_name}.tar.gz.sha256"
    cd - > /dev/null
    
    print_info "✓ Tarball created: ${tarball}"
    print_info "✓ Checksum: ${tarball}.sha256"
    
    # Clean up extracted directory
    rm -rf "$pkg_dir"
}

# Create RPM package (optional, requires rpmbuild)
create_rpm() {
    local arch=$1
    
    if ! command -v rpmbuild &> /dev/null; then
        print_warn "rpmbuild not found, skipping RPM creation"
        return
    fi
    
    print_info "Creating RPM package for ${arch}..."
    
    # Create RPM spec file
    local rpm_arch=$arch
    [ "$arch" = "amd64" ] && rpm_arch="x86_64"
    
    local spec_file="${PACKAGE_DIR}/xw.spec"
    cat > "$spec_file" <<EOF
Name:           xw
Version:        ${VERSION}
Release:        1%{?dist}
Summary:        XW AI Inference on Domestic Chips

License:        Apache-2.0
URL:            https://github.com/tsingmao/xw
Source0:        xw-${VERSION}-${arch}.tar.gz

BuildArch:      ${rpm_arch}
Requires:       systemd

%description
XW is an AI inference platform optimized for domestic chips including
Ascend NPU, Kunlun XPU, and other accelerators.

%prep
%setup -q -n xw-${VERSION}-${arch}

%install
mkdir -p %{buildroot}%{_bindir}
mkdir -p %{buildroot}%{_unitdir}
mkdir -p %{buildroot}%{_sysconfdir}/xw
mkdir -p %{buildroot}/opt/xw
mkdir -p %{buildroot}/var/log/xw

install -m 755 bin/xw %{buildroot}%{_bindir}/xw
install -m 644 systemd/xw-server.service %{buildroot}%{_unitdir}/xw-server.service

%files
%{_bindir}/xw
%{_unitdir}/xw-server.service
%dir %{_sysconfdir}/xw
%dir /opt/xw
%dir /var/log/xw

%pre
getent group xw >/dev/null || groupadd -r xw
getent passwd xw >/dev/null || useradd -r -g xw -d /opt/xw -s /sbin/nologin xw

%post
%systemd_post xw-server.service

%preun
%systemd_preun xw-server.service

%postun
%systemd_postun_with_restart xw-server.service

%changelog
* $(date '+%a %b %d %Y') XW Team <dev@tsingmao.com> - ${VERSION}-1
- Release ${VERSION}

EOF
    
    # Build RPM
    rpmbuild -bb --define "_topdir ${PACKAGE_DIR}/rpmbuild" \
             --define "_sourcedir ${PACKAGE_DIR}" \
             "$spec_file" 2>&1 | grep -v "warning: Deprecated"
    
    # Move RPM to packages directory
    find "${PACKAGE_DIR}/rpmbuild/RPMS" -name "*.rpm" -exec mv {} "$PACKAGE_DIR/" \;
    rm -rf "${PACKAGE_DIR}/rpmbuild" "$spec_file"
    
    print_info "✓ RPM package created"
}

# Create DEB package (optional, requires dpkg-deb)
create_deb() {
    local arch=$1
    
    if ! command -v dpkg-deb &> /dev/null; then
        print_warn "dpkg-deb not found, skipping DEB creation"
        return
    fi
    
    print_info "Creating DEB package for ${arch}..."
    
    local deb_arch=$arch
    [ "$arch" = "amd64" ] && deb_arch="amd64"
    [ "$arch" = "arm64" ] && deb_arch="arm64"
    [ "$arch" = "loong64" ] && deb_arch="loong64"
    
    local pkg_name="xw_${VERSION}_${deb_arch}"
    local deb_dir="${PACKAGE_DIR}/${pkg_name}"
    
    # Create DEB directory structure
    mkdir -p "${deb_dir}/DEBIAN"
    mkdir -p "${deb_dir}/usr/local/bin"
    mkdir -p "${deb_dir}/lib/systemd/system"
    mkdir -p "${deb_dir}/etc/xw"
    mkdir -p "${deb_dir}/opt/xw"
    mkdir -p "${deb_dir}/var/log/xw"
    
    # Copy files
    cp "${RELEASE_DIR}/xw-linux-${arch}" "${deb_dir}/usr/local/bin/xw"
    chmod +x "${deb_dir}/usr/local/bin/xw"
    cp "${PROJECT_DIR}/systemd/xw-server.service" "${deb_dir}/lib/systemd/system/"
    
    # Create control file
    cat > "${deb_dir}/DEBIAN/control" <<EOF
Package: xw
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${deb_arch}
Maintainer: XW Team <dev@tsingmao.com>
Description: XW AI Inference on Domestic Chips
 XW is an AI inference platform optimized for domestic chips including
 Ascend NPU, Kunlun XPU, and other accelerators.
Depends: systemd
EOF
    
    # Create postinst script
    cat > "${deb_dir}/DEBIAN/postinst" <<'EOF'
#!/bin/bash
set -e

# Create user
if ! id xw > /dev/null 2>&1; then
    useradd --system --no-create-home --shell /bin/false xw
fi

# Set permissions
chown xw:xw /opt/xw
chown xw:xw /var/log/xw

# Reload systemd
systemctl daemon-reload

exit 0
EOF
    chmod +x "${deb_dir}/DEBIAN/postinst"
    
    # Build DEB
    dpkg-deb --build "$deb_dir"
    mv "${deb_dir}.deb" "$PACKAGE_DIR/"
    rm -rf "$deb_dir"
    
    print_info "✓ DEB package created"
}

# Main packaging process
main() {
    echo "==============================================="
    echo "  XW Package Builder"
    echo "  Version: ${VERSION}"
    echo "==============================================="
    echo ""
    
    check_binaries
    
    # Clean and create package directory
    rm -rf "$PACKAGE_DIR"
    mkdir -p "$PACKAGE_DIR"
    
    # Package each architecture
    for arch in amd64 arm64; do
        if [ -f "${RELEASE_DIR}/xw-linux-${arch}" ]; then
            print_info "Packaging for ${arch}..."
            
            # Create tar.gz package
            pkg_dir=$(create_package_structure "$arch")
            create_tarball "$pkg_dir"
            
            # Create RPM and DEB (optional)
            # create_rpm "$arch"
            # create_deb "$arch"
            
            echo ""
        else
            print_warn "Binary not found for ${arch}, skipping"
        fi
    done
    
    # Show results
    echo "==============================================="
    echo -e "${GREEN}Packaging completed successfully!${NC}"
    echo "==============================================="
    echo ""
    echo "Packages created in: ${PACKAGE_DIR}"
    ls -lh "$PACKAGE_DIR"
    echo ""
    echo "To distribute:"
    echo "  1. Upload packages to release server"
    echo "  2. Share download links with users"
    echo "  3. Verify checksums: sha256sum -c *.sha256"
    echo ""
}

main "$@"

