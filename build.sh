#!/bin/bash
# Build wk4 for Windows (GUI, no console window) and Linux
set -e

VERSION=$(grep 'var version' main.go | sed 's/.*"\(.*\)".*/\1/')
echo "Building wk4 ${VERSION}..."

# Generate Windows icon/manifest resources (.syso files)
if command -v go-winres &>/dev/null; then
    echo "  Generating Windows resources..."
    go-winres make
else
    echo "  Skipping Windows resources (go-winres not installed)"
    echo "  Install with: go install github.com/tc-hib/go-winres@latest"
fi

# Windows (64-bit, GUI subsystem — no console window)
echo "  wk4.exe (windows/amd64)"
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o wk4.exe .

# Linux
echo "  wk4 (linux/amd64)"
GOOS=linux GOARCH=amd64 go build -o wk4 .

echo "Done."
