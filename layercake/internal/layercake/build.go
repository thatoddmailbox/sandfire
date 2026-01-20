package layercake

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Builder handles building layers
type Builder struct {
	graph    *LayerGraph
	workDir  string
	verbose  bool
}

// NewBuilder creates a new builder
func NewBuilder(graph *LayerGraph, verbose bool) *Builder {
	return &Builder{
		graph:   graph,
		workDir: "/tmp/layercake-build",
		verbose: verbose,
	}
}

// Build builds a layer and its dependencies if needed
func (b *Builder) Build(layerID string, force bool) error {
	order, err := b.graph.GetBuildOrder(layerID)
	if err != nil {
		return err
	}

	for _, layer := range order {
		status, err := GetBuildStatus(layer, b.graph)
		if err != nil {
			return fmt.Errorf("failed to get build status for %s: %w", layer.ID, err)
		}

		needsBuild := !status.Built || status.Stale || (force && layer.ID == layerID)
		if !needsBuild {
			fmt.Printf("Layer %s is up to date\n", layer.ID)
			continue
		}

		if status.Stale && status.Built {
			fmt.Printf("Layer %s is stale: %s\n", layer.ID, status.StaleReason)
		}

		if err := b.buildLayer(layer); err != nil {
			return fmt.Errorf("failed to build layer %s: %w", layer.ID, err)
		}
	}

	return nil
}

// BuildAll builds all layers in dependency order
func (b *Builder) BuildAll(force bool) error {
	order := b.graph.TopologicalSort()
	for _, layer := range order {
		status, err := GetBuildStatus(layer, b.graph)
		if err != nil {
			return fmt.Errorf("failed to get build status for %s: %w", layer.ID, err)
		}

		needsBuild := !status.Built || status.Stale || force
		if !needsBuild {
			fmt.Printf("Layer %s is up to date\n", layer.ID)
			continue
		}

		if status.Stale && status.Built {
			fmt.Printf("Layer %s is stale: %s\n", layer.ID, status.StaleReason)
		}

		if err := b.buildLayer(layer); err != nil {
			return fmt.Errorf("failed to build layer %s: %w", layer.ID, err)
		}
	}
	return nil
}

// BuildCascade force rebuilds a layer and all its descendants
func (b *Builder) BuildCascade(layerID string) error {
	layer := b.graph.Get(layerID)
	if layer == nil {
		return fmt.Errorf("unknown layer: %s", layerID)
	}

	// First build the target layer (and its parents if needed)
	if err := b.Build(layerID, true); err != nil {
		return err
	}

	// Then rebuild all descendants
	descendants := b.graph.GetDescendants(layerID)
	for _, desc := range descendants {
		fmt.Printf("Rebuilding descendant %s\n", desc.ID)
		if err := b.buildLayer(desc); err != nil {
			return fmt.Errorf("failed to rebuild descendant %s: %w", desc.ID, err)
		}
	}

	return nil
}

// buildLayer builds a single layer
func (b *Builder) buildLayer(layer *Layer) error {
	fmt.Printf("Building layer %s...\n", layer.ID)

	// Create work directory
	workDir := filepath.Join(b.workDir, layer.ID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	if layer.IsBase() {
		return b.buildBaseLayer(layer, workDir)
	}
	return b.buildDerivativeLayer(layer, workDir)
}

// buildBaseLayer builds a base layer using debootstrap
func (b *Builder) buildBaseLayer(layer *Layer, workDir string) error {
	// Download kernel if needed
	if _, err := os.Stat(layer.KernelPath()); os.IsNotExist(err) {
		fmt.Printf("Downloading kernel from %s...\n", layer.KernelURL)
		if err := downloadFile(layer.KernelURL, layer.KernelPath()); err != nil {
			return fmt.Errorf("failed to download kernel: %w", err)
		}
	}

	// Run debootstrap
	rootfsDir := filepath.Join(workDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		return err
	}

	fmt.Println("Running debootstrap (this may take a while)...")
	cmd := exec.Command("debootstrap",
		"--arch=amd64",
		"--variant=minbase",
		"--include=systemd,systemd-sysv,udev,iproute2,iputils-ping,openssh-server,sudo,curl,ca-certificates,passwd,busybox-static,python3,htop,nano,less,git",
		"noble",
		rootfsDir,
		"http://archive.ubuntu.com/ubuntu/",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("debootstrap failed: %w", err)
	}

	// Run layer.sh in chroot
	fmt.Println("Running layer.sh...")
	if err := b.runInChroot(layer, rootfsDir); err != nil {
		return fmt.Errorf("layer.sh failed: %w", err)
	}

	// Clean up apt cache
	os.RemoveAll(filepath.Join(rootfsDir, "var/cache/apt/archives"))
	os.RemoveAll(filepath.Join(rootfsDir, "var/lib/apt/lists"))

	// Create ext4 image
	fmt.Println("Creating ext4 image...")
	if err := b.createExt4Image(layer, rootfsDir); err != nil {
		return fmt.Errorf("failed to create ext4 image: %w", err)
	}

	// Write build hash
	if err := WriteBuildHash(layer); err != nil {
		return fmt.Errorf("failed to write build hash: %w", err)
	}

	// Get disk usage for success message
	if used, total, err := getRootfsUsage(layer.RootfsPath()); err == nil {
		fmt.Printf("Layer %s built successfully (used %s out of %s)\n", layer.ID, formatBytes(used), formatBytes(total))
	} else {
		fmt.Printf("Layer %s built successfully\n", layer.ID)
	}
	return nil
}

// buildDerivativeLayer builds a layer derived from a parent
func (b *Builder) buildDerivativeLayer(layer *Layer, workDir string) error {
	parent := b.graph.Get(layer.Parent)
	if parent == nil {
		return fmt.Errorf("parent layer %s not found", layer.Parent)
	}

	// Check parent is built
	if _, err := os.Stat(parent.RootfsPath()); os.IsNotExist(err) {
		return fmt.Errorf("parent layer %s is not built", layer.Parent)
	}

	// Clean up any stale mounts from interrupted builds BEFORE copying
	// (copying to a mounted file will fail)
	if err := unmountIfMounted(layer.RootfsPath()); err != nil {
		return fmt.Errorf("failed to clean up stale mounts: %w", err)
	}

	// Copy parent rootfs
	fmt.Printf("Copying rootfs from parent %s...\n", layer.Parent)
	if err := copyFile(parent.RootfsPath(), layer.RootfsPath()); err != nil {
		return fmt.Errorf("failed to copy parent rootfs: %w", err)
	}

	// Resize if needed
	if layer.RootfsSizeMB > parent.RootfsSizeMB {
		fmt.Printf("Resizing rootfs to %d MB...\n", layer.RootfsSizeMB)
		if err := resizeExt4(layer.RootfsPath(), layer.RootfsSizeMB); err != nil {
			return fmt.Errorf("failed to resize rootfs: %w", err)
		}
	}

	// Mount rootfs
	mountDir := filepath.Join(workDir, "mnt")
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return err
	}

	fmt.Println("Mounting rootfs...")
	cmd := exec.Command("mount", "-o", "loop", layer.RootfsPath(), mountDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount rootfs: %w", err)
	}
	defer func() {
		exec.Command("umount", mountDir).Run()
	}()

	// Run layer.sh in chroot
	fmt.Println("Running layer.sh...")
	if err := b.runInChroot(layer, mountDir); err != nil {
		exec.Command("umount", mountDir).Run()
		return fmt.Errorf("layer.sh failed: %w", err)
	}

	// Unmount
	fmt.Println("Unmounting rootfs...")
	if err := exec.Command("umount", mountDir).Run(); err != nil {
		return fmt.Errorf("failed to unmount rootfs: %w", err)
	}

	// Write build hash
	if err := WriteBuildHash(layer); err != nil {
		return fmt.Errorf("failed to write build hash: %w", err)
	}

	// Get disk usage for success message
	if used, total, err := getRootfsUsage(layer.RootfsPath()); err == nil {
		fmt.Printf("Layer %s built successfully (used %s out of %s)\n", layer.ID, formatBytes(used), formatBytes(total))
	} else {
		fmt.Printf("Layer %s built successfully\n", layer.ID)
	}
	return nil
}

// runInChroot runs layer.sh inside a chroot
func (b *Builder) runInChroot(layer *Layer, rootfsDir string) error {
	// Copy layer.sh into the chroot
	scriptDest := filepath.Join(rootfsDir, "tmp", "layer.sh")
	if err := copyFile(layer.ScriptPath(), scriptDest); err != nil {
		return fmt.Errorf("failed to copy layer.sh: %w", err)
	}
	if err := os.Chmod(scriptDest, 0755); err != nil {
		return err
	}

	// Set up SSH agent forwarding if available
	var sshCleanup func()
	sshAgentSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAgentSock != "" {
		cleanup, chrootSockPath, err := b.setupSSHAgentForwarding(rootfsDir, sshAgentSock)
		if err != nil {
			fmt.Printf("Warning: failed to set up SSH agent forwarding: %v\n", err)
		} else {
			sshCleanup = cleanup
			sshAgentSock = chrootSockPath
			fmt.Println("SSH agent forwarding enabled")
		}
	} else if os.Getuid() == 0 {
		fmt.Println("Note: SSH_AUTH_SOCK not set. For SSH agent forwarding, use: sudo -E layercake build ...")
	}
	defer func() {
		if sshCleanup != nil {
			sshCleanup()
		}
	}()

	// Run in chroot
	cmd := exec.Command("chroot", rootfsDir, "/bin/bash", "/tmp/layer.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DEBIAN_FRONTEND=noninteractive",
	}
	if sshAgentSock != "" {
		cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+sshAgentSock)
	}
	if err := cmd.Run(); err != nil {
		return err
	}

	// Clean up
	os.Remove(scriptDest)
	return nil
}

// setupSSHAgentForwarding bind-mounts the SSH agent socket into the chroot
// Returns a cleanup function, the path to the socket inside the chroot, and any error
func (b *Builder) setupSSHAgentForwarding(rootfsDir, hostSockPath string) (cleanup func(), chrootSockPath string, err error) {
	// Verify the host socket exists
	if _, err := os.Stat(hostSockPath); err != nil {
		return nil, "", fmt.Errorf("SSH agent socket not found: %w", err)
	}

	// Create directory for the socket inside chroot
	sshDir := filepath.Join(rootfsDir, "tmp", "ssh-agent")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, "", fmt.Errorf("failed to create ssh-agent directory: %w", err)
	}

	// The socket path inside the chroot
	chrootSockPath = "/tmp/ssh-agent/agent.sock"
	hostMountPoint := filepath.Join(rootfsDir, chrootSockPath)

	// Create an empty file to use as the bind mount target
	f, err := os.Create(hostMountPoint)
	if err != nil {
		os.RemoveAll(sshDir)
		return nil, "", fmt.Errorf("failed to create mount point: %w", err)
	}
	f.Close()

	// Bind mount the socket
	cmd := exec.Command("mount", "--bind", hostSockPath, hostMountPoint)
	if err := cmd.Run(); err != nil {
		os.RemoveAll(sshDir)
		return nil, "", fmt.Errorf("failed to bind mount SSH agent socket: %w", err)
	}

	cleanup = func() {
		exec.Command("umount", hostMountPoint).Run()
		os.RemoveAll(sshDir)
	}

	return cleanup, chrootSockPath, nil
}

// createExt4Image creates an ext4 image from a directory
func (b *Builder) createExt4Image(layer *Layer, sourceDir string) error {
	rootfsPath := layer.RootfsPath()

	// Create sparse image
	cmd := exec.Command("dd", "if=/dev/zero", "of="+rootfsPath,
		"bs=1M", "count=0", fmt.Sprintf("seek=%d", layer.RootfsSizeMB))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create sparse image: %w", err)
	}

	// Format as ext4
	cmd = exec.Command("mkfs.ext4", "-F", "-L", "rootfs", rootfsPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to format ext4: %w", err)
	}

	// Mount and copy contents
	mountDir := filepath.Join(b.workDir, "mnt-"+layer.ID)
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(mountDir)

	// Clean up any stale mounts from interrupted builds
	if err := unmountIfMounted(rootfsPath); err != nil {
		return fmt.Errorf("failed to clean up stale mounts: %w", err)
	}

	cmd = exec.Command("mount", "-o", "loop", rootfsPath, mountDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount new image: %w", err)
	}

	// Copy contents
	cmd = exec.Command("cp", "-a", sourceDir+"/.", mountDir+"/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		exec.Command("umount", mountDir).Run()
		return fmt.Errorf("failed to copy contents: %w", err)
	}

	// Unmount
	if err := exec.Command("umount", mountDir).Run(); err != nil {
		return fmt.Errorf("failed to unmount new image: %w", err)
	}

	return nil
}

// resizeExt4 resizes an ext4 image
func resizeExt4(path string, sizeMB int) error {
	// Extend the file
	cmd := exec.Command("dd", "if=/dev/zero", "of="+path,
		"bs=1M", "count=0", fmt.Sprintf("seek=%d", sizeMB))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extend image: %w", err)
	}

	// Check filesystem
	cmd = exec.Command("e2fsck", "-f", "-y", path)
	cmd.Run() // Ignore errors, e2fsck returns non-zero for fixes

	// Resize filesystem
	cmd = exec.Command("resize2fs", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resize filesystem: %w", err)
	}

	return nil
}

// downloadFile downloads a file from URL
func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// unmountIfMounted checks if a file (e.g., rootfs.ext4) is currently mounted
// via a loop device and unmounts it. This handles stale mounts from interrupted builds.
// It also handles nested mounts (e.g., bind mounts inside the loop mount).
func unmountIfMounted(devicePath string) error {
	absPath, err := filepath.Abs(devicePath)
	if err != nil {
		return err
	}

	// Use losetup -j to find loop devices associated with this file
	out, err := exec.Command("losetup", "-j", absPath).Output()
	if err != nil {
		// losetup returns error if no loop device found, which is fine
		return nil
	}

	// Parse output like: /dev/loop0: []: (/path/to/file)
	// There may be multiple loop devices
	var loopDevices []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, ":"); idx > 0 {
			loopDevices = append(loopDevices, line[:idx])
		}
	}

	if len(loopDevices) == 0 {
		return nil
	}

	// Read /proc/mounts to find mount points for these loop devices
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return err
	}
	defer file.Close()

	var loopMountPoints []string
	scanner = bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			for _, dev := range loopDevices {
				if fields[0] == dev {
					loopMountPoints = append(loopMountPoints, fields[1])
				}
			}
		}
	}

	// For each loop mount point, find any nested mounts (like bind mounts inside)
	// and collect all mount points that need to be unmounted
	var allMountPoints []string
	for _, loopMP := range loopMountPoints {
		// Re-read /proc/mounts to find nested mounts
		nestedMounts, err := findNestedMounts(loopMP)
		if err != nil {
			return fmt.Errorf("failed to find nested mounts: %w", err)
		}
		allMountPoints = append(allMountPoints, nestedMounts...)
		allMountPoints = append(allMountPoints, loopMP)
	}

	// Unmount deepest first (array is already sorted: nested mounts deepest-first, then loop mount)
	for _, mp := range allMountPoints {
		fmt.Printf("Cleaning up stale mount at %s...\n", mp)
		if err := robustUnmount(mp); err != nil {
			return fmt.Errorf("failed to unmount stale mount at %s: %w", mp, err)
		}
	}

	// Detach any remaining loop devices (in case they weren't mounted)
	for _, dev := range loopDevices {
		exec.Command("losetup", "-d", dev).Run()
	}

	return nil
}

// robustUnmount attempts to unmount a mount point, killing any processes
// that may be holding it busy if the first attempt fails.
func robustUnmount(mountPoint string) error {
	// First attempt: simple unmount
	if err := exec.Command("umount", mountPoint).Run(); err == nil {
		return nil
	}

	// If that failed, there may be processes using the mount point.
	// Use fuser to kill them.
	fmt.Printf("Mount busy, killing processes using %s and trying again...\n", mountPoint)
	exec.Command("fuser", "-km", mountPoint).Run() // -k kills, -m specifies mount point

	// Brief pause to let processes die
	time.Sleep(100 * time.Millisecond)

	// Second attempt
	if err := exec.Command("umount", mountPoint).Run(); err != nil {
		return err
	}
	return nil
}

// findNestedMounts finds all mounts that are under the given mount point
// Returns them sorted by path length (deepest first)
func findNestedMounts(parentMount string) ([]string, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var nested []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			mountPoint := fields[1]
			// Check if this mount is under the parent (but not the parent itself)
			if mountPoint != parentMount && strings.HasPrefix(mountPoint, parentMount+"/") {
				nested = append(nested, mountPoint)
			}
		}
	}

	// Sort by path length descending (deepest first)
	for i := 0; i < len(nested)-1; i++ {
		for j := i + 1; j < len(nested); j++ {
			if len(nested[j]) > len(nested[i]) {
				nested[i], nested[j] = nested[j], nested[i]
			}
		}
	}

	return nested, nil
}

// getRootfsUsage returns used and total space in bytes for an ext4 filesystem
func getRootfsUsage(path string) (used, total int64, err error) {
	out, err := exec.Command("dumpe2fs", "-h", path).Output()
	if err != nil {
		return 0, 0, err
	}

	var blockCount, freeBlocks, blockSize int64
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Block count:") {
			fmt.Sscanf(line, "Block count: %d", &blockCount)
		} else if strings.HasPrefix(line, "Free blocks:") {
			fmt.Sscanf(line, "Free blocks: %d", &freeBlocks)
		} else if strings.HasPrefix(line, "Block size:") {
			fmt.Sscanf(line, "Block size: %d", &blockSize)
		}
	}

	if blockSize == 0 {
		return 0, 0, fmt.Errorf("could not determine block size")
	}

	total = blockCount * blockSize
	used = (blockCount - freeBlocks) * blockSize
	return used, total, nil
}

// formatBytes formats bytes as human-readable size (e.g., "1.2 GB")
func formatBytes(bytes int64) string {
	const (
		GB = 1024 * 1024 * 1024
		MB = 1024 * 1024
	)
	if bytes >= GB {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/float64(MB))
}

// copyFile copies a file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}
