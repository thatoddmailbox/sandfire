#!/bin/bash
# Start a VM by ID with optional ephemeral context

API_URL="${SANDFIRE_API:-http://localhost:9000}"

usage() {
    echo "Usage: $0 <vm-id> [options]"
    echo "Options:"
    echo "  --context <json>       Pass inline JSON context"
    echo "  --context-file <file>  Pass context from a JSON file"
    echo ""
    echo "Examples:"
    echo "  $0 vm-abc12345"
    echo "  $0 vm-abc12345 --context '{\"task\": \"run tests\"}'"
    echo "  $0 vm-abc12345 --context-file task.json"
    exit 1
}

if [ -z "$1" ]; then
    usage
fi

VM_ID="$1"
shift

CONTEXT=""

while [ $# -gt 0 ]; do
    case "$1" in
        --context)
            if [ -z "$2" ]; then
                echo "Error: --context requires a JSON argument"
                exit 1
            fi
            CONTEXT="$2"
            shift 2
            ;;
        --context-file)
            if [ -z "$2" ]; then
                echo "Error: --context-file requires a file path"
                exit 1
            fi
            if [ ! -f "$2" ]; then
                echo "Error: Context file not found: $2"
                exit 1
            fi
            CONTEXT=$(cat "$2")
            shift 2
            ;;
        *)
            echo "Error: Unknown option: $1"
            usage
            ;;
    esac
done

# Build request body
if [ -n "$CONTEXT" ]; then
    # Validate that context is valid JSON
    if ! echo "$CONTEXT" | jq . > /dev/null 2>&1; then
        echo "Error: Invalid JSON context"
        exit 1
    fi
    REQUEST_BODY=$(jq -n --argjson ctx "$CONTEXT" '{"context": $ctx}')
    response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms/${VM_ID}/start" \
        -H "Content-Type: application/json" \
        -d "$REQUEST_BODY")
else
    response=$(curl -s -w "\n%{http_code}" -X POST "${API_URL}/api/vms/${VM_ID}/start")
fi

http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$http_code" = "200" ]; then
    ip=$(echo "$body" | jq -r '.ip_address // "pending"')
    echo "VM ${VM_ID} started successfully"
    echo "IP Address: ${ip}"
    if [ -n "$CONTEXT" ]; then
        echo "Context: provided (available via sandfire-get-context inside VM)"
    fi
    echo ""
    echo "To connect: telnet ${ip}"
    echo "Login: root / Password: sandfire"
else
    echo "Failed to start VM ${VM_ID}"
    echo "$body" | jq -r '.error // .'
    exit 1
fi
