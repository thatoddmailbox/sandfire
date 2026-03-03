# Sandfire Installation Guide

This guide covers installing Sandfire as a system service using systemd.

## Prerequisites

- Linux with KVM support (`/dev/kvm` must be accessible)
- Go 1.21+ (for building)
- Firecracker and jailer installed
- Root privileges

### Installing Firecracker

Download and install Firecracker from the official releases:

```bash
# Download the latest release (adjust version as needed)
ARCH="$(uname -m)"
release_url="https://github.com/firecracker-microvm/firecracker/releases"
latest=$(curl -fsSLI -o /dev/null -w %{url_effective} ${release_url}/latest | rev | cut -d'/' -f1 | rev)

curl -L ${release_url}/download/${latest}/firecracker-${latest}-${ARCH}.tgz | tar -xz

# Install binaries
sudo mv release-${latest}-${ARCH}/firecracker-${latest}-${ARCH} /usr/local/bin/firecracker
sudo mv release-${latest}-${ARCH}/jailer-${latest}-${ARCH} /usr/local/bin/jailer

# Verify installation
firecracker --version
jailer --version
```

## Installation Steps

### 1. Build Sandfire

```bash
cd /path/to/sandfire
go build -o sandfire ./cmd/sandfire
```

### 2. Install the Binary

```bash
sudo cp sandfire /usr/local/bin/sandfire
sudo chmod 755 /usr/local/bin/sandfire
```

### 3. Create Data Directory

```bash
sudo mkdir -p /var/lib/sandfire/data
```

### 4. Create Environment File (Optional but Recommended)

Sandfire uses an environment file for configuration, similar to Caddy. Create the envfile:

```bash
sudo tee /var/lib/sandfire/envfile << 'EOF'
# Sandfire environment configuration
# This file contains secrets - do not commit to version control!

# Cloudflare API token for DNS-01 ACME challenges (required for certificate management)
CLOUDFLARE_API_TOKEN=your-token-here

# Base domain for VM subdomains
SANDFIRE_DOMAIN=sand.example.com

# Set to 1 to use Let's Encrypt staging environment (for testing)
#SANDFIRE_ACME_STAGING=1
EOF

# Secure the file (contains secrets)
sudo chmod 600 /var/lib/sandfire/envfile
```

If no envfile is present, Sandfire will log a warning but continue running with default values.

### 5. Build and Register an OS Image (Optional)

If you haven't already built an OS image:

```bash
# Build the image (run from sandfire source directory)
sudo ./scripts/build-ubuntu-image.sh

# Copy the image to the system data directory
sudo cp -r ./data/images /var/lib/sandfire/data/

# Register the image in the database
sudo sqlite3 /var/lib/sandfire/data/sandfire.db \
  "INSERT INTO os_images (id, name, kernel_path, rootfs_path) VALUES \
   ('ubuntu-24.04', 'Ubuntu 24.04', \
    '/var/lib/sandfire/data/images/ubuntu-24.04/vmlinux', \
    '/var/lib/sandfire/data/images/ubuntu-24.04/rootfs.ext4');"
```

### 6. Install the systemd Service

Create the systemd unit file:

```bash
sudo tee /etc/systemd/system/sandfire.service << 'EOF'
[Unit]
Description=Sandfire VM Manager
Documentation=https://github.com/thatoddmailbox/sandfire
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/sandfire
WorkingDirectory=/var/lib/sandfire
Restart=on-failure
RestartSec=5

# Sandfire requires root for network and jailer operations
User=root
Group=root

# Environment file for secrets and configuration
# The dash prefix means the service won't fail if the file doesn't exist
EnvironmentFile=-/var/lib/sandfire/envfile

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sandfire

[Install]
WantedBy=multi-user.target
EOF
```

### 7. Enable and Start the Service

```bash
# Reload systemd to pick up the new service
sudo systemctl daemon-reload

# Enable the service to start on boot
sudo systemctl enable sandfire

# Start the service
sudo systemctl start sandfire

# Check status
sudo systemctl status sandfire
```

## Managing the Service

```bash
# Start the service
sudo systemctl start sandfire

# Stop the service
sudo systemctl stop sandfire

# Restart the service
sudo systemctl restart sandfire

# View logs
sudo journalctl -u sandfire -f

# View recent logs
sudo journalctl -u sandfire --since "10 minutes ago"
```

## Verifying the Installation

After starting the service, verify it's running:

```bash
# Check service status
sudo systemctl status sandfire

# Test the API
curl http://localhost:9000/health
```

Expected response:
```json
{"status": "ok"}
```

## Uninstalling

```bash
# Stop and disable the service
sudo systemctl stop sandfire
sudo systemctl disable sandfire

# Remove the service file
sudo rm /etc/systemd/system/sandfire.service
sudo systemctl daemon-reload

# Remove the binary
sudo rm /usr/local/bin/sandfire

# Optionally remove data (WARNING: destroys all VMs and data)
sudo rm -rf /var/lib/sandfire
```

## Troubleshooting

### Service fails to start

Check the logs:
```bash
sudo journalctl -u sandfire -n 50 --no-pager
```

### Permission denied errors

Ensure the service is running as root and has access to:
- `/dev/kvm`
- `/dev/net/tun`
- `/var/lib/sandfire`

### Network issues

Sandfire automatically creates the bridge and NAT rules on startup. Verify the bridge exists:
```bash
ip link show sandfire0
```

If missing, check the sandfire logs for network-related errors:
```bash
sudo journalctl -u sandfire | grep -i bridge
```
