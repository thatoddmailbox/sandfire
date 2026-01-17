package vm

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sandfire/internal/db"
	"sandfire/internal/network"
	"sync"
)

type Manager struct {
	dataDir         string
	firecrackerPath string
	jailerUID       int
	jailerGID       int
	useJailer       bool
	networkMgr      *network.Manager

	mu        sync.RWMutex
	processes map[string]*FirecrackerProcess
}

func NewManager(dataDir string, networkMgr *network.Manager) *Manager {
	// Find firecracker binary
	fcPath := "/usr/local/bin/firecracker"
	if p, err := exec.LookPath("firecracker"); err == nil {
		fcPath = p
	}

	return &Manager{
		dataDir:         dataDir,
		firecrackerPath: fcPath,
		jailerUID:       1000,
		jailerGID:       1000,
		useJailer:       false, // Disable jailer for now (networking issues)
		networkMgr:      networkMgr,
		processes:       make(map[string]*FirecrackerProcess),
	}
}

func (m *Manager) PrepareVMDisk(vmID string, img *db.OSImage) error {
	vmDir := filepath.Join(m.dataDir, "vms", vmID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create vm directory: %w", err)
	}

	// Copy rootfs from base image using reflink if supported
	srcRootfs := img.RootfsPath
	dstRootfs := filepath.Join(vmDir, "rootfs.ext4")

	cmd := exec.Command("cp", "--reflink=auto", srcRootfs, dstRootfs)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy rootfs: %w", err)
	}

	// Make writable
	if err := os.Chmod(dstRootfs, 0644); err != nil {
		return fmt.Errorf("chmod rootfs: %w", err)
	}

	return nil
}

// configureVMNetwork writes the network configuration to the VM's rootfs
func (m *Manager) configureVMNetwork(vmID, ipAddress string) error {
	vmRootfs := filepath.Join(m.dataDir, "vms", vmID, "rootfs.ext4")
	mountPoint := filepath.Join(m.dataDir, "mnt", vmID)

	// Create mount point
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	// Mount the rootfs
	cmd := exec.Command("mount", vmRootfs, mountPoint)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mount rootfs: %w", err)
	}

	// Ensure we unmount on exit
	defer func() {
		exec.Command("umount", mountPoint).Run()
		os.Remove(mountPoint)
	}()

	// Write systemd-networkd configuration
	networkDir := filepath.Join(mountPoint, "etc", "systemd", "network")
	if err := os.MkdirAll(networkDir, 0755); err != nil {
		return fmt.Errorf("create network dir: %w", err)
	}

	networkConfig := fmt.Sprintf(`[Match]
Name=eth0

[Network]
Address=%s/24
Gateway=10.20.30.1
DNS=8.8.8.8
`, ipAddress)

	configPath := filepath.Join(networkDir, "20-eth0.network")
	if err := os.WriteFile(configPath, []byte(networkConfig), 0644); err != nil {
		return fmt.Errorf("write network config: %w", err)
	}

	log.Printf("Configured network for VM %s with IP %s", vmID, ipAddress)
	return nil
}

func (m *Manager) StartVM(vm *db.VM, img *db.OSImage) (ipAddress, tapDevice string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[vm.ID]; exists {
		return "", "", fmt.Errorf("VM %s is already running", vm.ID)
	}

	// Allocate IP address
	ipAddress, err = m.networkMgr.AllocateIP()
	if err != nil {
		return "", "", fmt.Errorf("allocate IP: %w", err)
	}

	// Create TAP device
	tapDevice = "tap-" + vm.ID
	if err := m.networkMgr.CreateTap(tapDevice); err != nil {
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("create tap device: %w", err)
	}

	// Setup NAT if internet enabled
	if vm.InternetEnabled {
		if err := m.networkMgr.EnableNAT(); err != nil {
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			return "", "", fmt.Errorf("enable NAT: %w", err)
		}
	}

	// Configure network in VM's rootfs with allocated IP
	if err := m.configureVMNetwork(vm.ID, ipAddress); err != nil {
		m.networkMgr.DeleteTap(tapDevice)
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("configure VM network: %w", err)
	}

	// Paths for kernel and rootfs
	vmRootfs := filepath.Join(m.dataDir, "vms", vm.ID, "rootfs.ext4")
	var proc *FirecrackerProcess
	var kernelPath, rootfsPath string

	if m.useJailer {
		// Jail directory paths
		// Jailer creates jail at {jailBase}/{exec-file-name}/{vm-id}/root/
		jailBase := filepath.Join(m.dataDir, "jails")
		execFileName := filepath.Base(m.firecrackerPath)
		jailRoot := filepath.Join(jailBase, execFileName, vm.ID, "root")

		// Start jailer with firecracker (this creates the jail structure)
		var err error
		proc, err = startJailer(vm.ID, m.firecrackerPath, jailBase, m.jailerUID, m.jailerGID)
		if err != nil {
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			os.RemoveAll(filepath.Join(jailBase, execFileName, vm.ID))
			return "", "", fmt.Errorf("start jailer: %w", err)
		}

		// Wait for socket
		if err := proc.waitForSocket(10 * 1e9); err != nil {
			proc.stop()
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			os.RemoveAll(filepath.Join(jailBase, execFileName, vm.ID))
			return "", "", fmt.Errorf("wait for socket: %w", err)
		}

		// Create /dev/net/tun in jail for TAP device access
		devNetDir := filepath.Join(jailRoot, "dev", "net")
		os.MkdirAll(devNetDir, 0755)
		tunDst := filepath.Join(devNetDir, "tun")
		exec.Command("mknod", tunDst, "c", "10", "200").Run()
		exec.Command("chmod", "0666", tunDst).Run()

		// Copy kernel to jail
		kernelDst := filepath.Join(jailRoot, "kernel")
		cmd := exec.Command("cp", img.KernelPath, kernelDst)
		if err := cmd.Run(); err != nil {
			proc.stop()
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			os.RemoveAll(filepath.Join(jailBase, execFileName, vm.ID))
			return "", "", fmt.Errorf("copy kernel to jail: %w", err)
		}

		// Copy rootfs to jail
		rootfsDst := filepath.Join(jailRoot, "rootfs.ext4")
		cmd = exec.Command("cp", vmRootfs, rootfsDst)
		if err := cmd.Run(); err != nil {
			proc.stop()
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			os.RemoveAll(filepath.Join(jailBase, execFileName, vm.ID))
			return "", "", fmt.Errorf("copy rootfs to jail: %w", err)
		}

		// Change ownership
		exec.Command("chown", fmt.Sprintf("%d:%d", m.jailerUID, m.jailerGID), kernelDst, rootfsDst).Run()

		kernelPath = "/kernel"
		rootfsPath = "/rootfs.ext4"
	} else {
		// Run firecracker directly without jailer
		var err error
		proc, err = startFirecrackerDirect(vm.ID, m.firecrackerPath, m.dataDir)
		if err != nil {
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			return "", "", fmt.Errorf("start firecracker: %w", err)
		}

		// Wait for socket
		if err := proc.waitForSocket(10 * 1e9); err != nil {
			proc.stop()
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			return "", "", fmt.Errorf("wait for socket: %w", err)
		}

		// Use absolute paths since not in chroot
		kernelPath = img.KernelPath
		rootfsPath = vmRootfs
	}

	// Generate MAC address
	mac := generateMAC()

	// Configure firecracker
	config := NewFirecrackerConfig(
		kernelPath,
		rootfsPath,
		vm.VCPUCount,
		vm.RamMB,
		tapDevice,
		mac,
		ipAddress,
	)

	if err := proc.configureVM(config); err != nil {
		proc.stop()
		m.networkMgr.DeleteTap(tapDevice)
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("configure VM: %w", err)
	}

	// Start the VM
	if err := proc.start(); err != nil {
		proc.stop()
		m.networkMgr.DeleteTap(tapDevice)
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("start VM: %w", err)
	}

	m.processes[vm.ID] = proc
	log.Printf("Started VM %s with IP %s on %s", vm.ID, ipAddress, tapDevice)
	log.Printf("Serial output for VM %s logged to: %s/vms/%s/serial.log", vm.ID, m.dataDir, vm.ID)

	return ipAddress, tapDevice, nil
}

func (m *Manager) StopVM(vmID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	proc, exists := m.processes[vmID]
	if !exists {
		return fmt.Errorf("VM %s is not running", vmID)
	}

	if err := proc.stop(); err != nil {
		log.Printf("Warning: error stopping VM %s: %v", vmID, err)
	}

	// Cleanup TAP device
	tapDevice := "tap-" + vmID
	if err := m.networkMgr.DeleteTap(tapDevice); err != nil {
		log.Printf("Warning: error deleting tap device %s: %v", tapDevice, err)
	}

	// Clean up runtime directories
	if m.useJailer {
		jailBase := filepath.Join(m.dataDir, "jails")
		execFileName := filepath.Base(m.firecrackerPath)
		os.RemoveAll(filepath.Join(jailBase, execFileName, vmID))
	} else {
		socketDir := filepath.Join(m.dataDir, "run", vmID)
		os.RemoveAll(socketDir)
	}

	delete(m.processes, vmID)
	log.Printf("Stopped VM %s", vmID)

	return nil
}

func (m *Manager) CleanupVM(vmID string) error {
	vmDir := filepath.Join(m.dataDir, "vms", vmID)
	return os.RemoveAll(vmDir)
}

func (m *Manager) StopAllVMs() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for vmID, proc := range m.processes {
		log.Printf("Stopping VM %s...", vmID)
		if err := proc.stop(); err != nil {
			log.Printf("Warning: error stopping VM %s: %v", vmID, err)
		}
		tapDevice := "tap-" + vmID
		m.networkMgr.DeleteTap(tapDevice)
	}
	m.processes = make(map[string]*FirecrackerProcess)
}

func (m *Manager) IsRunning(vmID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.processes[vmID]
	return exists
}

func generateMAC() string {
	buf := make([]byte, 6)
	rand.Read(buf)
	// Set local bit, clear multicast bit
	buf[0] = (buf[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
}
