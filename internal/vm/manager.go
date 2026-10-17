package vm

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sandfire/internal/db"
	"sandfire/internal/network"
	"sandfire/internal/storage"
)

type Manager struct {
	dataDir         string
	firecrackerPath string
	jailerUID       int
	jailerGID       int
	useJailer       bool
	networkMgr      *network.Manager
	baseDomain      string

	mu        sync.RWMutex
	processes map[string]*FirecrackerProcess
	// diskBusy holds IDs of stopped VMs whose disk is being modified, so they
	// can't be started until the operation finishes.
	diskBusy map[string]bool
}

func NewManager(dataDir, baseDomain string, networkMgr *network.Manager) *Manager {
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
		useJailer:       true,
		networkMgr:      networkMgr,
		baseDomain:      baseDomain,
		processes:       make(map[string]*FirecrackerProcess),
		diskBusy:        make(map[string]bool),
	}
}

// CleanupOrphanedResources cleans up resources that may have been left behind
// from a previous unclean shutdown (e.g., SIGKILL). This should be called
// before attempting to restore VMs on startup.
func (m *Manager) CleanupOrphanedResources() {
	log.Println("Checking for orphaned resources from previous run...")

	// 1. Kill orphaned firecracker processes
	m.killOrphanedFirecrackerProcesses()

	// 2. Clean up orphaned TAP devices
	m.cleanOrphanedTapDevices()

	// 3. Clean up orphaned socket directories
	m.cleanOrphanedSocketDirs()

	// 4. Clean up orphaned jail directories
	m.cleanOrphanedJails()

	log.Println("Orphaned resource cleanup complete")
}

// killOrphanedFirecrackerProcesses finds and kills any firecracker processes
// that are still running from a previous session
func (m *Manager) killOrphanedFirecrackerProcesses() {
	// Find all firecracker processes
	cmd := exec.Command("pgrep", "-f", "firecracker")
	output, err := cmd.Output()
	if err != nil {
		// pgrep exits with code 1 if no matches found, which is fine
		return
	}

	pids := strings.Fields(string(output))
	if len(pids) == 0 {
		return
	}

	log.Printf("Found %d orphaned firecracker process(es), killing...", len(pids))

	for _, pidStr := range pids {
		pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
		if err != nil {
			continue
		}

		// Check if this process has our data directory in its command line
		// to avoid killing unrelated firecracker processes
		cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
		cmdline, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		// Check if this firecracker process is using our data directory
		if !strings.Contains(string(cmdline), m.dataDir) {
			log.Printf("Skipping firecracker process %d (not ours)", pid)
			continue
		}

		log.Printf("Killing orphaned firecracker process %d", pid)

		// Send SIGTERM first
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}

		proc.Signal(syscall.SIGTERM)

		// Give it a second to exit gracefully
		// We don't wait long since we're doing cleanup
		time.Sleep(500 * time.Millisecond)

		// Force kill if still running
		if m.processExists(pid) {
			log.Printf("Force killing firecracker process %d", pid)
			proc.Kill()
		}
	}
}

// processExists checks if a process with the given PID is still running
func (m *Manager) processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// cleanOrphanedTapDevices removes TAP devices that were created by sandfire
// but not properly cleaned up
func (m *Manager) cleanOrphanedTapDevices() {
	// Get all interfaces attached to the sandfire bridge
	cmd := exec.Command("ip", "link", "show", "master", network.BridgeName)
	output, err := cmd.Output()
	if err != nil {
		// Bridge might not exist yet
		return
	}

	// Parse the output to find tap-* devices
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		// Look for lines like "4: tap-abc123@sandfire0: ..."
		if strings.Contains(line, "tap-") {
			// Extract device name
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			// Device name is in format "name:" or "name@master:"
			devName := parts[1]
			devName = strings.TrimSuffix(devName, ":")
			if idx := strings.Index(devName, "@"); idx > 0 {
				devName = devName[:idx]
			}

			if strings.HasPrefix(devName, "tap-") {
				log.Printf("Cleaning up orphaned TAP device: %s", devName)
				m.networkMgr.DeleteTap(devName)
			}
		}
	}
}

// cleanOrphanedSocketDirs removes socket directories from previous runs
func (m *Manager) cleanOrphanedSocketDirs() {
	runDir := filepath.Join(m.dataDir, "run")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		// run directory might not exist
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			socketDir := filepath.Join(runDir, entry.Name())
			log.Printf("Cleaning up orphaned socket directory: %s", socketDir)
			os.RemoveAll(socketDir)
		}
	}
}

// cleanOrphanedJails removes jail directories left behind from a previous run.
func (m *Manager) cleanOrphanedJails() {
	execFileName := filepath.Base(m.firecrackerPath)
	jailParent := filepath.Join(m.dataDir, "jails", execFileName)
	entries, err := os.ReadDir(jailParent)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			vmID := entry.Name()
			log.Printf("Cleaning up orphaned jail: %s", vmID)
			m.cleanupJailDir(vmID)
		}
	}
}

// cleanupJailDir unmounts bind mounts and removes the jail directory for a VM.
func (m *Manager) cleanupJailDir(vmID string) {
	jailBase := filepath.Join(m.dataDir, "jails")
	execFileName := filepath.Base(m.firecrackerPath)
	jailDir := filepath.Join(jailBase, execFileName, vmID)
	chrootDir := filepath.Join(jailDir, "root")
	cleanupJail(jailDir, chrootDir)
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

func (m *Manager) StartVM(vm *db.VM, img *db.OSImage, vmContext json.RawMessage) (ipAddress, tapDevice string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[vm.ID]; exists {
		return "", "", fmt.Errorf("VM %s is already running", vm.ID)
	}
	if m.diskBusy[vm.ID] {
		return "", "", fmt.Errorf("VM %s: %w", vm.ID, ErrDiskBusy)
	}

	// Allocate IP address
	ipAddress, err = m.networkMgr.AllocateIP()
	if err != nil {
		return "", "", fmt.Errorf("allocate IP: %w", err)
	}

	// Create TAP device. When using the jailer, set the TAP owner to the
	// jail UID so the unprivileged firecracker process can attach to it.
	tapDevice = "tap-" + vm.ID
	tapOwner := ""
	if m.useJailer {
		tapOwner = fmt.Sprintf("%d", m.jailerUID)
	}
	if err := m.networkMgr.CreateTap(tapDevice, tapOwner); err != nil {
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

	// Paths for kernel and rootfs
	vmRootfs := filepath.Join(m.dataDir, "vms", vm.ID, "rootfs.ext4")
	var proc *FirecrackerProcess
	var kernelPath, rootfsPath string

	if m.useJailer {
		jailBase := filepath.Join(m.dataDir, "jails")

		jail := jailConfig{
			KernelPath: img.KernelPath,
			RootfsPath: vmRootfs,
		}

		var err error
		proc, err = startJailer(vm.ID, m.firecrackerPath, jailBase, m.dataDir, m.jailerUID, m.jailerGID, jail)
		if err != nil {
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			return "", "", fmt.Errorf("start jailer: %w", err)
		}

		if err := proc.waitForSocket(10 * time.Second); err != nil {
			proc.stop()
			m.cleanupJailDir(vm.ID)
			m.networkMgr.DeleteTap(tapDevice)
			m.networkMgr.ReleaseIP(ipAddress)
			return "", "", fmt.Errorf("wait for socket: %w", err)
		}

		// Paths are relative to the chroot
		kernelPath = "/vmlinux"
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
		if err := proc.waitForSocket(10 * time.Second); err != nil {
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

	// Configure MMDS with VM metadata (network interface ID is "eth0")
	// Include Claude credentials if available on the host
	claudeCredentials := loadClaudeConfig()
	vmDomain := m.getVMDomain(vm.ID)
	if err := proc.configureMMDS("eth0", vm.ID, vm.Name, vmDomain, claudeCredentials, vmContext); err != nil {
		proc.stop()
		m.networkMgr.DeleteTap(tapDevice)
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("configure MMDS: %w", err)
	}

	// Start the VM
	if err := proc.start(); err != nil {
		proc.stop()
		m.networkMgr.DeleteTap(tapDevice)
		m.networkMgr.ReleaseIP(ipAddress)
		return "", "", fmt.Errorf("start VM: %w", err)
	}

	proc.IPAddress = ipAddress
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

	m.networkMgr.ReleaseIP(proc.IPAddress)

	// Clean up runtime directories
	if m.useJailer {
		m.cleanupJailDir(vmID)
	} else {
		socketDir := filepath.Join(m.dataDir, "run", vmID)
		os.RemoveAll(socketDir)
	}

	delete(m.processes, vmID)
	log.Printf("Stopped VM %s", vmID)

	return nil
}

func (m *Manager) CleanupVM(vmID string) error {
	unlock, err := m.lockDisk(vmID)
	if err != nil {
		return err
	}
	defer unlock()

	vmDir := filepath.Join(m.dataDir, "vms", vmID)
	return os.RemoveAll(vmDir)
}

func (m *Manager) vmRootfsPath(vmID string) string {
	return filepath.Join(m.dataDir, "vms", vmID, "rootfs.ext4")
}

var (
	// ErrVMRunning is returned when a disk operation is attempted on a running VM.
	ErrVMRunning = errors.New("VM is running")
	// ErrDiskBusy is returned when another operation is already using the VM's disk.
	ErrDiskBusy = errors.New("VM disk is already being modified")
)

// lockDisk marks a stopped VM's disk as busy so the VM can't be started (or
// have another disk operation run) until the returned function is called.
func (m *Manager) lockDisk(vmID string) (unlock func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[vmID]; exists {
		return nil, fmt.Errorf("VM %s: %w", vmID, ErrVMRunning)
	}
	if m.diskBusy[vmID] {
		return nil, fmt.Errorf("VM %s: %w", vmID, ErrDiskBusy)
	}
	m.diskBusy[vmID] = true

	return func() {
		m.mu.Lock()
		delete(m.diskBusy, vmID)
		m.mu.Unlock()
	}, nil
}

// VMDiskSizeGB returns the size of the VM's rootfs image file in GiB, rounded up.
func (m *Manager) VMDiskSizeGB(vmID string) (int, error) {
	st, err := os.Stat(m.vmRootfsPath(vmID))
	if err != nil {
		return 0, err
	}
	return int((st.Size() + storage.GiB - 1) / storage.GiB), nil
}

// InspectVMDisk reports the size, usage and minimum size of a stopped VM's disk.
func (m *Manager) InspectVMDisk(vmID string) (*storage.Ext4Info, error) {
	unlock, err := m.lockDisk(vmID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	return storage.InspectExt4(m.vmRootfsPath(vmID))
}

// ResizeVMDisk grows or shrinks a stopped VM's disk to sizeBytes.
func (m *Manager) ResizeVMDisk(vmID string, sizeBytes int64) error {
	unlock, err := m.lockDisk(vmID)
	if err != nil {
		return err
	}
	defer unlock()

	if err := storage.ResizeExt4(m.vmRootfsPath(vmID), sizeBytes); err != nil {
		return err
	}
	log.Printf("Resized disk for VM %s to %d bytes", vmID, sizeBytes)
	return nil
}

// ResetVMDisk replaces the VM's rootfs with a fresh copy from the base OS image,
// keeping the disk's current size if it is larger than the image's.
// The VM must be stopped before calling this method.
func (m *Manager) ResetVMDisk(vmID string, img *db.OSImage) error {
	unlock, err := m.lockDisk(vmID)
	if err != nil {
		return err
	}
	defer unlock()

	dstRootfs := m.vmRootfsPath(vmID)

	var oldSize int64
	if st, err := os.Stat(dstRootfs); err == nil {
		oldSize = st.Size()
	}

	// Remove the existing rootfs
	if err := os.Remove(dstRootfs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old rootfs: %w", err)
	}

	// Copy fresh rootfs from base image using reflink if supported
	cmd := exec.Command("cp", "--reflink=auto", img.RootfsPath, dstRootfs)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy rootfs: %w", err)
	}

	// Make writable
	if err := os.Chmod(dstRootfs, 0644); err != nil {
		return fmt.Errorf("chmod rootfs: %w", err)
	}

	if st, err := os.Stat(dstRootfs); err == nil && oldSize > st.Size() {
		if err := storage.ResizeExt4(dstRootfs, oldSize); err != nil {
			return fmt.Errorf("restore disk size: %w", err)
		}
	}

	log.Printf("Reset disk for VM %s from image %s", vmID, img.ID)
	return nil
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
		m.networkMgr.ReleaseIP(proc.IPAddress)
		if m.useJailer {
			m.cleanupJailDir(vmID)
		} else {
			socketDir := filepath.Join(m.dataDir, "run", vmID)
			os.RemoveAll(socketDir)
		}
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

// loadClaudeConfig loads Claude Code credentials from the running user's home directory.
// Returns nil for either if not found (not an error - VMs can run without them).
func loadClaudeConfig() (credentials json.RawMessage) {
	var homeDir string

	// If running with sudo, use the original user's home directory
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		sudoUserInfo, err := user.Lookup(sudoUser)
		if err == nil {
			homeDir = sudoUserInfo.HomeDir
		}
	}

	// Fall back to current user's home if SUDO_USER not available
	if homeDir == "" {
		currentUser, err := user.Current()
		if err != nil {
			log.Printf("Could not determine current user for Claude config: %v", err)
			return nil
		}
		homeDir = currentUser.HomeDir
	}

	claudeDir := filepath.Join(homeDir, ".claude")

	// Load credentials
	credPath := filepath.Join(claudeDir, ".credentials.json")
	if data, err := os.ReadFile(credPath); err == nil && json.Valid(data) {
		credentials = json.RawMessage(data)
		log.Printf("Loaded Claude credentials from %s", credPath)
	} else if err != nil && !os.IsNotExist(err) {
		log.Printf("Error reading Claude credentials: %v", err)
	}

	return credentials
}

// getVMDomain returns the full domain for a VM, or empty string if no base domain is configured.
func (m *Manager) getVMDomain(vmID string) string {
	if m.baseDomain == "" {
		return ""
	}
	return vmID + "." + m.baseDomain
}
