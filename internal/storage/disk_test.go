package storage

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const MiB = 1024 * 1024

// makeExt4 creates an ext4 image of sizeBytes containing a file named "data"
// with dataBytes of random content, and returns the image path and content.
func makeExt4(t *testing.T, sizeBytes int64, dataBytes int) (string, []byte) {
	t.Helper()
	for _, tool := range []string{"mkfs.ext4", "e2fsck", "resize2fs", "dumpe2fs", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}

	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, dataBytes)
	rand.Read(data)
	if err := os.WriteFile(filepath.Join(srcDir, "data"), data, 0644); err != nil {
		t.Fatal(err)
	}

	img := filepath.Join(dir, "rootfs.ext4")
	if err := os.WriteFile(img, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(img, sizeBytes); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("mkfs.ext4", "-q", "-F", "-b", "4096", "-d", srcDir, img).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %s: %v", output, err)
	}
	return img, data
}

// leaveUnreplayedJournal commits a transaction to the image's journal without
// replaying it, leaving the filesystem in the state a killed VM leaves it in.
// The transaction rewrites the filesystem's last block, which is unused.
func leaveUnreplayedJournal(t *testing.T, img string) {
	t.Helper()

	output, err := exec.Command("dumpe2fs", "-h", img).Output()
	if err != nil {
		t.Fatalf("dumpe2fs: %v", err)
	}
	var blockCount int64
	for _, line := range strings.Split(string(output), "\n") {
		if rest, ok := strings.CutPrefix(line, "Block count:"); ok {
			fmt.Sscan(rest, &blockCount)
		}
	}
	if blockCount == 0 {
		t.Fatalf("could not find block count:\n%s", output)
	}

	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, make([]byte, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("debugfs", "-w", "-f", "-", img)
	cmd.Stdin = strings.NewReader(fmt.Sprintf("jo\njw -b %d %s\njc\n", blockCount-1, block))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("debugfs: %s: %v", output, err)
	}

	// Make sure this actually reproduces the problem being tested.
	if output, err := exec.Command("resize2fs", "-P", img).CombinedOutput(); err == nil {
		t.Fatalf("resize2fs -P succeeded on image with unreplayed journal: %s", output)
	}
}

// checkImage verifies the image's file size, that the filesystem fills it and
// is clean, and that the data file is intact.
func checkImage(t *testing.T, img string, wantSize int64, wantData []byte) {
	t.Helper()

	st, err := os.Stat(img)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != wantSize {
		t.Errorf("file size = %d, want %d", st.Size(), wantSize)
	}

	output, err := exec.Command("dumpe2fs", "-h", img).Output()
	if err != nil {
		t.Fatalf("dumpe2fs: %v", err)
	}
	if !strings.Contains(string(output), fmt.Sprintf("Block count:              %d\n", wantSize/4096)) {
		t.Errorf("filesystem does not fill the image:\n%s", output)
	}

	if output, err := exec.Command("e2fsck", "-f", "-n", img).CombinedOutput(); err != nil {
		t.Errorf("e2fsck: %s: %v", output, err)
	}

	got, err := exec.Command("debugfs", "-R", "cat /data", img).Output()
	if err != nil {
		t.Fatalf("debugfs: %v", err)
	}
	if !bytes.Equal(got, wantData) {
		t.Errorf("data file changed (got %d bytes, want %d)", len(got), len(wantData))
	}
}

func TestInspectExt4(t *testing.T) {
	img, _ := makeExt4(t, 256*MiB, 64*MiB)

	info, err := InspectExt4(img)
	if err != nil {
		t.Fatal(err)
	}
	if info.SizeBytes != 256*MiB {
		t.Errorf("SizeBytes = %d, want %d", info.SizeBytes, 256*MiB)
	}
	if info.UsedBytes < 64*MiB || info.UsedBytes > 128*MiB {
		t.Errorf("UsedBytes = %d, want between 64 and 128 MiB", info.UsedBytes)
	}
	if info.MinSizeBytes < 64*MiB || info.MinSizeBytes > info.SizeBytes {
		t.Errorf("MinSizeBytes = %d, want between 64 MiB and size", info.MinSizeBytes)
	}
}

func TestResizeExt4GrowAndShrink(t *testing.T) {
	img, data := makeExt4(t, 256*MiB, 64*MiB)

	if err := ResizeExt4(img, 1024*MiB); err != nil {
		t.Fatalf("grow: %v", err)
	}
	checkImage(t, img, 1024*MiB, data)

	info, err := InspectExt4(img)
	if err != nil {
		t.Fatal(err)
	}
	shrunk := (info.MinSizeBytes/MiB + 1) * MiB
	if err := ResizeExt4(img, shrunk); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	checkImage(t, img, shrunk, data)

	// Same size is a no-op.
	if err := ResizeExt4(img, shrunk); err != nil {
		t.Fatalf("no-op resize: %v", err)
	}
	checkImage(t, img, shrunk, data)
}

func TestResizeExt4ShrinkTooSmall(t *testing.T) {
	img, data := makeExt4(t, 256*MiB, 128*MiB)

	err := ResizeExt4(img, 64*MiB)
	if err == nil {
		t.Fatal("shrinking below used space succeeded")
	}
	if !strings.Contains(err.Error(), "needs at least") {
		t.Errorf("unexpected error: %v", err)
	}
	checkImage(t, img, 256*MiB, data)
}

func TestInspectExt4UnreplayedJournal(t *testing.T) {
	img, data := makeExt4(t, 256*MiB, 64*MiB)
	leaveUnreplayedJournal(t, img)

	info, err := InspectExt4(img)
	if err != nil {
		t.Fatal(err)
	}
	if info.UsedBytes < 64*MiB || info.MinSizeBytes < 64*MiB {
		t.Errorf("UsedBytes = %d, MinSizeBytes = %d, want both at least 64 MiB", info.UsedBytes, info.MinSizeBytes)
	}
	checkImage(t, img, 256*MiB, data)
}

func TestResizeExt4UnreplayedJournal(t *testing.T) {
	img, data := makeExt4(t, 256*MiB, 64*MiB)

	leaveUnreplayedJournal(t, img)
	if err := ResizeExt4(img, 512*MiB); err != nil {
		t.Fatalf("grow: %v", err)
	}
	checkImage(t, img, 512*MiB, data)

	leaveUnreplayedJournal(t, img)
	if err := ResizeExt4(img, 192*MiB); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	checkImage(t, img, 192*MiB, data)
}
