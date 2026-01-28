#!/bin/bash
# Create a new VM

API_URL="${SANDFIRE_API:-http://localhost:9000}"

usage() {
    echo "Usage: $0 <name> <image> [ram_mb] [vcpu_count]"
    echo ""
    echo "Arguments:"
    echo "  name        VM name (required)"
    echo "  image       OS image ID (required)"
    echo "  ram_mb      RAM in MB (default: 512)"
    echo "  vcpu_count  Number of vCPUs (default: 1)"
    echo ""
    echo "Examples:"
    echo "  $0 my-vm ubuntu-24.04"
    echo "  $0 my-vm ubuntu-24.04 1024 2"
    exit 1
}

if [ -z "$1" ] || [ -z "$2" ]; then
    usage
fi

NAME="$1"
IMAGE="$2"
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
