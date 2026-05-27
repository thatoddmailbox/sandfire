# Sandfire CLI Scripts

Simple shell scripts for managing Sandfire VMs. Requires `curl` and `jq`.

## Scripts

### list-vms.sh
List all VMs with their status.
```bash
./list-vms.sh
```

### create-vm.sh
Create a new VM.
```bash
./create-vm.sh <name> [image] [ram_mb] [vcpu]

# Examples:
./create-vm.sh my-vm                    # 512MB RAM, 1 vCPU
./create-vm.sh webserver ubuntu-24.04 1024 2   # 1GB RAM, 2 vCPUs
```

### start-vm.sh
Start a stopped VM.
```bash
./start-vm.sh <vm-id>

# Example:
./start-vm.sh vm-abc12345
```

### stop-vm.sh
Stop a running VM.
```bash
./stop-vm.sh <vm-id>

# Example:
./stop-vm.sh vm-abc12345
```

### delete-vm.sh
Delete a VM (must be stopped first).
```bash
./delete-vm.sh <vm-id>

# Example:
./delete-vm.sh vm-abc12345
```

## Configuration

Set `SANDFIRE_API` environment variable to use a different API endpoint:
```bash
export SANDFIRE_API=http://192.168.1.100:9000
```
