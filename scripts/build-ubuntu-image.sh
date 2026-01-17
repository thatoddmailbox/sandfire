#!/bin/bash
set -e

# Build Ubuntu 24.04 image for Firecracker
# This script downloads and prepares a minimal Ubuntu rootfs and kernel

# Use Firecracker CI kernel - Linux 6.1 with virtio-rng support
KERNEL_VERSION="6.1.155"
FIRECRACKER_CI_VERSION="v1.14"
KERNEL_URL="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/${FIRECRACKER_CI_VERSION}/x86_64/vmlinux-${KERNEL_VERSION}"
UBUNTU_VERSION="noble"
IMAGE_ID="ubuntu-24.04-2"
IMAGE_NAME="Ubuntu 24.04"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${SCRIPT_DIR}/../data"
IMAGE_DIR="${DATA_DIR}/images/${IMAGE_ID}"
WORK_DIR="/tmp/sandfire-build-$$"

echo "=== Sandfire Ubuntu 24.04 Image Builder ==="
echo "Image directory: ${IMAGE_DIR}"

# Check for root
if [ "$EUID" -ne 0 ]; then
    echo "Error: This script must be run as root"
    exit 1
fi

# Check dependencies
for cmd in wget debootstrap; do
    if ! command -v $cmd &> /dev/null; then
        echo "Error: $cmd is required but not installed"
        exit 1
    fi
done

# Create directories
mkdir -p "${IMAGE_DIR}"
mkdir -p "${WORK_DIR}"
cd "${WORK_DIR}"

echo ""
echo "=== Downloading Firecracker kernel ==="
if [ ! -f "${IMAGE_DIR}/vmlinux" ]; then
    wget -q --show-progress -O "${IMAGE_DIR}/vmlinux" "${KERNEL_URL}"
    echo "Kernel downloaded"
else
    echo "Kernel already exists, skipping download"
fi

echo ""
echo "=== Building Ubuntu rootfs ==="
ROOTFS="${IMAGE_DIR}/rootfs.ext4"

if [ -f "${ROOTFS}" ]; then
    echo "Rootfs already exists. Remove it to rebuild."
    echo "  rm ${ROOTFS}"
    echo "Skipping rootfs build."
else
    ROOTFS_DIR="${WORK_DIR}/rootfs"
    mkdir -p "${ROOTFS_DIR}"

    echo "Running debootstrap (this may take a while)..."
    debootstrap --arch=amd64 --variant=minbase --include=systemd,systemd-sysv,udev,iproute2,iputils-ping,openssh-server,sudo,curl,ca-certificates,passwd,busybox-static,python3,htop \
        "${UBUNTU_VERSION}" "${ROOTFS_DIR}" http://archive.ubuntu.com/ubuntu/

    echo "Configuring rootfs..."

    # Note: We intentionally skip mounting /proc, /sys, /dev in the chroot.
    # systemctl enable will warn but still creates symlinks correctly.
    # This is safer than exposing host filesystems to the chroot.

    # Set up fstab
    cat > "${ROOTFS_DIR}/etc/fstab" << 'EOF'
/dev/vda    /    ext4    defaults    0 1
EOF

    # Configure networking via systemd-networkd
    mkdir -p "${ROOTFS_DIR}/etc/systemd/network"

    # Rename virtio network interface to eth0 (critical for networking to work)
    cat > "${ROOTFS_DIR}/etc/systemd/network/10-virtio.link" << 'EOF'
[Match]
Driver=virtio_net

[Link]
Name=eth0
EOF

    cat > "${ROOTFS_DIR}/etc/systemd/network/20-eth0.network" << 'EOF'
[Match]
Name=eth0

[Network]
DHCP=no
EOF

    # Enable systemd-networkd
    chroot "${ROOTFS_DIR}" systemctl enable systemd-networkd

    # Set hostname
    echo "sandfire-vm" > "${ROOTFS_DIR}/etc/hostname"

    # Configure hosts
    cat > "${ROOTFS_DIR}/etc/hosts" << 'EOF'
127.0.0.1   localhost
127.0.1.1   sandfire-vm
EOF

    # Set root password (changeme)
    echo "root:sandfire" | chroot "${ROOTFS_DIR}" /usr/sbin/chpasswd

    # Create a regular user
    chroot "${ROOTFS_DIR}" /usr/sbin/useradd -m -s /bin/bash -G sudo sandfire
    echo "sandfire:sandfire" | chroot "${ROOTFS_DIR}" /usr/sbin/chpasswd

    # Allow password auth for SSH (for testing)
    sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' "${ROOTFS_DIR}/etc/ssh/sshd_config"
    sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/' "${ROOTFS_DIR}/etc/ssh/sshd_config"

    # Enable SSH
    chroot "${ROOTFS_DIR}" systemctl enable ssh

    # Set up busybox telnet server (alternative to SSH, works without entropy)
    # Note: busybox-static already installs /usr/bin/busybox, and with usrmerge
    # /bin is a symlink to usr/bin, so no additional symlink is needed.
    cat > "${ROOTFS_DIR}/etc/systemd/system/telnet.service" << 'EOF'
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
    chroot "${ROOTFS_DIR}" systemctl enable telnet.service

    # Configure serial console
    mkdir -p "${ROOTFS_DIR}/etc/systemd/system/serial-getty@ttyS0.service.d"
    cat > "${ROOTFS_DIR}/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf" << 'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty -o '-p -f -- \\u' --keep-baud 115200,38400,9600 --autologin root --noclear %I $TERM
EOF
    chroot "${ROOTFS_DIR}" systemctl enable serial-getty@ttyS0.service

    # Clean up apt cache
    rm -rf "${ROOTFS_DIR}/var/cache/apt/archives"/*
    rm -rf "${ROOTFS_DIR}/var/lib/apt/lists"/*

    echo "Creating ext4 image..."
    # Create 2GB sparse image (will be resized per VM)
    dd if=/dev/zero of="${ROOTFS}" bs=1M count=0 seek=2048 2>/dev/null
    mkfs.ext4 -F -L rootfs "${ROOTFS}"

    # Mount and copy
    MOUNT_DIR="${WORK_DIR}/mnt"
    mkdir -p "${MOUNT_DIR}"
    mount -o loop "${ROOTFS}" "${MOUNT_DIR}"
    cp -a "${ROOTFS_DIR}"/* "${MOUNT_DIR}"/
    umount "${MOUNT_DIR}"

    echo "Rootfs created"
fi

# Clean up work directory
rm -rf "${WORK_DIR}"

echo ""
echo "=== Registering image with Sandfire ==="

# Provide instructions on how to register the image
echo "Run this command to register the image:"
echo ""
echo "  sqlite3 ${DATA_DIR}/sandfire.db \"INSERT INTO os_images (id, name, kernel_path, rootfs_path) VALUES ('${IMAGE_ID}', '${IMAGE_NAME}', '${IMAGE_DIR}/vmlinux', '${IMAGE_DIR}/rootfs.ext4');\""

echo ""
echo "=== Build complete ==="
echo "Kernel: ${IMAGE_DIR}/vmlinux"
echo "Rootfs: ${IMAGE_DIR}/rootfs.ext4"
echo ""
echo "Default credentials:"
echo "  Username: root     Password: sandfire"
echo "  Username: sandfire Password: sandfire"
