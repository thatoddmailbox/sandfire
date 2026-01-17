package layercake

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
		"--include=systemd,systemd-sysv,udev,iproute2,iputils-ping,openssh-server,sudo,curl,ca-certificates,passwd,busybox-static,python3,htop",
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

	fmt.Printf("Layer %s built successfully\n", layer.ID)
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

	fmt.Printf("Layer %s built successfully\n", layer.ID)
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

	// Run in chroot
	cmd := exec.Command("chroot", rootfsDir, "/bin/bash", "/tmp/layer.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DEBIAN_FRONTEND=noninteractive",
	}
	if err := cmd.Run(); err != nil {
		return err
	}

	// Clean up
	os.Remove(scriptDest)
	return nil
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
