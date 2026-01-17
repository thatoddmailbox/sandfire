#!/bin/bash
# Stop a VM by ID

API_URL="${SANDFIRE_API:-http://localhost:9000}"

if [ -z "$1" ]; then
    echo "Usage: $0 <vm-id>"
    echo "Example: $0 vm-abc12345"
    exit 1
fi

VM_ID="$1"

response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms/${VM_ID}/stop")
http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "200" ]; then
    echo "VM ${VM_ID} stopped successfully"
else
    echo "Failed to stop VM ${VM_ID}"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
