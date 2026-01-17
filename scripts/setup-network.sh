#!/bin/bash
set -e

# Sandfire Network Setup Script
# Sets up the bridge interface and NAT rules for VM networking

BRIDGE_NAME="sandfire0"
BRIDGE_IP="10.20.30.1/24"
NETWORK="10.20.30.0/24"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Check for root
if [ "$EUID" -ne 0 ]; then
    log_error "This script must be run as root"
    exit 1
fi

echo "=== Sandfire Network Setup ==="
echo ""

# Detect default interface
DEFAULT_IFACE=$(ip route show default | awk '/default/ {print $5}' | head -1)
if [ -z "$DEFAULT_IFACE" ]; then
    log_error "Could not detect default network interface"
    exit 1
fi
log_info "Default interface: $DEFAULT_IFACE"

# Create bridge if it doesn't exist
if ip link show "$BRIDGE_NAME" &> /dev/null; then
    log_info "Bridge $BRIDGE_NAME already exists"
else
    log_info "Creating bridge $BRIDGE_NAME..."
    ip link add "$BRIDGE_NAME" type bridge
    log_info "Bridge created"
fi

# Assign IP to bridge
if ip addr show "$BRIDGE_NAME" | grep -q "inet "; then
    log_info "Bridge already has IP address"
else
    log_info "Assigning IP $BRIDGE_IP to bridge..."
    ip addr add "$BRIDGE_IP" dev "$BRIDGE_NAME"
fi

# Bring up bridge
ip link set "$BRIDGE_NAME" up
log_info "Bridge $BRIDGE_NAME is up"

# Enable IP forwarding
log_info "Enabling IP forwarding..."
sysctl -w net.ipv4.ip_forward=1 > /dev/null
echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/99-sandfire.conf

# Setup NAT rules
log_info "Setting up NAT rules..."

# MASQUERADE rule
if ! iptables -t nat -C POSTROUTING -s "$NETWORK" -o "$DEFAULT_IFACE" -j MASQUERADE 2>/dev/null; then
    iptables -t nat -A POSTROUTING -s "$NETWORK" -o "$DEFAULT_IFACE" -j MASQUERADE
    log_info "Added MASQUERADE rule"
else
    log_info "MASQUERADE rule already exists"
fi

# FORWARD rules
if ! iptables -C FORWARD -i "$BRIDGE_NAME" -o "$DEFAULT_IFACE" -j ACCEPT 2>/dev/null; then
    iptables -A FORWARD -i "$BRIDGE_NAME" -o "$DEFAULT_IFACE" -j ACCEPT
    log_info "Added FORWARD rule (outbound)"
else
    log_info "FORWARD rule (outbound) already exists"
fi

if ! iptables -C FORWARD -i "$DEFAULT_IFACE" -o "$BRIDGE_NAME" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null; then
    iptables -A FORWARD -i "$DEFAULT_IFACE" -o "$BRIDGE_NAME" -m state --state RELATED,ESTABLISHED -j ACCEPT
    log_info "Added FORWARD rule (inbound)"
else
    log_info "FORWARD rule (inbound) already exists"
fi

echo ""
echo "=== Network Setup Complete ==="
echo ""
echo "Bridge: $BRIDGE_NAME ($BRIDGE_IP)"
echo "NAT via: $DEFAULT_IFACE"
echo "VM network: $NETWORK"
echo ""
echo "VMs will receive IPs from 10.20.30.2 to 10.20.30.254"
echo "Gateway: 10.20.30.1"
echo ""
log_warn "Note: These iptables rules are not persistent across reboots."
log_warn "To make them persistent, install iptables-persistent or add to rc.local"
echo ""
echo "To make bridge persistent, add to /etc/network/interfaces or use systemd-networkd:"
echo ""
echo "  # /etc/systemd/network/sandfire0.netdev"
echo "  [NetDev]"
echo "  Name=sandfire0"
echo "  Kind=bridge"
echo ""
echo "  # /etc/systemd/network/sandfire0.network"
echo "  [Match]"
echo "  Name=sandfire0"
echo ""
echo "  [Network]"
echo "  Address=10.20.30.1/24"
