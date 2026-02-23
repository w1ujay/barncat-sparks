#!/bin/bash
# Build BarnCat Sparks for Windows (GUI, no console window) and Linux
set -e

VERSION=$(grep 'var Version' about.go | sed 's/.*"\(.*\)".*/\1/')
echo "Building BarnCat Sparks ${VERSION}..."

# Generate Windows icon/manifest resources (.syso files)
if command -v go-winres &>/dev/null; then
    echo "  Generating Windows resources..."
    go-winres make
else
    echo "  Skipping Windows resources (go-winres not installed)"
    echo "  Install with: go install github.com/tc-hib/go-winres@latest"
fi

# Windows (64-bit, GUI subsystem — no console window)
echo "  barncat-sparks.exe (windows/amd64)"
mkdir -p bin

GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o bin/barncat-sparks.exe .

# Linux
echo "  barncat-sparks (linux/amd64)"
GOOS=linux GOARCH=amd64 go build -o bin/barncat-sparks .

echo "Done."
