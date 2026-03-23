#!/bin/bash
# Creates the host user inside the container so the devcontainer runs
# as the same identity as the host. Called via onCreateCommand.
#
# When the host user is not "vscode" (e.g., CI runner user "ci-runner"),
# this script creates the user with a matching UID/GID derived from
# workspace file ownership, and copies Claude Code config from the
# pre-built vscode user.
#
# Runs as the remoteUser (vscode), so privileged commands use sudo.
set -e

TARGET_USER="${1:-vscode}"

# If target user already exists, nothing to do
if id "$TARGET_USER" &>/dev/null; then
    echo "User $TARGET_USER already exists, skipping creation."
    # Ensure the user has zsh as default shell (base image sets it for vscode)
    if [ "$(getent passwd "$TARGET_USER" | cut -d: -f7)" != "/usr/bin/zsh" ]; then
        sudo chsh -s /usr/bin/zsh "$TARGET_USER" 2>/dev/null || true
    fi
    exit 0
fi

# Detect host UID/GID from workspace file ownership
# Detect workspace directory dynamically
WORKSPACE=$(find /workspaces -maxdepth 1 -mindepth 1 -type d 2>/dev/null | head -1)
if [ -d "$WORKSPACE" ]; then
    TARGET_UID=$(stat -c '%u' "$WORKSPACE")
    TARGET_GID=$(stat -c '%g' "$WORKSPACE")
else
    # Fallback to 1000:1000 (standard devcontainer UID)
    TARGET_UID=1000
    TARGET_GID=1000
fi

# Refuse to create root user — Claude Code requires non-root
if [ "$TARGET_UID" = "0" ]; then
    echo "ERROR: Host UID is 0 (root). Claude Code requires a non-root user."
    echo "Set a non-root user in your CI runner or devcontainer configuration."
    exit 1
fi

echo "Creating user $TARGET_USER (UID=$TARGET_UID, GID=$TARGET_GID)..."

# If the target UID is already owned by another user (e.g., vscode has UID 1000),
# skip creation — remoteUser + updateRemoteUserUID handles UID mapping.
EXISTING_USER=$(getent passwd "$TARGET_UID" | cut -d: -f1)
if [ -n "$EXISTING_USER" ] && [ "$EXISTING_USER" != "$TARGET_USER" ]; then
    echo "UID $TARGET_UID already belongs to '$EXISTING_USER'. Skipping user creation."
    echo "The devcontainer will run as '$EXISTING_USER' with updateRemoteUserUID handling UID mapping."
    exit 0
fi

# Create group if GID doesn't already exist
if ! getent group "$TARGET_GID" &>/dev/null; then
    sudo groupadd --gid "$TARGET_GID" "$TARGET_USER"
fi

# Get the group name for the target GID
TARGET_GROUP=$(getent group "$TARGET_GID" | cut -d: -f1)

# Create user with matching UID/GID
sudo useradd -m -s /usr/bin/zsh -u "$TARGET_UID" -g "$TARGET_GID" "$TARGET_USER"

# Grant passwordless sudo (standard for devcontainer users)
echo "$TARGET_USER ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/"$TARGET_USER" > /dev/null
sudo chmod 0440 /etc/sudoers.d/"$TARGET_USER"

# Copy Claude Code config and plugins from pre-built vscode user
if [ -d /home/vscode/.claude ]; then
    sudo cp -r /home/vscode/.claude /home/"$TARGET_USER"/.claude 2>/dev/null || true
fi
if [ -d /home/vscode/.cache ]; then
    sudo cp -r /home/vscode/.cache /home/"$TARGET_USER"/.cache 2>/dev/null || true
fi

# Fix ownership of copied files
sudo chown -R "$TARGET_USER":"$TARGET_GROUP" /home/"$TARGET_USER"

# Set up gh config symlink if the mount exists
if [ -d /tmp/gh-config ]; then
    sudo mkdir -p /home/"$TARGET_USER"/.config
    sudo ln -sf /tmp/gh-config /home/"$TARGET_USER"/.config/gh
    sudo chown -h "$TARGET_USER":"$TARGET_GROUP" /home/"$TARGET_USER"/.config/gh
fi

echo "User $TARGET_USER created successfully."
