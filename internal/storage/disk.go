package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type DiskManager struct {
	dataDir string
}

func NewDiskManager(dataDir string) *DiskManager {
	return &DiskManager{dataDir: dataDir}
}

func (dm *DiskManager) EnsureDirectories() error {
	dirs := []string{
		filepath.Join(dm.dataDir, "images"),
		filepath.Join(dm.dataDir, "vms"),
		filepath.Join(dm.dataDir, "jails"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	return nil
}

func (dm *DiskManager) ResizeDisk(path string, sizeGB int) error {
	// First truncate to the desired size
	sizeBytes := int64(sizeGB) * 1024 * 1024 * 1024
	if err := os.Truncate(path, sizeBytes); err != nil {
		return fmt.Errorf("truncate disk: %w", err)
	}

	// Then resize the filesystem
	cmd := exec.Command("e2fsck", "-f", "-y", path)
	cmd.Run() // Ignore errors, just ensure fs is clean

	cmd = exec.Command("resize2fs", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("resize2fs: %s: %w", string(output), err)
	}

	return nil
}

func (dm *DiskManager) GetImagesDir() string {
	return filepath.Join(dm.dataDir, "images")
}

func (dm *DiskManager) GetVMsDir() string {
	return filepath.Join(dm.dataDir, "vms")
}
