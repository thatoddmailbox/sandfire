#!/bin/bash
# Start a VM by ID

API_URL="${SANDFIRE_API:-http://localhost:9000}"

if [ -z "$1" ]; then
    echo "Usage: $0 <vm-id>"
    echo "Example: $0 vm-abc12345"
    exit 1
fi

VM_ID="$1"

response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms/${VM_ID}/start")
http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "200" ]; then
    ip=$(echo "$body" | jq -r '.ip_address // "pending"')
    echo "VM ${VM_ID} started successfully"
    echo "IP Address: ${ip}"
    echo ""
    echo "To connect: telnet ${ip}"
    echo "Login: root / Password: sandfire"
else
    echo "Failed to start VM ${VM_ID}"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
