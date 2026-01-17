package network

import (
	"fmt"
	"log"
)

func (m *Manager) CreateTap(name string) error {
	log.Printf("Creating TAP device %s", name)

	// Create TAP device
	if err := runCmd("ip", "tuntap", "add", name, "mode", "tap"); err != nil {
		return fmt.Errorf("create tap: %w", err)
	}

	// Attach to bridge
	if err := runCmd("ip", "link", "set", name, "master", BridgeName); err != nil {
		runCmd("ip", "link", "delete", name)
		return fmt.Errorf("attach tap to bridge: %w", err)
	}

	// Bring up
	if err := runCmd("ip", "link", "set", name, "up"); err != nil {
		runCmd("ip", "link", "delete", name)
		return fmt.Errorf("bring up tap: %w", err)
	}

	log.Printf("TAP device %s created and attached to %s", name, BridgeName)
	return nil
}

func (m *Manager) DeleteTap(name string) error {
	log.Printf("Deleting TAP device %s", name)

	if err := runCmd("ip", "link", "delete", name); err != nil {
		return fmt.Errorf("delete tap: %w", err)
	}

	return nil
}
