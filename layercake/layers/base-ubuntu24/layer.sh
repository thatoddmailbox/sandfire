#!/bin/bash
set -e

# Base Ubuntu 24.04 configuration for Firecracker VMs
# This runs AFTER debootstrap, inside the chroot

# Set up fstab
cat > /etc/fstab << 'EOF'
/dev/vda    /    ext4    defaults    0 1
EOF

# Configure systemd-networkd
mkdir -p /etc/systemd/network

# Rename virtio network interface to eth0
cat > /etc/systemd/network/10-virtio.link << 'EOF'
[Match]
Driver=virtio_net

[Link]
Name=eth0
EOF

cat > /etc/systemd/network/20-eth0.network << 'EOF'
[Match]
Name=eth0

[Network]
DHCP=no
KeepConfiguration=yes
EOF

systemctl enable systemd-networkd

# Janky fix to unblock nginx, TODO this should work properly
systemctl disable systemd-networkd-wait-online.service
systemctl mask systemd-networkd-wait-online.service

# Set timezone
ln -sf /usr/share/zoneinfo/America/New_York /etc/localtime
echo "America/New_York" > /etc/timezone

# Set hostname
echo "sandfire-vm" > /etc/hostname

# Configure hosts
cat > /etc/hosts << 'EOF'
127.0.0.1   localhost
127.0.1.1   sandfire-vm
EOF

# Set root password
echo "root:sandfire" | chpasswd

# Create sandfire user
useradd -m -s /bin/bash -G sudo sandfire
echo "sandfire:sandfire" | chpasswd

# Configure SSH
sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config
sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/' /etc/ssh/sshd_config
systemctl enable ssh

# Set up busybox telnet server (alternative to SSH)
cat > /etc/systemd/system/telnet.service << 'EOF'
[Unit]
Description=Telnet Server
After=network.target

[Service]
ExecStart=/bin/busybox telnetd -F -l /bin/login
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
systemctl enable telnet.service

# Configure serial console with autologin
mkdir -p /etc/systemd/system/serial-getty@ttyS0.service.d
cat > /etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf << 'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty -o '-p -f -- \\u' --keep-baud 115200,38400,9600 --autologin root --noclear %I $TERM
EOF
systemctl enable serial-getty@ttyS0.service

# Set up MMDS route and MOTD update script
cat > /usr/local/bin/sandfire-mmds-motd.sh << 'SCRIPT'
#!/bin/bash
# Fetch VM metadata from Firecracker MMDS and update MOTD
# This runs once at boot since VM name/ID don't change

MMDS_IP="169.254.169.254"
MMDS_IFACE="eth0"

# Add route to MMDS (link-local address needs explicit route)
ip route add ${MMDS_IP}/32 dev ${MMDS_IFACE} 2>/dev/null || true

# Get MMDS V2 token (valid for 300 seconds, more than enough for this)
TOKEN=$(curl -s -X PUT "http://${MMDS_IP}/latest/api/token" \
    -H "X-metadata-token-ttl-seconds: 300" 2>/dev/null)

if [ -z "$TOKEN" ]; then
    echo "Warning: Could not get MMDS token" >&2
    exit 0
fi

# Fetch VM metadata
METADATA=$(curl -s "http://${MMDS_IP}/sandfire" \
    -H "X-metadata-token: ${TOKEN}" \
    -H "Accept: application/json" 2>/dev/null)

if [ -z "$METADATA" ]; then
    echo "Warning: Could not fetch MMDS metadata" >&2
    exit 0
fi

# Parse VM name, ID, and domain using Python (available in base image)
VM_NAME=$(echo "$METADATA" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('vm_name',''))" 2>/dev/null)
VM_ID=$(echo "$METADATA" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('vm_id',''))" 2>/dev/null)
VM_DOMAIN=$(echo "$METADATA" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('domain',''))" 2>/dev/null)

# Write domain to /etc/sandfire/domain if available
if [ -n "$VM_DOMAIN" ]; then
    mkdir -p /etc/sandfire
    echo "$VM_DOMAIN" > /etc/sandfire/domain
fi

# Update MOTD with VM information
cat > /etc/motd << EOF

This is ${VM_NAME:-unknown} (${VM_ID:-unknown}).

EOF
SCRIPT
chmod +x /usr/local/bin/sandfire-mmds-motd.sh

# Create systemd service to run MMDS MOTD script at boot
cat > /etc/systemd/system/sandfire-mmds-motd.service << 'EOF'
[Unit]
Description=Fetch Sandfire VM metadata and update MOTD
After=network.target
Wants=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sandfire-mmds-motd.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl enable sandfire-mmds-motd.service

# Set up DNS resolution (remove host's resolv.conf from debootstrap)
rm -f /etc/resolv.conf
echo "nameserver 8.8.8.8" > /etc/resolv.conf
echo "nameserver 1.1.1.1" >> /etc/resolv.conf

# Configure git user if secrets are provided
# Note: We write directly to .gitconfig because git config requires /dev/null
# which isn't available in the chroot environment
if [ -n "$GIT_USER_NAME" ] || [ -n "$GIT_USER_EMAIL" ]; then
    {
        echo "[user]"
        [ -n "$GIT_USER_NAME" ] && echo "	name = $GIT_USER_NAME"
        [ -n "$GIT_USER_EMAIL" ] && echo "	email = $GIT_USER_EMAIL"
    } > /home/sandfire/.gitconfig
    chown sandfire:sandfire /home/sandfire/.gitconfig
    echo "Git config written to /home/sandfire/.gitconfig"
    [ -n "$GIT_USER_NAME" ] && echo "  user.name = $GIT_USER_NAME"
    [ -n "$GIT_USER_EMAIL" ] && echo "  user.email = $GIT_USER_EMAIL"
fi

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
