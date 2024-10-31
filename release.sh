#!/bin/bash

set -e

# Check input arguments
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <plugin-name> <version>"
  exit 1
fi

PLUGIN_NAME=$1
VERSION=$2
SRC_DIR="./src/$PLUGIN_NAME"
BINARY_NAME="kubectl-$PLUGIN_NAME"
BINARIES_DIR="binaries/$PLUGIN_NAME"
ARTIFACTS_DIR="artifacts"
PLUGINS_DIR="plugins"
REPO_URL="https://github.com/vrabbi/kubectl-plugins" # Update with your GitHub repo URL

# Supported architectures and operating systems
ARCHS=("amd64" "arm64")
OS=("linux" "darwin" "windows")

# Check if the module is initialized
if [ ! -f "$SRC_DIR/go.mod" ]; then
  echo "Initializing Go module in $SRC_DIR"
  (cd "$SRC_DIR" && go mod init github.com/YOUR_GITHUB_USER/YOUR_REPO/src/$PLUGIN_NAME)
fi

# Build binaries
for os in "${OS[@]}"; do
  for arch in "${ARCHS[@]}"; do
    OUTPUT_DIR="$BINARIES_DIR/$os/$arch"
    mkdir -p "$OUTPUT_DIR"

    # Change to the plugin directory to ensure Go uses the correct go.mod file
    (cd "$SRC_DIR" && GOOS=$os GOARCH=$arch go build -o "../../$OUTPUT_DIR/$BINARY_NAME" .)

    # Create tar.gz archive
    ARCHIVE_NAME="$ARTIFACTS_DIR/${BINARY_NAME}-${VERSION}-${os}-${arch}.tar.gz"
    mkdir -p "$ARTIFACTS_DIR"
    tar -czf "$ARCHIVE_NAME" -C "$OUTPUT_DIR" "$BINARY_NAME"
  done
done

# Create Krew plugin manifest
MANIFEST_FILE="$PLUGINS_DIR/$PLUGIN_NAME.yaml"
mkdir -p "$PLUGINS_DIR"

cat <<EOF > "$MANIFEST_FILE"
apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: $PLUGIN_NAME
spec:
  version: "$VERSION"
  homepage: "$REPO_URL"
  platforms:
EOF

for os in "${OS[@]}"; do
  for arch in "${ARCHS[@]}"; do
    cat <<EOF >> "$MANIFEST_FILE"
  - selector:
      matchLabels:
        os: "$os"
        arch: "$arch"
    uri: "$REPO_URL/releases/download/$PLUGIN_NAME-$VERSION/${BINARY_NAME}-${VERSION}-${os}-${arch}.tar.gz"
    bin: "$BINARY_NAME"
    files:
      - from: "$BINARY_NAME"
        to: "$BINARY_NAME"
EOF
  done
done

# Create GitHub tag and release with GH CLI
RELEASE_TAG="${PLUGIN_NAME}-${VERSION}"
gh release create "$RELEASE_TAG" "$ARTIFACTS_DIR"/*.tar.gz --title "$RELEASE_TAG" --notes "Release of $PLUGIN_NAME version $VERSION"
