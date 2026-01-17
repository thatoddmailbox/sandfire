package network

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

const (
	BridgeName   = "sandfire0"
	BridgeIP     = "10.20.30.1"
	BridgeCIDR   = "10.20.30.1/24"
	NetworkCIDR  = "10.20.30.0/24"
	IPRangeStart = 2
	IPRangeEnd   = 254
)

func (m *Manager) EnsureBridge() error {
	// Check if bridge exists
	if bridgeExists(BridgeName) {
		log.Printf("Bridge %s already exists", BridgeName)
		return nil
	}

	log.Printf("Creating bridge %s", BridgeName)

	// Create bridge
	if err := runCmd("ip", "link", "add", BridgeName, "type", "bridge"); err != nil {
		return fmt.Errorf("create bridge: %w", err)
	}

	// Assign IP
	if err := runCmd("ip", "addr", "add", BridgeCIDR, "dev", BridgeName); err != nil {
		// Bridge might already have IP
		if !strings.Contains(err.Error(), "RTNETLINK answers: File exists") {
			return fmt.Errorf("assign bridge IP: %w", err)
		}
	}

	// Bring up
	if err := runCmd("ip", "link", "set", BridgeName, "up"); err != nil {
		return fmt.Errorf("bring up bridge: %w", err)
	}

	log.Printf("Bridge %s created with IP %s", BridgeName, BridgeCIDR)
	return nil
}

func bridgeExists(name string) bool {
	cmd := exec.Command("ip", "link", "show", name)
	return cmd.Run() == nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(output), err)
	}
	return nil
}
