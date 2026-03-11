package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type FirecrackerProcess struct {
	VMID       string
	Cmd        *exec.Cmd
	SocketPath string
	JailPath   string
	SerialLog  *os.File
	waitDone   chan struct{}
	waitErr    error
}

func (p *FirecrackerProcess) startWaiter() {
	p.waitDone = make(chan struct{})
	go func() {
		p.waitErr = p.Cmd.Wait()
		close(p.waitDone)
	}()
}

func (p *FirecrackerProcess) waitForSocket(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Check if process exited early
		select {
		case <-p.waitDone:
			if p.waitErr != nil {
				return fmt.Errorf("jailer process exited: %w", p.waitErr)
			}
			return fmt.Errorf("jailer process exited unexpectedly")
		default:
		}

		if _, err := os.Stat(p.SocketPath); err == nil {
			// Socket exists, try to connect
			conn, err := net.DialTimeout("unix", p.SocketPath, time.Second)
			if err == nil {
				conn.Close()
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for socket %s", p.SocketPath)
}

func (p *FirecrackerProcess) configureVM(config *FirecrackerConfig) error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", p.SocketPath)
			},
		},
		Timeout: 30 * time.Second,
	}

	// Configure boot source
	if err := p.putConfig(client, "/boot-source", config.BootSource); err != nil {
		return fmt.Errorf("configure boot source: %w", err)
	}

	// Configure drives
	for _, drive := range config.Drives {
		if err := p.putConfig(client, "/drives/"+drive.DriveID, drive); err != nil {
			return fmt.Errorf("configure drive %s: %w", drive.DriveID, err)
		}
	}

	// Configure network interfaces
	for _, iface := range config.NetworkInterfaces {
		if err := p.putConfig(client, "/network-interfaces/"+iface.IfaceID, iface); err != nil {
			return fmt.Errorf("configure network interface %s: %w", iface.IfaceID, err)
		}
	}

	// Configure machine
	if err := p.putConfig(client, "/machine-config", config.MachineConfig); err != nil {
		return fmt.Errorf("configure machine: %w", err)
	}

	// Enable entropy device for guest random number generation
	entropyConfig := map[string]int64{"rate_limiter": 0}
	if err := p.putConfig(client, "/entropy", entropyConfig); err != nil {
		// Not fatal - older Firecracker versions may not support this
		// Log warning but continue
	}

	return nil
}

func (p *FirecrackerProcess) putConfig(client *http.Client, path string, data interface{}) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", "http://localhost"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("firecracker API error: %s", errResp["fault_message"])
	}

	return nil
}

// configureMMDS sets up the MMDS service with VM metadata
func (p *FirecrackerProcess) configureMMDS(networkIfaceID, vmID, vmName, domain string, claudeCredentials, vmContext json.RawMessage) error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", p.SocketPath)
			},
		},
		Timeout: 30 * time.Second,
	}

	// Configure MMDS to use the network interface (must be done before boot)
	mmdsConfig := MMDSConfig{
		NetworkInterfaces: []string{networkIfaceID},
		Version:           "V2",
	}
	if err := p.putConfig(client, "/mmds/config", mmdsConfig); err != nil {
		return fmt.Errorf("configure MMDS: %w", err)
	}

	// Set the metadata
	metadata := MMDSMetadata{
		Sandfire: SandfireMetadata{
			VMID:              vmID,
			VMName:            vmName,
			Domain:            domain,
			ClaudeCredentials: claudeCredentials,
			Context:           vmContext,
		},
	}
	if err := p.putConfig(client, "/mmds", metadata); err != nil {
		return fmt.Errorf("set MMDS metadata: %w", err)
	}

	return nil
}

func (p *FirecrackerProcess) start() error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", p.SocketPath)
			},
		},
		Timeout: 30 * time.Second,
	}

	action := map[string]string{"action_type": "InstanceStart"}
	body, _ := json.Marshal(action)

	req, err := http.NewRequest("PUT", "http://localhost/actions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("failed to start instance: %s", errResp["fault_message"])
	}

	return nil
}

func (p *FirecrackerProcess) stop() error {
	if p.Cmd != nil && p.Cmd.Process != nil {
		// Check if already exited
		select {
		case <-p.waitDone:
			// Process already exited, just close the log
			if p.SerialLog != nil {
				p.SerialLog.Close()
			}
			return nil
		default:
		}

		// Send SIGTERM first
		p.Cmd.Process.Signal(syscall.SIGTERM)

		// Wait a bit for graceful shutdown
		select {
		case <-p.waitDone:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill if still running
			p.Cmd.Process.Kill()
			<-p.waitDone
		}
	}

	// Close serial log file
	if p.SerialLog != nil {
		p.SerialLog.Close()
	}

	return nil
}

// jailConfig holds the paths needed to set up bind mounts in the jail.
type jailConfig struct {
	KernelPath string // Host path to kernel image
	RootfsPath string // Host path to VM rootfs
}

// startJailer prepares a jail with bind-mounted resources, then starts the
// official Firecracker jailer binary.
//
// The flow is:
//  1. Pre-create the chroot dir that the jailer expects
//  2. Bind mount kernel and rootfs into the chroot dir
//  3. Start the jailer (it copies the firecracker binary, creates device nodes,
//     pivots root, drops privileges, and execs firecracker)
//  4. The caller waits for the socket and configures the VM
func startJailer(vmID, firecrackerPath, jailBase, dataDir string, uid, gid int, jail jailConfig) (*FirecrackerProcess, error) {
	execFileName := filepath.Base(firecrackerPath)
	jailDir := filepath.Join(jailBase, execFileName, vmID)
	chrootDir := filepath.Join(jailDir, "root")
	socketPath := filepath.Join(chrootDir, "run", "firecracker.socket")

	// Clean up any existing jail (unmount first, then remove)
	cleanupJail(jailDir, chrootDir)

	// Create chroot dir before the jailer runs. The jailer docs say
	// "Nothing is done if the path already exists."
	if err := os.MkdirAll(chrootDir, 0755); err != nil {
		return nil, fmt.Errorf("create chroot dir: %w", err)
	}

	// The jailer bind-mounts chrootDir on itself and then pivot_roots into it.
	// If the underlying filesystem is mounted with "nodev" (common for /home),
	// device nodes created by the jailer (/dev/kvm, /dev/net/tun) won't work.
	// Fix: bind-mount chrootDir on itself and remount with "dev" enabled,
	// so the jailer's subsequent bind mount inherits the "dev" option.
	if err := exec.Command("mount", "--bind", chrootDir, chrootDir).Run(); err != nil {
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("bind mount chroot dir: %w", err)
	}
	if err := exec.Command("mount", "-o", "remount,dev", chrootDir).Run(); err != nil {
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("remount chroot dir with dev: %w", err)
	}

	// Bind mount kernel into jail
	if err := bindMount(jail.KernelPath, filepath.Join(chrootDir, "vmlinux")); err != nil {
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("bind mount kernel: %w", err)
	}

	// Bind mount rootfs into jail
	if err := bindMount(jail.RootfsPath, filepath.Join(chrootDir, "rootfs.ext4")); err != nil {
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("bind mount rootfs: %w", err)
	}

	// Set ownership so the jailed firecracker can read/write these files
	uidGid := fmt.Sprintf("%d:%d", uid, gid)
	exec.Command("chown", uidGid,
		filepath.Join(chrootDir, "vmlinux"),
		filepath.Join(chrootDir, "rootfs.ext4"),
	).Run()

	// Create serial log file
	logPath := filepath.Join(dataDir, "vms", vmID, "serial.log")
	serialLog, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("create serial log: %w", err)
	}

	cmd := exec.Command("jailer",
		"--id", vmID,
		"--exec-file", firecrackerPath,
		"--uid", fmt.Sprintf("%d", uid),
		"--gid", fmt.Sprintf("%d", gid),
		"--chroot-base-dir", jailBase,
		"--",
		"--api-sock", "/run/firecracker.socket",
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Capture serial output (jailer does NOT use --daemonize so we get stdout/stderr)
	cmd.Stdout = serialLog
	cmd.Stderr = serialLog

	if err := cmd.Start(); err != nil {
		serialLog.Close()
		cleanupJail(jailDir, chrootDir)
		return nil, fmt.Errorf("start jailer: %w", err)
	}

	proc := &FirecrackerProcess{
		VMID:       vmID,
		Cmd:        cmd,
		SocketPath: socketPath,
		JailPath:   jailDir,
		SerialLog:  serialLog,
	}

	proc.startWaiter()

	return proc, nil
}

// bindMount creates a bind mount from src to dst. The dst file is created
// (as an empty file) if it does not exist.
func bindMount(src, dst string) error {
	// Create empty target file for the bind mount
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create mount target %s: %w", dst, err)
	}
	f.Close()

	if err := exec.Command("mount", "--bind", src, dst).Run(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("mount --bind %s %s: %w", src, dst, err)
	}
	return nil
}

// cleanupJail unmounts bind mounts inside the chroot dir and removes the
// entire jail directory.
func cleanupJail(jailDir, chrootDir string) {
	// Unmount any bind mounts (ignore errors — they may not be mounted)
	exec.Command("umount", filepath.Join(chrootDir, "vmlinux")).Run()
	exec.Command("umount", filepath.Join(chrootDir, "rootfs.ext4")).Run()
	// Unmount the chroot dir itself (we bind-mounted it for nodev workaround)
	exec.Command("umount", chrootDir).Run()
	os.RemoveAll(jailDir)
}

func startFirecrackerDirect(vmID, firecrackerPath, dataDir string) (*FirecrackerProcess, error) {
	// Create socket directory
	socketDir := filepath.Join(dataDir, "run", vmID)
	os.MkdirAll(socketDir, 0755)
	socketPath := filepath.Join(socketDir, "firecracker.socket")

	// Remove existing socket
	os.Remove(socketPath)

	// Create serial log file (truncate if exists)
	logPath := filepath.Join(dataDir, "vms", vmID, "serial.log")
	serialLog, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("create serial log: %w", err)
	}

	cmd := exec.Command(firecrackerPath,
		"--api-sock", socketPath,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	// Redirect serial output to log file
	cmd.Stdout = serialLog
	cmd.Stderr = serialLog

	if err := cmd.Start(); err != nil {
		serialLog.Close()
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	proc := &FirecrackerProcess{
		VMID:       vmID,
		Cmd:        cmd,
		SocketPath: socketPath,
		JailPath:   socketDir,
		SerialLog:  serialLog,
	}

	// Start waiting for process exit in background
	proc.startWaiter()

	return proc, nil
}
