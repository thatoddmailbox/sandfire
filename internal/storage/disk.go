package storage

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

const GiB = 1024 * 1024 * 1024

// Ext4Info describes the size and usage of an ext4 image file.
type Ext4Info struct {
	SizeBytes    int64 `json:"size_bytes"`
	UsedBytes    int64 `json:"used_bytes"`
	MinSizeBytes int64 `json:"min_size_bytes"`
}

// InspectExt4 reports the size of an ext4 image file, how much of its
// filesystem is in use, and the smallest size it could be shrunk to. The
// filesystem must not be mounted.
//
// If the filesystem wasn't cleanly unmounted (e.g. its VM was killed), its
// journal is replayed first, just as mounting it would.
func InspectExt4(path string) (*Ext4Info, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat disk: %w", err)
	}

	if err := replayExt4Journal(path); err != nil {
		return nil, err
	}

	sb, err := readExt4Superblock(path)
	if err != nil {
		return nil, err
	}

	minBlocks, err := minExt4Blocks(path)
	if err != nil {
		return nil, err
	}

	return &Ext4Info{
		SizeBytes:    st.Size(),
		UsedBytes:    (sb.blockCount - sb.freeBlocks) * sb.blockSize,
		MinSizeBytes: minBlocks * sb.blockSize,
	}, nil
}

type ext4Superblock struct {
	blockCount, freeBlocks, blockSize int64
}

// size returns the size of the filesystem in bytes.
func (sb *ext4Superblock) size() int64 {
	return sb.blockCount * sb.blockSize
}

// readExt4Superblock reads the block counts and block size from an ext4 image.
func readExt4Superblock(path string) (*ext4Superblock, error) {
	output, err := exec.Command("dumpe2fs", "-h", path).Output()
	if err != nil {
		return nil, fmt.Errorf("dumpe2fs: %w", err)
	}
	var sb ext4Superblock
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "Block count":
			sb.blockCount = n
		case "Free blocks":
			sb.freeBlocks = n
		case "Block size":
			sb.blockSize = n
		}
	}
	if sb.blockCount == 0 || sb.blockSize == 0 {
		return nil, fmt.Errorf("dumpe2fs: could not parse filesystem size")
	}
	return &sb, nil
}

// minExt4Blocks returns resize2fs's estimate of the minimum filesystem size,
// in filesystem blocks.
func minExt4Blocks(path string) (int64, error) {
	output, err := exec.Command("resize2fs", "-P", path).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("resize2fs -P: %s: %w", strings.TrimSpace(string(output)), err)
	}
	const prefix = "Estimated minimum size of the filesystem:"
	for _, line := range strings.Split(string(output), "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		}
	}
	return 0, fmt.Errorf("resize2fs -P: could not parse output: %s", strings.TrimSpace(string(output)))
}

// checkExt4 runs a forced filesystem check, automatically fixing only problems
// that are safe to fix without human intervention.
func checkExt4(path string) error {
	return runE2fsck(path, "-f", "-p")
}

// replayExt4Journal replays the filesystem's journal if it needs recovery,
// without performing any other checks or repairs.
func replayExt4Journal(path string) error {
	return runE2fsck(path, "-E", "journal_only", "-p")
}

func runE2fsck(path string, args ...string) error {
	output, err := exec.Command("e2fsck", append(args, path)...).CombinedOutput()
	if err != nil {
		// Exit codes 1 and 2 mean errors were found and corrected (2 also asks
		// for a reboot, which doesn't apply to an unmounted image), which is fine.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 2) {
			return nil
		}
		return fmt.Errorf("e2fsck found problems that need manual repair (run e2fsck -f on %s): %s: %w",
			path, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// ResizeExt4 grows or shrinks an ext4 image file and the filesystem inside it
// to exactly sizeBytes. The filesystem must not be mounted.
//
// When shrinking, the filesystem is shrunk before the file is truncated, so a
// failure (e.g. not enough free space) leaves the image untouched.
func ResizeExt4(path string, sizeBytes int64) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat disk: %w", err)
	}
	if sizeBytes%1024 != 0 {
		return fmt.Errorf("size must be a multiple of 1 KiB")
	}
	if sizeBytes == st.Size() {
		return nil
	}

	if err := checkExt4(path); err != nil {
		return err
	}

	if sizeBytes > st.Size() {
		if err := os.Truncate(path, sizeBytes); err != nil {
			return fmt.Errorf("grow disk file: %w", err)
		}
		if output, err := exec.Command("resize2fs", path).CombinedOutput(); err != nil {
			// Put the file back to its original size so it still matches the
			// filesystem, unless resize2fs got far enough to grow the filesystem.
			if sb, sbErr := readExt4Superblock(path); sbErr == nil && sb.size() <= st.Size() {
				if truncErr := os.Truncate(path, st.Size()); truncErr != nil {
					log.Printf("Warning: failed to restore size of %s after failed resize: %v", path, truncErr)
				}
			}
			return fmt.Errorf("resize2fs: %s: %w", strings.TrimSpace(string(output)), err)
		}
		return nil
	}

	info, err := InspectExt4(path)
	if err != nil {
		return err
	}
	if sizeBytes < info.MinSizeBytes {
		return fmt.Errorf("cannot shrink to %.1f GB: filesystem needs at least %d GB",
			float64(sizeBytes)/GiB, (info.MinSizeBytes+GiB-1)/GiB)
	}

	sizeArg := fmt.Sprintf("%dK", sizeBytes/1024)
	if output, err := exec.Command("resize2fs", path, sizeArg).CombinedOutput(); err != nil {
		return fmt.Errorf("resize2fs: %s: %w", strings.TrimSpace(string(output)), err)
	}
	if err := os.Truncate(path, sizeBytes); err != nil {
		return fmt.Errorf("shrink disk file: %w", err)
	}
	return nil
}

func (dm *DiskManager) GetImagesDir() string {
	return filepath.Join(dm.dataDir, "images")
}

func (dm *DiskManager) GetVMsDir() string {
	return filepath.Join(dm.dataDir, "vms")
}
