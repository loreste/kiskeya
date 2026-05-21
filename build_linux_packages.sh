#!/bin/bash
# Kiskeya Softphone Linux Installer & Package Generator (Ubuntu/Fedora)
# Run this script on your Linux machine to compile and package the app natively.

set -e

echo "=========================================================="
echo "      Kiskeya Linux Package Generator (Ubuntu / Fedora)   "
echo "=========================================================="
echo ""

# 1. Detect System Type
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_ID=$ID
    OS_LIKE=$ID_LIKE
else
    echo "❌ Error: Cannot determine operating system type."
    exit 1
fi

echo "--> Detected OS: $NAME ($OS_ID)"

# WebKit2GTK 4.0 was dropped on newer distros (Ubuntu 24.04+, recent Fedora) in
# favour of 4.1. Detect which is available and set the matching Wails build tag.
WAILS_TAGS=""

# 2. Install dependencies based on Package Manager
if [[ "$OS_ID" == "ubuntu" || "$OS_ID" == "debian" || "$OS_LIKE" == *"debian"* ]]; then
    echo "--> Installing dependencies for Ubuntu/Debian..."
    sudo apt-get update
    if apt-cache show libwebkit2gtk-4.1-dev >/dev/null 2>&1; then
        WEBKIT_PKG="libwebkit2gtk-4.1-dev"; WAILS_TAGS="-tags webkit2_41"
    else
        WEBKIT_PKG="libwebkit2gtk-4.0-dev"
    fi
    sudo apt-get install -y \
        golang-go \
        nodejs \
        npm \
        libgtk-3-dev \
        "$WEBKIT_PKG" \
        libasound2-dev \
        pkg-config \
        build-essential \
        dpkg-dev
elif [[ "$OS_ID" == "fedora" || "$OS_ID" == "rhel" || "$OS_LIKE" == *"rhel"* || "$OS_LIKE" == *"fedora"* ]]; then
    echo "--> Installing dependencies for Fedora/RedHat..."
    if dnf list webkit2gtk4.1-devel >/dev/null 2>&1; then
        WEBKIT_PKG="webkit2gtk4.1-devel"; WAILS_TAGS="-tags webkit2_41"
    else
        WEBKIT_PKG="webkit2gtk3-devel"
    fi
    sudo dnf install -y \
        golang \
        nodejs \
        npm \
        gtk3-devel \
        "$WEBKIT_PKG" \
        alsa-lib-devel \
        gcc \
        gcc-c++ \
        make \
        pkg-config \
        rpm-build
else
    echo "⚠ Unsupported OS for automatic dependency installation."
    echo "Please ensure Go, Node.js, GTK3, WebKit2Gtk, ALSA dev libs, and build tools are installed manually."
fi

# 3. Verify Go and Node.js are available
export PATH=$PATH:/usr/local/go/bin:$(go env GOPATH)/bin
if ! command -v go &> /dev/null; then
    echo "❌ Error: Go compiler not found in PATH."
    exit 1
fi

# 4. Install/Update Wails CLI
echo "--> Installing Wails CLI tool..."
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0

# 5. Build application binary
echo "--> Compiling Kiskeya production binary (tags: ${WAILS_TAGS:-none})..."
wails build -platform linux/amd64 $WAILS_TAGS

# 6. Build Packages
mkdir -p build/bin

if [[ "$OS_ID" == "ubuntu" || "$OS_ID" == "debian" || "$OS_LIKE" == *"debian"* ]]; then
    echo "--> Generating Debian package (.deb)..."
    DEB_DIR="build/linux/deb"
    rm -rf "$DEB_DIR"
    
    # Create directory tree
    mkdir -p "$DEB_DIR/DEBIAN"
    mkdir -p "$DEB_DIR/usr/bin"
    mkdir -p "$DEB_DIR/usr/share/applications"
    mkdir -p "$DEB_DIR/usr/share/icons/hicolor/512x512/apps"
    
    # Copy binary & assets
    cp build/bin/kiskeya "$DEB_DIR/usr/bin/"
    cp build/appicon.png "$DEB_DIR/usr/share/icons/hicolor/512x512/apps/kiskeya.png"
    
    # Write control metadata
    cat <<EOF > "$DEB_DIR/DEBIAN/control"
Package: kiskeya
Version: 1.0.0
Section: utils
Priority: optional
Architecture: amd64
Depends: libgtk-3-0, libwebkit2gtk-4.0-37, libasound2
Maintainer: Kiskeya Team <info@kiskeya.com>
Description: Kiskeya Desktop SIP Softphone
 A professional, high-performance desktop VoIP SIP softphone.
 Beautiful Glassmorphism UI with real-time responsive audio logo.
EOF

    # Write Desktop launcher entry
    cat <<EOF > "$DEB_DIR/usr/share/applications/kiskeya.desktop"
[Desktop Entry]
Name=Kiskeya
Comment=Kiskeya SIP Softphone
Exec=/usr/bin/kiskeya
Icon=kiskeya
Type=Application
Terminal=false
Categories=Network;Telephony;
EOF

    chmod +x "$DEB_DIR/usr/bin/kiskeya"
    
    # Package deb
    dpkg-deb --build "$DEB_DIR" build/bin/kiskeya_1.0.0_amd64.deb
    echo "✓ Debian package created: build/bin/kiskeya_1.0.0_amd64.deb"

elif [[ "$OS_ID" == "fedora" || "$OS_ID" == "rhel" || "$OS_LIKE" == *"rhel"* || "$OS_LIKE" == *"fedora"* ]]; then
    echo "--> Generating RPM package (.rpm)..."
    RPM_ROOT="$HOME/rpmbuild"
    rm -rf "$RPM_ROOT"
    mkdir -p "$RPM_ROOT"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
    
    # Copy source files
    cp build/bin/kiskeya "$RPM_ROOT/SOURCES/"
    cp build/appicon.png "$RPM_ROOT/SOURCES/"
    
    # Create desktop file
    cat <<EOF > "$RPM_ROOT/SOURCES/kiskeya.desktop"
[Desktop Entry]
Name=Kiskeya
Comment=Kiskeya SIP Softphone
Exec=/usr/bin/kiskeya
Icon=kiskeya
Type=Application
Terminal=false
Categories=Network;Telephony;
EOF

    # Create spec file
    cat <<EOF > "$RPM_ROOT/SPECS/kiskeya.spec"
Name:           kiskeya
Version:        1.0.0
Release:        1%{?dist}
Summary:        Kiskeya Desktop SIP Softphone
License:        MIT
URL:            https://kiskeya.com

Requires:       gtk3, webkit2gtk3, alsa-lib

%description
A professional, high-performance desktop VoIP SIP softphone.
Beautiful Glassmorphism UI with real-time responsive audio logo.

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/512x512/apps
cp %{_sourcedir}/kiskeya %{buildroot}/usr/bin/kiskeya
cp %{_sourcedir}/kiskeya.desktop %{buildroot}/usr/share/applications/kiskeya.desktop
cp %{_sourcedir}/appicon.png %{buildroot}/usr/share/icons/hicolor/512x512/apps/kiskeya.png

%files
/usr/bin/kiskeya
/usr/share/applications/kiskeya.desktop
/usr/share/icons/hicolor/512x512/apps/kiskeya.png

%changelog
* Tue May 19 2026 Kiskeya Team <info@kiskeya.com> - 1.0.0-1
- Initial release package
EOF

    # Build RPM
    rpmbuild -bb "$RPM_ROOT/SPECS/kiskeya.spec"
    
    # Copy output to build dir
    cp "$RPM_ROOT"/RPMS/x86_64/kiskeya-1.0.0-1.*.rpm build/bin/
    echo "✓ RPM package created: build/bin/$(basename build/bin/kiskeya-1.0.0-1.*.rpm)"
fi

echo ""
echo "=== Linux packaging completed successfully! ==="
