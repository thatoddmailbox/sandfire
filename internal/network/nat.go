package network

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
)

type Manager struct {
	allocatedIPs map[string]bool
	natEnabled   bool
	mu           sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		allocatedIPs: make(map[string]bool),
	}
}

func (m *Manager) SetAllocatedIPs(ips []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ip := range ips {
		m.allocatedIPs[ip] = true
	}
}

func (m *Manager) AllocateIP() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := IPRangeStart; i <= IPRangeEnd; i++ {
		ip := fmt.Sprintf("10.20.30.%d", i)
		if !m.allocatedIPs[ip] {
			m.allocatedIPs[ip] = true
			return ip, nil
		}
	}

	return "", fmt.Errorf("no available IP addresses")
}

func (m *Manager) ReleaseIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allocatedIPs, ip)
}

func (m *Manager) EnableNAT() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.natEnabled {
		return nil
	}

	log.Printf("Enabling NAT for %s", NetworkCIDR)

	// Enable IP forwarding
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable ip forwarding: %w", err)
	}

	// Get default route interface
	outIface, err := getDefaultInterface()
	if err != nil {
		return fmt.Errorf("get default interface: %w", err)
	}

	// Add MASQUERADE rule (check if exists first)
	if !iptablesRuleExists("nat", "POSTROUTING", "-s", NetworkCIDR, "-o", outIface, "-j", "MASQUERADE") {
		if err := runCmd("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-s", NetworkCIDR, "-o", outIface, "-j", "MASQUERADE"); err != nil {
			return fmt.Errorf("add MASQUERADE rule: %w", err)
		}
	}

	// Add FORWARD rules
	if !iptablesRuleExists("filter", "FORWARD", "-i", BridgeName, "-o", outIface, "-j", "ACCEPT") {
		if err := runCmd("iptables", "-A", "FORWARD",
			"-i", BridgeName, "-o", outIface, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add FORWARD rule (outbound): %w", err)
		}
	}

	if !iptablesRuleExists("filter", "FORWARD", "-i", outIface, "-o", BridgeName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT") {
		if err := runCmd("iptables", "-A", "FORWARD",
			"-i", outIface, "-o", BridgeName,
			"-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add FORWARD rule (inbound): %w", err)
		}
	}

	m.natEnabled = true
	log.Printf("NAT enabled via %s", outIface)
	return nil
}

func getDefaultInterface() (string, error) {
	cmd := exec.Command("ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Parse "default via X.X.X.X dev ethX ..."
	parts := strings.Fields(string(output))
	for i, p := range parts {
		if p == "dev" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}

	return "", fmt.Errorf("could not determine default interface")
}

func iptablesRuleExists(table, chain string, args ...string) bool {
	cmdArgs := []string{"-t", table, "-C", chain}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("iptables", cmdArgs...)
	return cmd.Run() == nil
}
