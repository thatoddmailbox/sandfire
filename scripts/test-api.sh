#!/bin/bash
set -e

# Sandfire API Test Script
# Tests the full VM lifecycle through the API

API_URL="${API_URL:-http://localhost:9000}"
VM_NAME="test-vm-$$"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

check_response() {
    local response="$1"
    local expected="$2"
    local description="$3"

    if echo "$response" | grep -q "$expected"; then
        log_info "$description: OK"
        return 0
    else
        log_error "$description: FAILED"
        echo "Response: $response"
        return 1
    fi
}

cleanup() {
    if [ -n "$VM_ID" ]; then
        log_info "Cleaning up VM $VM_ID..."
        # Stop if running
        curl -s -X POST "${API_URL}/api/vms/${VM_ID}/stop" > /dev/null 2>&1 || true
        sleep 1
        # Delete
        curl -s -X DELETE "${API_URL}/api/vms/${VM_ID}" > /dev/null 2>&1 || true
    fi
}

trap cleanup EXIT

echo "=== Sandfire API Test Suite ==="
echo "API URL: ${API_URL}"
echo ""

# Test 1: Health check
log_info "Test 1: Health check"
RESPONSE=$(curl -s "${API_URL}/health")
check_response "$RESPONSE" '"status":"ok"' "Health check"
echo ""

# Test 2: List OS images
log_info "Test 2: List OS images"
RESPONSE=$(curl -s "${API_URL}/api/os-images")
echo "Available OS images: $RESPONSE"
echo ""

# Check if we have an image to work with
if ! echo "$RESPONSE" | grep -q '"id"'; then
    log_warn "No OS images available. Please run build-ubuntu-image.sh first."
    log_warn "Skipping VM lifecycle tests."
    exit 0
fi

# Get first available image ID
OS_IMAGE_ID=$(echo "$RESPONSE" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
log_info "Using OS image: $OS_IMAGE_ID"
echo ""

# Test 3: Create VM
log_info "Test 3: Create VM"
RESPONSE=$(curl -s -X POST "${API_URL}/api/vms" \
    -H "Content-Type: application/json" \
    -d "{
        \"name\": \"${VM_NAME}\",
        \"os_image_id\": \"${OS_IMAGE_ID}\",
        \"ram_mb\": 512,
        \"disk_size_gb\": 2,
        \"vcpu_count\": 1,
        \"internet_enabled\": true
    }")

if ! echo "$RESPONSE" | grep -q '"id"'; then
    log_error "Failed to create VM"
    echo "Response: $RESPONSE"
    exit 1
fi

VM_ID=$(echo "$RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
log_info "Created VM: $VM_ID"
check_response "$RESPONSE" '"state":"stopped"' "VM initial state"
echo ""

# Test 4: Get VM details
log_info "Test 4: Get VM details"
RESPONSE=$(curl -s "${API_URL}/api/vms/${VM_ID}")
check_response "$RESPONSE" "\"id\":\"${VM_ID}\"" "Get VM by ID"
echo ""

# Test 5: List VMs
log_info "Test 5: List VMs"
RESPONSE=$(curl -s "${API_URL}/api/vms")
check_response "$RESPONSE" "\"id\":\"${VM_ID}\"" "VM appears in list"
echo ""

# Test 6: Update VM (while stopped)
log_info "Test 6: Update VM"
RESPONSE=$(curl -s -X PUT "${API_URL}/api/vms/${VM_ID}" \
    -H "Content-Type: application/json" \
    -d '{"ram_mb": 1024}')
check_response "$RESPONSE" '"ram_mb":1024' "Update VM RAM"
echo ""

# Test 7: Start VM
log_info "Test 7: Start VM"
RESPONSE=$(curl -s -X POST "${API_URL}/api/vms/${VM_ID}/start")

if echo "$RESPONSE" | grep -q '"error"'; then
    log_error "Failed to start VM"
    echo "Response: $RESPONSE"
    log_warn "This may be expected if Firecracker/jailer is not installed or KVM is unavailable"
else
    check_response "$RESPONSE" '"state":"running"' "VM started"

    # Get IP address
    IP_ADDRESS=$(echo "$RESPONSE" | grep -o '"ip_address":"[^"]*"' | cut -d'"' -f4)
    if [ -n "$IP_ADDRESS" ]; then
        log_info "VM IP address: $IP_ADDRESS"

        # Wait for VM to boot
        log_info "Waiting for VM to boot..."
        sleep 5

        # Test 8: Ping VM (if possible)
        log_info "Test 8: Ping VM"
        if ping -c 1 -W 2 "$IP_ADDRESS" > /dev/null 2>&1; then
            log_info "VM is reachable at $IP_ADDRESS"
        else
            log_warn "Cannot ping VM (may need network setup)"
        fi
    fi
    echo ""

    # Test 9: Stop VM
    log_info "Test 9: Stop VM"
    RESPONSE=$(curl -s -X POST "${API_URL}/api/vms/${VM_ID}/stop")
    check_response "$RESPONSE" '"state":"stopped"' "VM stopped"
fi
echo ""

# Test 10: Delete VM
log_info "Test 10: Delete VM"
# Make sure it's stopped first
curl -s -X POST "${API_URL}/api/vms/${VM_ID}/stop" > /dev/null 2>&1 || true
sleep 1

RESPONSE=$(curl -s -X DELETE "${API_URL}/api/vms/${VM_ID}")
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "${API_URL}/api/vms/${VM_ID}")

# Already deleted in first call, verify it's gone
RESPONSE=$(curl -s "${API_URL}/api/vms/${VM_ID}")
check_response "$RESPONSE" '"error"' "VM deleted"

# Clear VM_ID so cleanup doesn't try to delete again
VM_ID=""
echo ""

echo "=== All tests completed ==="
