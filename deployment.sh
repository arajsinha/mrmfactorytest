#!/bin/bash

# Exit immediately if a command exits with a non-zero status.
set -e

# --- Configuration ---
IMAGE_NAME="mrm2cell"
DEFAULT_VERSION="v0.03"
REMOTE_REGISTRY="ghcr.io/mahesh0431" # Your GitHub Container Registry path
DOCKERFILE="Dockerfile"              # Assumes Dockerfile is in the current directory

# --- Check Prerequisites ---
if ! command -v docker &>/dev/null || ! docker buildx version &>/dev/null; then
    echo "Error: docker or docker buildx is not available."
    echo "Please install Docker Desktop (which includes buildx) or ensure docker buildx is configured."
    exit 1
fi

# --- User Input ---
echo "Docker Build and Deployment Helper"
echo "----------------------------------"
echo "1. Build for Local ARM64 (for testing on ARM Mac)"
echo "2. Build for AMD64 and Push to Registry (${REMOTE_REGISTRY})"
echo "3. Build for ARM64 and Push to Registry (${REMOTE_REGISTRY})"
echo "----------------------------------"

read -p "Choose an option (1, 2, or 3): " choice

read -p "Enter image version [default: ${DEFAULT_VERSION}]: " input_version
VERSION=${input_version:-$DEFAULT_VERSION} # Use default if input is empty

echo "Using version: ${VERSION}"

# --- Determine Build Parameters ---
PLATFORM=""
TAG=""
ACTION="" # 'load' or 'push'

case $choice in
  1) # Build local ARM64
    PLATFORM="linux/arm64"
    TAG="${IMAGE_NAME}:${VERSION}"
    ACTION="load"
    ;;
  2) # Build and push AMD64
    PLATFORM="linux/amd64" # Corrected platform
    TAG="${REMOTE_REGISTRY}/${IMAGE_NAME}:${VERSION}"
    ACTION="push"
    ;;
  3) # Build and push ARM64
    PLATFORM="linux/arm64"
    TAG="${REMOTE_REGISTRY}/${IMAGE_NAME}:${VERSION}"
    ACTION="push"
    ;;
  *)
    echo "Error: Invalid choice. Please enter 1, 2, or 3."
    exit 1
    ;;
esac

# --- Execute Build ---
echo ""
echo ">>> Building image for platform ${PLATFORM} <<<"
echo "Tag: ${TAG}"

# Common buildx command parts
BUILD_CMD="docker buildx build --platform ${PLATFORM} -t "${TAG}" -f "${DOCKERFILE}" ."

if [ "$ACTION" == "load" ]; then
    echo "Action: Build and load locally"
    ${BUILD_CMD} --load

    echo ""
    echo "-----------------------------------------------------------------"
    echo "✅ Success! Image built and loaded locally."
    echo "   Image: ${TAG}"
    echo "   You can run it using: docker run -it --rm ${TAG}"
    echo "-----------------------------------------------------------------"

elif [ "$ACTION" == "push" ]; then
    echo "Action: Build and push to ${REMOTE_REGISTRY}"
    echo "Make sure you are logged in: docker login ghcr.io -u YOUR_GITHUB_USERNAME -p YOUR_PAT" # Keep the reminder
    ${BUILD_CMD} --push

    echo ""
    echo "-----------------------------------------------------------------"
    echo "✅ Success! Image built and pushed to registry."
    echo "   Pushed Image: ${TAG}"
    echo "   You can now use this image in environments requiring ${PLATFORM} architecture."
    echo "-----------------------------------------------------------------"
fi

exit 0
