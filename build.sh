#!/bin/bash
mkdir -p output/linux/amd64
mkdir -p output/linux/arm64
mkdir -p output/darwin/amd64
mkdir -p output/darwin/arm64
mkdir -p output/windows/amd64
mkdir -p output/windows/arm64
# Linux
echo "Building for Linux AMD64"
GOOS=linux GOARCH=amd64 go build -o output/linux/amd64/kubectl-capacity
echo "Building for Linux ARM64"
GOOS=linux GOARCH=arm64 go build -o output/linux/arm64/kubectl-capacity

# macOS
echo "Building for MACOS AMD64"
GOOS=darwin GOARCH=amd64 go build -o output/darwin/amd64/kubectl-capacity
echo "Building for MACOS ARM64"
GOOS=darwin GOARCH=arm64 go build -o output/darwin/arm64/kubectl-capacity

# Windows
echo "Building for Windows AMD64"
GOOS=windows GOARCH=amd64 go build -o output/windows/amd64/kubectl-capacity.exe
echo "Building for Windows ARM64"
GOOS=windows GOARCH=arm64 go build -o output/windows/arm64/kubectl-capacity.exe

echo "All binaries have been built!"
echo "Building tar.gz archives"
cd output/linux/amd64
tar 
