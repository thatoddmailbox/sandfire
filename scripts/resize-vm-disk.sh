#!/bin/bash
# Resize a stopped VM's disk

API_URL="${SANDFIRE_API:-http://localhost:9000}"

if [ -z "$1" ]; then
    echo "Usage: $0 <vm-id> [size_gb]"
    echo "Example: $0 vm-abc12345 32"
    echo "Omit size_gb to show the disk's current size and usage."
    exit 1
fi

VM_ID="$1"
SIZE_GB="$2"

if [ -z "$SIZE_GB" ]; then
    response=$(curl -s -w "\n%{http_code}" "${API_URL}/api/vms/${VM_ID}/disk")
    http_code=$(echo "$response" | tail -1)
    body=$(echo "$response" | head -n -1)

    if [ "$http_code" = "200" ]; then
        echo "$body" | jq -r '
            def gb: . / 1073741824 * 10 | round / 10 | tostring + " GB";
            "Size:    \(.size_bytes | gb)",
            "Used:    \(.used_bytes | gb)",
            "Minimum: \(.min_size_bytes / 1073741824 | ceil) GB"'
    else
        echo "Failed to get disk info for VM ${VM_ID}"
        echo "$body" | jq -r '.error // .'
        exit 1
    fi
    exit 0
fi

response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms/${VM_ID}/resize-disk" \
    -H "Content-Type: application/json" \
    -d "{\"size_gb\": ${SIZE_GB}}")
http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "200" ]; then
    echo "VM ${VM_ID} disk resized to ${SIZE_GB} GB"
else
    echo "Failed to resize disk for VM ${VM_ID}"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
