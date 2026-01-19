# SSH Proxy Installation Guide

This guide covers installing SSH Proxy as a system service using systemd.

## Prerequisites

- Go 1.21+ (for building)
- Sandfire running and accessible
- Root privileges

## Installation Steps

### 1. Build SSH Proxy

```bash
cd /path/to/sandfire/sshproxy
go build -o sshproxy ./cmd/sshproxy
```

### 2. Install the Binary

```bash
sudo cp sshproxy /usr/local/bin/sshproxy
sudo chmod 755 /usr/local/bin/sshproxy
```

### 3. Create Data Directory

```bash
sudo mkdir -p /var/lib/sshproxy
```

### 4. Install the systemd Service

Create the systemd unit file:

```bash
sudo tee /etc/systemd/system/sshproxy.service << 'EOF'
[Unit]
Description=Sandfire SSH Proxy
After=network.target sandfire.service

[Service]
Type=simple
ExecStart=/usr/local/bin/sshproxy -listen :2222 -api http://localhost:9000 -hostkey /var/lib/sshproxy/host_key
WorkingDirectory=/var/lib/sshproxy
Restart=on-failure
RestartSec=5

# SSH Proxy requires root to read user authorized_keys files
User=root
Group=root

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sshproxy

[Install]
WantedBy=multi-user.target
EOF
```

### 5. Enable and Start the Service

```bash
# Reload systemd to pick up the new service
sudo systemctl daemon-reload

# Enable the service to start on boot
sudo systemctl enable sshproxy

# Start the service
sudo systemctl start sshproxy

# Check status
sudo systemctl status sshproxy
```

## Managing the Service

```bash
# Start the service
sudo systemctl start sshproxy

# Stop the service
sudo systemctl stop sshproxy

# Restart the service
sudo systemctl restart sshproxy

# View logs
sudo journalctl -u sshproxy -f

# View recent logs
sudo journalctl -u sshproxy --since "10 minutes ago"
```

## Verifying the Installation

After starting the service, verify it's running:

```bash
# Check service status
sudo systemctl status sshproxy

# Test SSH connection
ssh -p 2222 $(whoami)@localhost
```

## Uninstalling

```bash
# Stop and disable the service
sudo systemctl stop sshproxy
sudo systemctl disable sshproxy

# Remove the service file
sudo rm /etc/systemd/system/sshproxy.service
sudo systemctl daemon-reload

# Remove the binary
sudo rm /usr/local/bin/sshproxy

# Remove data directory
sudo rm -rf /var/lib/sshproxy
```

## Troubleshooting

### Service fails to start

Check the logs:
```bash
sudo journalctl -u sshproxy -n 50 --no-pager
```

### Cannot connect to Sandfire API

Ensure Sandfire is running:
```bash
sudo systemctl status sandfire
curl http://localhost:9000/health
```

### Authentication fails

Ensure you have an SSH key in your `~/.ssh/authorized_keys`:
```bash
cat ~/.ssh/authorized_keys
```

If empty, add your public key:
```bash
cat ~/.ssh/id_ed25519.pub >> ~/.ssh/authorized_keys
# or
cat ~/.ssh/id_rsa.pub >> ~/.ssh/authorized_keys
```
