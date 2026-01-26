#!/bin/bash
set -e

# Install AI tools (Claude Code)
# This layer fetches credentials from MMDS at runtime

# Configure NOPASSWD sudo for sudo group
echo '%sudo ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/sudo-nopasswd
chmod 440 /etc/sudoers.d/sudo-nopasswd

# Install useful CLI tools for AI coding assistants
apt-get update
apt-get install -y software-properties-common
add-apt-repository -y universe
apt-get update
apt-get install -y \
    expect \
    jq \
    tree \
    tmux \
    screen \
    ripgrep

# Install Node.js and npm
curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
apt-get install -y nodejs

# Install Claude Code native binary directly (without running 'claude install' which needs a TTY)
GCS_BUCKET="https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"

# Detect platform
arch=$(uname -m)
case "$arch" in
    x86_64|amd64) arch="x64" ;;
    arm64|aarch64) arch="arm64" ;;
esac
platform="linux-${arch}"

# Get latest version and download
version=$(curl -fsSL "$GCS_BUCKET/latest")

# Install for sandfire user using official layout:
# - Binary at ~/.local/share/claude/versions/{version}
# - Symlink at ~/.local/bin/claude -> binary (we use claude-bin for wrapper)
SANDFIRE_HOME="/home/sandfire"
CLAUDE_VERSIONS_DIR="${SANDFIRE_HOME}/.local/share/claude/versions"
CLAUDE_BIN_DIR="${SANDFIRE_HOME}/.local/bin"

mkdir -p "$CLAUDE_VERSIONS_DIR"
mkdir -p "$CLAUDE_BIN_DIR"

# Download binary to versions directory (official location)
curl -fsSL -o "${CLAUDE_VERSIONS_DIR}/${version}" "$GCS_BUCKET/$version/$platform/claude"
chmod +x "${CLAUDE_VERSIONS_DIR}/${version}"

# Create claude-bin symlink pointing to the versioned binary (for wrapper to call)
ln -sf "${CLAUDE_VERSIONS_DIR}/${version}" "${CLAUDE_BIN_DIR}/claude-bin"

# Create wrapper script at the official claude location
# This satisfies Claude's self-check while adding --dangerously-skip-permissions
cat > "${CLAUDE_BIN_DIR}/claude" << 'EOF'
#!/bin/bash
# Claude Code wrapper for sandfire VMs
# Automatically adds --dangerously-skip-permissions for isolated VM environment
SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
exec "${SCRIPT_DIR}/claude-bin" --dangerously-skip-permissions "$@"
EOF
chmod +x "${CLAUDE_BIN_DIR}/claude"

# Set ownership for sandfire user
chown -R sandfire:sandfire "${SANDFIRE_HOME}/.local"

# Create base .claude.json settings file (child layers can modify this at build time)
echo '{"hasCompletedOnboarding": true, "bypassPermissionsModeAccepted": true, "sonnet45MigrationComplete": true, "sonnet45MigrationTimestamp": 1769408052736}' > "${SANDFIRE_HOME}/.claude.json"
chown sandfire:sandfire "${SANDFIRE_HOME}/.claude.json"

# Also set some useful settings in the claude settings.json file
mkdir -p "${SANDFIRE_HOME}/.claude"
echo '{"cleanupPeriodDays": 99999, "model": "opus"}' > "${SANDFIRE_HOME}/.claude/settings.json"
chown -R sandfire:sandfire "${SANDFIRE_HOME}/.claude"

# Create script to fetch Claude credentials from MMDS
cat > /usr/local/bin/sandfire-claude-credentials.sh << 'SCRIPT'
#!/bin/bash
# Fetch Claude Code credentials from Firecracker MMDS
# This runs at boot to configure Claude Code with host credentials

MMDS_IP="169.254.169.254"
MMDS_IFACE="eth0"

# Add route to MMDS (link-local address needs explicit route)
ip route add ${MMDS_IP}/32 dev ${MMDS_IFACE} 2>/dev/null || true

# Get MMDS V2 token
TOKEN=$(curl -s -X PUT "http://${MMDS_IP}/latest/api/token" \
    -H "X-metadata-token-ttl-seconds: 300" 2>/dev/null)

if [ -z "$TOKEN" ]; then
    echo "Warning: Could not get MMDS token" >&2
    exit 0
fi

# Fetch all sandfire metadata from MMDS
METADATA=$(curl -s "http://${MMDS_IP}/sandfire" \
    -H "X-metadata-token: ${TOKEN}" \
    -H "Accept: application/json" 2>/dev/null)

if [ -z "$METADATA" ]; then
    echo "Warning: Could not fetch MMDS metadata" >&2
    exit 0
fi

# Extract credentials using Python
CREDENTIALS=$(echo "$METADATA" | python3 -c "
import sys, json
data = json.load(sys.stdin)
creds = data.get('claude_credentials')
if creds:
    print(json.dumps(creds))
" 2>/dev/null)

if [ -z "$CREDENTIALS" ] || [ "$CREDENTIALS" = "null" ]; then
    echo "No Claude credentials found in MMDS metadata" >&2
    exit 0
fi

# Set up credentials for both root and sandfire users
for USER_HOME in /root /home/sandfire; do
    if [ -d "$USER_HOME" ]; then
        CLAUDE_DIR="${USER_HOME}/.claude"
        mkdir -p "$CLAUDE_DIR"

        # Write credentials file
        echo "$CREDENTIALS" > "${CLAUDE_DIR}/.credentials.json"
        chmod 600 "${CLAUDE_DIR}/.credentials.json"

        # Set ownership if it's the sandfire user's home
        if [ "$USER_HOME" = "/home/sandfire" ]; then
            chown -R sandfire:sandfire "$CLAUDE_DIR"
        fi

        echo "Configured Claude credentials and settings for $USER_HOME"
    fi
done
SCRIPT
chmod +x /usr/local/bin/sandfire-claude-credentials.sh

# Create systemd service to fetch Claude credentials at boot
cat > /etc/systemd/system/sandfire-claude-credentials.service << 'EOF'
[Unit]
Description=Fetch Claude Code credentials from MMDS
After=network.target sandfire-mmds-motd.service
Wants=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sandfire-claude-credentials.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl enable sandfire-claude-credentials.service

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
