package layercake

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Layer represents a single image layer configuration
type Layer struct {
	ID           string
	Name         string
	Parent       string // "scratch" for base image
	KernelURL    string // only for base image
	RootfsSizeMB int
	Export       bool
	Dir          string // absolute path to layer directory
}

// LoadLayer loads a layer configuration from a directory
func LoadLayer(dir string) (*Layer, error) {
	confPath := filepath.Join(dir, "layer.conf")
	f, err := os.Open(confPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open layer.conf: %w", err)
	}
	defer f.Close()

	layer := &Layer{
		Dir:          dir,
		RootfsSizeMB: 2048, // default
		Export:       false,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "ID":
			layer.ID = value
		case "NAME":
			layer.Name = value
		case "PARENT":
			layer.Parent = value
		case "KERNEL_URL":
			layer.KernelURL = value
		case "ROOTFS_SIZE_MB":
			if size, err := strconv.Atoi(value); err == nil {
				layer.RootfsSizeMB = size
			}
		case "EXPORT":
			layer.Export = strings.ToLower(value) == "true"
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read layer.conf: %w", err)
	}

	if layer.ID == "" {
		return nil, fmt.Errorf("layer.conf missing required ID field")
	}
	if layer.Parent == "" {
		return nil, fmt.Errorf("layer.conf missing required PARENT field")
	}
	if layer.Name == "" {
		layer.Name = layer.ID
	}

	return layer, nil
}

// LoadAllLayers loads all layers from the given directories.
// Directories are processed in order, with later directories overriding
// layers with the same ID from earlier directories.
// This allows layers-local to override layers from the main layers folder.
func LoadAllLayers(layersDirs ...string) ([]*Layer, error) {
	layerMap := make(map[string]*Layer)

	for _, layersDir := range layersDirs {
		// Skip directories that don't exist
		if _, err := os.Stat(layersDir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(layersDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read layers directory %s: %w", layersDir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			dir := filepath.Join(layersDir, entry.Name())
			confPath := filepath.Join(dir, "layer.conf")
			if _, err := os.Stat(confPath); os.IsNotExist(err) {
				continue
			}

			layer, err := LoadLayer(dir)
			if err != nil {
				return nil, fmt.Errorf("failed to load layer %s: %w", entry.Name(), err)
			}

			// Verify directory name matches ID
			if entry.Name() != layer.ID {
				return nil, fmt.Errorf("layer directory name %q does not match ID %q", entry.Name(), layer.ID)
			}

			// Later directories override earlier ones
			layerMap[layer.ID] = layer
		}
	}

	// Convert map to slice
	var layers []*Layer
	for _, layer := range layerMap {
		layers = append(layers, layer)
	}

	return layers, nil
}

// IsBase returns true if this is a base layer (PARENT=scratch)
func (l *Layer) IsBase() bool {
	return l.Parent == "scratch"
}

// RootfsPath returns the path to the rootfs.ext4 file
func (l *Layer) RootfsPath() string {
	return filepath.Join(l.Dir, "rootfs.ext4")
}

// KernelPath returns the path to the vmlinux file
func (l *Layer) KernelPath() string {
	return filepath.Join(l.Dir, "vmlinux")
}

// ScriptPath returns the path to the layer.sh file
func (l *Layer) ScriptPath() string {
	return filepath.Join(l.Dir, "layer.sh")
}

// ConfigPath returns the path to the layer.conf file
func (l *Layer) ConfigPath() string {
	return filepath.Join(l.Dir, "layer.conf")
}

// BuildHashPath returns the path to the .build-hash file
func (l *Layer) BuildHashPath() string {
	return filepath.Join(l.Dir, ".build-hash")
}
