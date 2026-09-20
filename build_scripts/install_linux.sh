#!/bin/bash
# ==============================================================================
# Gemini Flat Panel Proxy - One-Line Installer
# ==============================================================================
# Usage:
#   curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo bash
# OR:
#   wget -qO- https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo bash
#
# To uninstall:
#   curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo GFP_UNINSTALL=1 bash
#
# No libusb runtime check or udev rule needed - this project talks to the Gemini panel
# through the kernel's normal tty driver (go.bug.st/serial), not a direct-USB bypass, so
# the only OS-level requirement is serial port access via the 'dialout' group.
# ==============================================================================
set -e

REPO="DIYAstro/GeminiFlatPanel-Alpaca"
BINARY_NAME="geminiflatpanel"
INSTALL_DIR="/usr/local/bin"
SERVICE_DIR="/etc/systemd/system"
SERVICE_NAME="geminiflatpanel"

# --- Check for root privileges ---
if [ "$EUID" -ne 0 ]; then
    echo "Error: This script requires root privileges. Please run with sudo."
    exit 1
fi

# --- Uninstall ---
# GFP_UNINSTALL=1 removes everything this script itself installs, then exits - same
# env-var override pattern as GFP_RELEASE_TAG below, for consistency (one invocation
# style to remember, not a second one like `bash -s -- --uninstall`):
#   curl -sSL .../install_linux.sh | sudo GFP_UNINSTALL=1 bash
# Doesn't need architecture detection or a download - skip straight to it before any of
# that, and before the "Installer" banner below (this has its own banner instead).
if [ -n "${GFP_UNINSTALL:-}" ]; then
    echo "=== Gemini Flat Panel Proxy - Linux Uninstaller ==="
    echo ""

    echo "[1/2] Stopping and disabling service..."
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$SERVICE_DIR/$SERVICE_NAME.service"
    systemctl daemon-reload

    echo "[2/2] Removing binary..."
    rm -f "$INSTALL_DIR/$BINARY_NAME"

    echo ""
    echo "=== Uninstall Complete ==="
    echo ""
    echo "Left untouched on purpose (remove these yourself if you want a completely clean slate):"
    echo "  - dialout group membership - other serial devices on this system may rely on it"
    echo "  - saved config/logs at ~/.config/GeminiFlatPanelProxy/ - kept in case you reinstall later"
    exit 0
fi

echo "=== Gemini Flat Panel Proxy - Linux Installer ==="
echo ""

# Get the actual user (not root when running with sudo)
ACTUAL_USER="${SUDO_USER:-$USER}"

# --- Detect Architecture ---
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH_TAG="amd64"
        ;;
    aarch64)
        ARCH_TAG="arm64"
        ;;
    *)
        echo "Error: Unsupported architecture '$ARCH'."
        echo "Supported: x86_64 (amd64), aarch64 (Raspberry Pi arm64)"
        exit 1
        ;;
esac
echo "Detected architecture: $ARCH ($ARCH_TAG)"

# --- Determine Download URL ---
# GFP_RELEASE_TAG overrides which release to install from - for pointing a specific
# tester at a beta/pre-release build (e.g. `sudo GFP_RELEASE_TAG=v0.9.9-beta.1 bash`).
# GitHub itself never treats a pre-release as "latest", so without this override there's
# no way to reach one at all. Unset (the normal case) behaves exactly as before: resolve
# whatever's currently "latest".
if [ -n "${GFP_RELEASE_TAG:-}" ]; then
    LATEST_TAG="$GFP_RELEASE_TAG"
    echo "Using explicitly requested release: $LATEST_TAG"
else
    echo "Fetching latest release version from GitHub..."
    LATEST_TAG=$(curl -sSf "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' \
        | sed -E 's/.*"tag_name":\s*"([^"]+)".*/\1/')

    if [ -z "$LATEST_TAG" ]; then
        echo "Error: Could not determine the latest release version."
        echo "Please check your internet connection and try again."
        exit 1
    fi
    echo "Latest release: $LATEST_TAG"
fi

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_TAG/${BINARY_NAME}-linux-${ARCH_TAG}"
echo "Download URL: $DOWNLOAD_URL"

# --- Download Binary ---
echo ""
echo "[1/5] Downloading binary ($ARCH_TAG)..."
TMP_BINARY=$(mktemp)
curl -sSfL "$DOWNLOAD_URL" -o "$TMP_BINARY"
chmod 755 "$TMP_BINARY"

# --- Serial Port Permissions ---
echo "[2/5] Ensuring serial port access for user '$ACTUAL_USER'..."
if ! groups "$ACTUAL_USER" | grep -q dialout; then
    usermod -aG dialout "$ACTUAL_USER"
    echo "  -> Added '$ACTUAL_USER' to 'dialout' group."
    echo "  -> NOTE: the service started below picks this up immediately (it's a fresh process)."
    echo "     Any *existing* login shell for '$ACTUAL_USER' still needs a fresh login to see it."
else
    echo "  -> User '$ACTUAL_USER' is already in the 'dialout' group. OK."
fi

# --- Stop existing service ---
echo "[3/5] Stopping existing service (if running)..."
systemctl stop "$SERVICE_NAME" 2>/dev/null || true

# --- Install Binary ---
echo "[4/5] Installing binary to $INSTALL_DIR/$BINARY_NAME..."
mv "$TMP_BINARY" "$INSTALL_DIR/$BINARY_NAME"
chmod 755 "$INSTALL_DIR/$BINARY_NAME"

# --- Install systemd Service (inline) ---
echo "[5/5] Installing systemd service..."
cat > "$SERVICE_DIR/$SERVICE_NAME.service" <<EOF
[Unit]
Description=Gemini Flat Panel Proxy
Documentation=https://github.com/$REPO
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/$BINARY_NAME
Restart=on-failure
RestartSec=5
User=$ACTUAL_USER

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

# --- Done ---
echo ""
echo "=== Installation Complete ($LATEST_TAG, $ARCH_TAG) ==="
echo ""
echo "Useful commands:"
echo "  Status:  sudo systemctl status $SERVICE_NAME"
echo "  Logs:    sudo journalctl -u $SERVICE_NAME -f"
echo "  Stop:    sudo systemctl stop $SERVICE_NAME"
echo "  Restart: sudo systemctl restart $SERVICE_NAME"
echo ""
# Show the machine's primary IP address for convenience
IP=$(hostname -I | awk '{print $1}')
echo "Web interface: http://${IP:-<your-ip>}:32300"
