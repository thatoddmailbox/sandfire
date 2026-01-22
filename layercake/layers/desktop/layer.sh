#!/bin/bash
set -e

# Desktop environment layer - Xfce4 + VNC + Google Chrome
# VNC server runs on display :0 (port 5900)

apt-get update

# Install Xfce desktop environment
apt-get install -y \
    xfce4 \
    xfce4-terminal \
    mousepad \
    dbus-x11

# Install TigerVNC server
apt-get install -y \
    tigervnc-standalone-server \
    tigervnc-common

# Install fonts (including symbola for emoji/symbols)
apt-get install -y fonts-symbola
fc-cache -fv

# Install mesa-utils so we at least have software rendering
apt-get install -y mesa-utils

# Set xfce4-terminal as default terminal emulator
update-alternatives --install /usr/bin/x-terminal-emulator x-terminal-emulator /usr/bin/xfce4-terminal 50
update-alternatives --set x-terminal-emulator /usr/bin/xfce4-terminal

# Install Google Chrome
curl -fsSL -o /tmp/google-chrome.deb "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb"
apt-get install -y /tmp/google-chrome.deb
rm -f /tmp/google-chrome.deb

# Remove gnome-keyring to avoid keyring prompt on Chrome startup
# Chrome will fall back to basic password storage automatically
apt-get purge -y gnome-keyring || true

# Configure Chrome policies to skip first-run dialog
# - Make Chrome default browser
# - Disable stats reporting to Google
# - Disable sign-in prompt
mkdir -p /etc/opt/chrome/policies/managed
cat > /etc/opt/chrome/policies/managed/sandfire.json << 'EOF'
{
    "DefaultBrowserSettingEnabled": true,
    "MetricsReportingEnabled": false,
    "BrowserSignin": 0,
    "SyncDisabled": true
}
EOF

# Configure VNC for sandfire user
SANDFIRE_HOME="/home/sandfire"
VNC_DIR="${SANDFIRE_HOME}/.vnc"

mkdir -p "$VNC_DIR"

# Set VNC password non-interactively
echo "sandfire" | vncpasswd -f > "${VNC_DIR}/passwd"
chmod 600 "${VNC_DIR}/passwd"

# Create xstartup script
cat > "${VNC_DIR}/xstartup" << 'EOF'
#!/bin/bash
unset SESSION_MANAGER
unset DBUS_SESSION_BUS_ADDRESS
export XKL_XMODMAP_DISABLE=1

# Source system profile to get proper PATH (includes Go, ~/.local/bin, etc.)
if [ -f /etc/profile ]; then
    . /etc/profile
fi

exec startxfce4
EOF
chmod +x "${VNC_DIR}/xstartup"

# Set ownership
chown -R sandfire:sandfire "$VNC_DIR"

# Create xfce4-terminal config with some good defaults
XFCE_CONFIG_DIR="${SANDFIRE_HOME}/.config/xfce4/xfconf/xfce-perchannel-xml"
mkdir -p "$XFCE_CONFIG_DIR"

cat > "${XFCE_CONFIG_DIR}/xfce4-terminal.xml" << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>

<channel name="xfce4-terminal" version="1.0">
  <property name="encoding" type="string" value="UTF-8"/>
  <property name="misc-show-unsafe-paste-dialog" type="bool" value="false"/>
  <property name="command-login-shell" type="bool" value="true"/>
</channel>
EOF

chown -R sandfire:sandfire "${SANDFIRE_HOME}/.config"

# Create systemd service to start VNC on display :0 at boot
cat > /etc/systemd/system/vncserver@.service << 'EOF'
[Unit]
Description=TigerVNC Server on display %i
After=network.target

[Service]
Type=simple
User=sandfire
Group=sandfire
WorkingDirectory=/home/sandfire
ExecStartPre=/bin/sh -c '/usr/bin/vncserver -kill :%i > /dev/null 2>&1 || :'
ExecStart=/usr/bin/vncserver :%i -geometry 1280x800 -depth 24 -localhost no -fg
ExecStop=/usr/bin/vncserver -kill :%i
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Enable VNC server on display :0
systemctl enable vncserver@0.service

# Set DISPLAY=:0 for all SSH sessions
cat > /etc/profile.d/vnc-display.sh << 'EOF'
# Set DISPLAY to VNC server display for SSH sessions
export DISPLAY=:0
EOF
chmod +x /etc/profile.d/vnc-display.sh

# Also set in /etc/environment for non-login shells
echo 'DISPLAY=:0' >> /etc/environment

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
