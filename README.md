# Sandfire

Sandfire is a VM management service that uses Firecracker VMM for fast, secure microVM creation. It provides a simple HTTP API for managing virtual machines with automatic networking via NAT.

## Features

- REST API for VM lifecycle management (create, start, stop, delete)
- Firecracker VMM with jailer for security isolation
- Automatic networking with bridge interface and NAT
- SQLite database for persistent state
- Auto-restart of running VMs on server restart
- Graceful shutdown handling

## Requirements

- Linux with KVM support (`/dev/kvm` must be accessible)
- Firecracker and jailer installed (https://github.com/firecracker-microvm/firecracker)
- Root privileges (for network configuration and jailer)
- Go 1.21+ (for building)

## Quick Start

### 1. Build Sandfire

```bash
go build -o sandfire ./cmd/sandfire
```

### 2. Build an OS Image

Build an Ubuntu 24.04 image for VMs:

```bash
sudo ./scripts/build-ubuntu-image.sh
```

> [!NOTE]
> This builds a basic image to get started quickly. For real use, set up [layercake](./layercake) instead — it builds customizable root filesystems using composable layers (similar to Docker), so you can easily create and maintain purpose-built VM images.

### 3. Register the Image

```bash
sqlite3 ./data/sandfire.db "INSERT INTO os_images (id, name, kernel_path, rootfs_path) VALUES ('ubuntu-24.04', 'Ubuntu 24.04', '$(pwd)/data/images/ubuntu-24.04/vmlinux', '$(pwd)/data/images/ubuntu-24.04/rootfs.ext4');"
```

### 4. Start Sandfire

```bash
sudo ./sandfire
```

The server listens on port 9000 by default.

## API Reference

### Health Check

```
GET /health
```

Response:
```json
{"status": "ok"}
```

### List OS Images

```
GET /api/os-images
```

### List VMs

```
GET /api/vms
```

### Create VM

```
POST /api/vms
Content-Type: application/json

{
  "name": "my-vm",
  "os_image_id": "ubuntu-24.04",
  "ram_mb": 1024,
  "disk_size_gb": 10,
  "vcpu_count": 2,
  "internet_enabled": true
}
```

### Get VM

```
GET /api/vms/{id}
```

### Update VM (must be stopped)

```
PUT /api/vms/{id}
Content-Type: application/json

{
  "ram_mb": 2048,
  "vcpu_count": 4
}
```

### Delete VM (must be stopped)

```
DELETE /api/vms/{id}
```

### Start VM

```
POST /api/vms/{id}/start
```

### Stop VM

```
POST /api/vms/{id}/stop
```

## Configuration

Sandfire uses the following defaults:

| Setting | Value | Description |
|---------|-------|-------------|
| Port | 9000 | HTTP API port |
| Data Directory | `./data` | Storage for database, images, and VM disks |
| Bridge Name | `sandfire0` | Network bridge interface |
| Bridge IP | `10.20.30.1/24` | Bridge network |
| VM IP Range | `10.20.30.2-254` | IP addresses assigned to VMs |

## Directory Structure

```
./data/
├── sandfire.db              # SQLite database
├── images/                  # OS base images
│   └── ubuntu-24.04/
│       ├── vmlinux          # Kernel
│       └── rootfs.ext4      # Root filesystem
├── vms/                     # VM-specific data
│   └── {vm-id}/
│       └── rootfs.ext4      # VM's root disk (copy of base)
└── jails/                   # Firecracker jail directories
    └── {vm-id}/
        └── root/
```

## Networking

Each VM gets:
- A TAP device attached to the `sandfire0` bridge
- An IP address from the 10.20.30.0/24 range
- Gateway at 10.20.30.1
- NAT for internet access (if `internet_enabled` is true)

The IP is configured via kernel command line parameters at boot.

## VM Credentials

VMs created with `build-ubuntu-image.sh` have:

| Username | Password |
|----------|----------|
| root | sandfire |
| sandfire | sandfire |

SSH is enabled by default.

## Testing

Run the API test suite:

```bash
./scripts/test-api.sh
```

## Troubleshooting

### VM fails to start

1. Check that KVM is available: `ls -la /dev/kvm`
2. Ensure Firecracker is installed: `which firecracker jailer`
3. Check logs for errors

### Network not working

1. Verify bridge exists: `ip link show sandfire0`
2. Check NAT rules: `iptables -t nat -L -n`
3. Ensure IP forwarding is enabled: `cat /proc/sys/net/ipv4/ip_forward`

### Permission denied

Sandfire requires root privileges for:
- Creating bridge and TAP devices
- Managing iptables rules
- Running jailer

## Companion tools

In this same repository are two additional tools that help with using Sandfire VMs:
* [layercake](./layercake) - build VM root filesystems using multiple "layers", similar to Docker containers
* [sshproxy](./sshproxy) - provide a single SSH server that can forward connections (and ports) to any running VM
