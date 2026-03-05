# Sandfire

Sandfire is a service that lets you create isolated environments for AI coding agents. It uses [Firecracker](https://firecracker-microvm.github.io/) to create fast, secure microVMs.

## Motivation

You set up a VM image for something you are working on (using the companion [layercake](./layercake) tool) and then quickly spawn new VMs and launch agents with full permissions inside the VM. VMs get their own subdomain and you get SSH access.

For example, say you are working on a fullstack web app. You would set up an image with your backend and frontend servers running. Then start a VM with your image, SSH in and prompt your agent. The agent is equipped with the tools to not only make the change but test it with Playwright MCP and Google Chrome! And then you can review its work by accessing the VM's subdomain in your browser.

Thanks to their fast startup times, it is possible to create and delete VMs as you need them. You can also spin up multiple VMs to try different prompts on the same application in parallel.

### Why not containers/Docker?

I did not feel that containers provided the right level of isolation (especially from a security perspective, it just takes one wrong bind mount and then you have a container escape).

I also wanted to run applications that use Docker containers or Docker Compose inside the isolate environment. Nesting Docker inside Docker is complex and risky. But running Docker inside a microVM is easy, just install it like you normally would!

The main downside is that you have to make your own VM images, although [layercake](./layercake) can help with that. But if you have containers already, it is pretty easy to make a VM image that just installs Docker and then runs your application's containers.

## Features

- REST API for VM lifecycle management (create, start, stop, delete)
- Firecracker VMM with jailer for security isolation
- Fast VM startup times (a few seconds)
- Automatic networking with bridge interface and NAT
- SQLite database for persistent state

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

VMs created with layercake or `build-ubuntu-image.sh` have these users:

| Username | Password |
|----------|----------|
| root | sandfire |
| sandfire | sandfire |

SSH is enabled by default and can be accessed via the VM's private IP (10.20.30.x).

It's recommended to also set up the companion [sshproxy](./sshproxy) tool, which allows accessing the VMs remotely.

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
