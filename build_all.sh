#!/bin/bash
# Kiskeya Softphone Cross-Platform Build Helper Script
# This script builds the application locally for target platforms.

set -e

echo "=== Kiskeya Softphone Build Script ==="
echo ""

# 1. Local macOS Build
echo "--> Building for macOS (Darwin Universal/ARM64/AMD64)..."
wails build -platform darwin/universal
echo "✓ macOS build successful! App located at: build/bin/kiskeya.app"
echo ""

# 2. Windows Cross-Compilation Check
echo "--> Checking toolchain for Windows build (amd64)..."
if command -v x86_64-w64-mingw32-gcc &> /dev/null; then
    echo "✓ Found MinGW cross-compiler (x86_64-w64-mingw32-gcc)."
    echo "Building for Windows..."
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 wails build -platform windows/amd64
    echo "✓ Windows build successful! Binary located at: build/bin/kiskeya.exe"
else
    echo "⚠ MinGW cross-compiler not found. Skipping local Windows compilation."
    echo "  To build locally, install it via Homebrew: brew install mingw-w64"
fi
echo ""

# 3. Linux Cross-Compilation Check
echo "--> Checking toolchain for Linux build (amd64)..."
if command -v x86_64-unknown-linux-gnu-gcc &> /dev/null; then
    echo "✓ Found Linux cross-compiler (x86_64-unknown-linux-gnu-gcc)."
    echo "Building for Linux..."
    CGO_ENABLED=1 CC=x86_64-unknown-linux-gnu-gcc GOOS=linux GOARCH=amd64 wails build -platform linux/amd64
    echo "✓ Linux build successful! Binary located at: build/bin/kiskeya"
else
    echo "⚠ Linux cross-compiler and ALSA development headers not found. Skipping local Linux compilation."
    echo "  To build for all platforms automatically without setting up complex cross-compilers,"
    echo "  push your code to GitHub to trigger the Actions workflow in .github/workflows/build.yml."
fi

echo ""
echo "=== Build Process Finished ==="
