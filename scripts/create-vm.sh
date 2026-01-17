#!/bin/bash
# Create a new VM

API_URL="${SANDFIRE_API:-http://localhost:9000}"

NAME="${1:-my-vm}"
IMAGE="${2:-ubuntu-24.04}"
RAM="${3:-512}"
VCPU="${4:-1}"

response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms" \
    -H "Content-Type: application/json" \
    -d "{
        \"name\": \"${NAME}\",
        \"os_image_id\": \"${IMAGE}\",
        \"ram_mb\": ${RAM},
        \"vcpu_count\": ${VCPU},
        \"internet_enabled\": true
    }")

http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "201" ]; then
    vm_id=$(echo "$body" | jq -r '.id')
    echo "VM created successfully"
    echo "ID: ${vm_id}"
    echo ""
    echo "To start: ./start-vm.sh ${vm_id}"
else
    echo "Failed to create VM"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
