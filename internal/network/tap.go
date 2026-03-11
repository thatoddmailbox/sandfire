package network

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// CreateTap creates a TAP device and attaches it to the bridge.
// If ownerUID is non-empty, the TAP device is created with that user as owner,
// allowing an unprivileged process (e.g. jailed firecracker) to attach to it.
func (m *Manager) CreateTap(name string, ownerUID string) error {
	log.Printf("Creating TAP device %s", name)

	// Check if device already exists and delete it first (idempotent creation)
	if m.tapExists(name) {
		log.Printf("TAP device %s already exists, removing it first", name)
		if err := m.DeleteTap(name); err != nil {
			log.Printf("Warning: failed to delete existing TAP device %s: %v", name, err)
			// Continue anyway - the create might still work or give a more specific error
		}
	}

	// Create TAP device, optionally with user ownership for jailed access
	args := []string{"tuntap", "add", name, "mode", "tap"}
	if ownerUID != "" {
		args = append(args, "user", ownerUID)
	}
	if err := runCmd("ip", args...); err != nil {
		// Double-check if it exists now (race condition protection)
		if m.tapExists(name) {
			log.Printf("TAP device %s was created by another process, continuing", name)
		} else {
			return fmt.Errorf("create tap: %w", err)
		}
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
		// If the device doesn't exist, don't treat it as an error
		if strings.Contains(err.Error(), "Cannot find device") {
			log.Printf("TAP device %s already deleted", name)
			return nil
		}
		return fmt.Errorf("delete tap: %w", err)
	}

	return nil
}

// tapExists checks if a TAP device with the given name exists
func (m *Manager) tapExists(name string) bool {
	cmd := exec.Command("ip", "link", "show", name)
	return cmd.Run() == nil
}
