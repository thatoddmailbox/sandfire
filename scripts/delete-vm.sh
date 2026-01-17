#!/bin/bash
# Delete a VM by ID (VM must be stopped first)

API_URL="${SANDFIRE_API:-http://localhost:9000}"

if [ -z "$1" ]; then
    echo "Usage: $0 <vm-id>"
    echo "Example: $0 vm-abc12345"
    exit 1
fi

VM_ID="$1"

# Check VM state first
state=$(curl -s "${API_URL}/api/vms/${VM_ID}" | jq -r '.state // "unknown"')

if [ "$state" = "running" ]; then
    echo "Error: VM ${VM_ID} is running. Stop it first with: ./stop-vm.sh ${VM_ID}"
    exit 1
fi

response=$(curl -s -w "\n%{http_code}" -X DELETE "${API_URL}/api/vms/${VM_ID}")
http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "200" ] || [ "$http_code" = "204" ]; then
    echo "VM ${VM_ID} deleted successfully"
else
    echo "Failed to delete VM ${VM_ID}"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
