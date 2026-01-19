# SSH Proxy

SSH Proxy is a companion tool for Sandfire that provides SSH access to VMs. It acts as a jump host, authenticating users against their system SSH keys and proxying connections to Sandfire VMs.

## Building

```bash
cd sshproxy
go build -o sshproxy ./cmd/sshproxy
```

## How It Works

SSH Proxy runs on port 2222 (by default) and:

1. Authenticates users by checking their SSH public key against their system `~/.ssh/authorized_keys`
2. Queries the Sandfire API to list available VMs
3. Proxies SSH connections to VMs over the internal network

This allows users to connect to VMs without exposing each VM's SSH port directly.

## Usage

```
Usage: sshproxy [options]

Options:
  -listen string   Address to listen on (default ":2222")
  -api string      Sandfire API URL (default "http://localhost:9000")
  -hostkey string  Path to host key file (default "./data/sshproxy_host_key")
```

## Examples

Start the proxy:
```bash
$ ./sshproxy
2024/01/15 10:30:00 Generating new ED25519 host key...
2024/01/15 10:30:00 Saved new host key to ./data/sshproxy_host_key
2024/01/15 10:30:00 Starting Sandfire SSH proxy on :2222
2024/01/15 10:30:00 SSH proxy listening on :2222
```

Connect and use the interactive shell:
```bash
$ ssh -p 2222 alex@localhost

===========================================
  Sandfire SSH Proxy
===========================================

Commands:
  list              - List all VMs
  connect <vm-id>   - Connect to a VM
  help              - Show this help
  exit              - Exit the proxy

sandfire> list

ID              NAME                 STATE      IP
---             ----                 -----      --
vm-abc123       dev-server           running    10.0.0.2
vm-def456       web-test             stopped    -

sandfire> connect vm-abc123
Connecting to dev-server (vm-abc123) at 10.0.0.2...
Welcome to Ubuntu 24.04 LTS

sandfire@dev-server:~$
```

Connect directly to a VM:
```bash
$ ssh -t -p 2222 alex@localhost connect vm-abc123
Connecting to dev-server (vm-abc123) at 10.0.0.2...
Welcome to Ubuntu 24.04 LTS

sandfire@dev-server:~$
```

## Authentication

SSH Proxy authenticates users by:

1. Looking up the username as a system user
2. Reading `~/.ssh/authorized_keys` for that user
3. Checking if the presented SSH key matches any authorized key

This means users must have a local system account and their SSH public key in their `authorized_keys` file.
