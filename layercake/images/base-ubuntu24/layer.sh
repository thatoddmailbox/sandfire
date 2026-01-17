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

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
