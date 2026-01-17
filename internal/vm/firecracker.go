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

	return nil
}

func startJailer(vmID, firecrackerPath, jailBase string, uid, gid int) (*FirecrackerProcess, error) {
	// Jailer creates jail at {chroot-base-dir}/{exec-file-name}/{vm-id}/root/
	execFileName := filepath.Base(firecrackerPath)
	jailPath := filepath.Join(jailBase, execFileName, vmID, "root")
	socketPath := filepath.Join(jailPath, "run", "firecracker.socket")

	// Clean up any existing jail
	os.RemoveAll(filepath.Join(jailBase, execFileName, vmID))

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
		Setsid: true, // Create new session
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start jailer: %w", err)
	}

	proc := &FirecrackerProcess{
		VMID:       vmID,
		Cmd:        cmd,
		SocketPath: socketPath,
		JailPath:   jailPath,
	}

	// Start waiting for process exit in background
	proc.startWaiter()

	return proc, nil
}

func startFirecrackerDirect(vmID, firecrackerPath, dataDir string) (*FirecrackerProcess, error) {
	// Create socket directory
	socketDir := filepath.Join(dataDir, "run", vmID)
	os.MkdirAll(socketDir, 0755)
	socketPath := filepath.Join(socketDir, "firecracker.socket")

	// Remove existing socket
	os.Remove(socketPath)

	cmd := exec.Command(firecrackerPath,
		"--api-sock", socketPath,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	proc := &FirecrackerProcess{
		VMID:       vmID,
		Cmd:        cmd,
		SocketPath: socketPath,
		JailPath:   socketDir,
	}

	// Start waiting for process exit in background
	proc.startWaiter()

	return proc, nil
}
