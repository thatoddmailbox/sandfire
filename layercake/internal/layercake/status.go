package layercake

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// BuildStatus represents the build status of a layer
type BuildStatus struct {
	Built       bool
	Stale       bool
	StaleReason string
}

// GetBuildStatus checks if a layer needs to be built
func GetBuildStatus(layer *Layer, graph *LayerGraph) (*BuildStatus, error) {
	// Use a cache to avoid recomputing status for the same layer
	cache := make(map[string]*BuildStatus)
	return getBuildStatusCached(layer, graph, cache)
}

func getBuildStatusCached(layer *Layer, graph *LayerGraph, cache map[string]*BuildStatus) (*BuildStatus, error) {
	// Check cache first
	if cached, ok := cache[layer.ID]; ok {
		return cached, nil
	}

	status := &BuildStatus{}

	// Check if rootfs exists
	if _, err := os.Stat(layer.RootfsPath()); os.IsNotExist(err) {
		status.Built = false
		status.Stale = true
		status.StaleReason = "rootfs.ext4 does not exist"
		cache[layer.ID] = status
		return status, nil
	}
	status.Built = true

	// Check if build hash exists
	hashPath := layer.BuildHashPath()
	savedHash, err := readBuildHash(hashPath)
	if err != nil {
		status.Stale = true
		status.StaleReason = "no build hash found"
		cache[layer.ID] = status
		return status, nil
	}

	// Check if layer.sh has changed
	currentHash, err := hashFile(layer.ScriptPath())
	if err != nil {
		status.Stale = true
		status.StaleReason = fmt.Sprintf("cannot hash layer.sh: %v", err)
		cache[layer.ID] = status
		return status, nil
	}

	if currentHash != savedHash.ScriptHash {
		status.Stale = true
		status.StaleReason = "layer.sh has changed"
		cache[layer.ID] = status
		return status, nil
	}

	// Check if layer.conf has changed
	currentConfigHash, err := hashFile(layer.ConfigPath())
	if err != nil {
		status.Stale = true
		status.StaleReason = fmt.Sprintf("cannot hash layer.conf: %v", err)
		cache[layer.ID] = status
		return status, nil
	}

	if currentConfigHash != savedHash.ConfigHash {
		status.Stale = true
		status.StaleReason = "layer.conf has changed"
		cache[layer.ID] = status
		return status, nil
	}

	// Check if layer.secrets has changed (optional file)
	currentSecretsHash, _ := hashFile(layer.SecretsPath()) // empty string if file doesn't exist
	if currentSecretsHash != savedHash.SecretsHash {
		status.Stale = true
		status.StaleReason = "layer.secrets has changed"
		cache[layer.ID] = status
		return status, nil
	}

	// Check parent status (for non-base layers)
	if !layer.IsBase() {
		parent := graph.Get(layer.Parent)
		if parent == nil {
			status.Stale = true
			status.StaleReason = "parent layer not found"
			cache[layer.ID] = status
			return status, nil
		}

		// Recursively check if parent is stale
		parentStatus, err := getBuildStatusCached(parent, graph, cache)
		if err != nil {
			status.Stale = true
			status.StaleReason = fmt.Sprintf("cannot check parent: %v", err)
			cache[layer.ID] = status
			return status, nil
		}

		if parentStatus.Stale {
			status.Stale = true
			status.StaleReason = fmt.Sprintf("parent %s is stale", parent.ID)
			cache[layer.ID] = status
			return status, nil
		}

		// Also check if parent has been rebuilt since this layer was built
		parentRootfs := parent.RootfsPath()
		parentInfo, err := os.Stat(parentRootfs)
		if err != nil {
			status.Stale = true
			status.StaleReason = "parent rootfs not found"
			cache[layer.ID] = status
			return status, nil
		}

		if parentInfo.ModTime().After(savedHash.BuildTime) {
			status.Stale = true
			status.StaleReason = "parent has been rebuilt"
			cache[layer.ID] = status
			return status, nil
		}
	}

	cache[layer.ID] = status
	return status, nil
}

// BuildHash stores information about a build
type BuildHash struct {
	ScriptHash  string
	ConfigHash  string
	SecretsHash string // empty if no layer.secrets file
	BuildTime   time.Time
}

// readBuildHash reads the .build-hash file
func readBuildHash(path string) (*BuildHash, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hash := &BuildHash{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "SCRIPT_HASH":
			hash.ScriptHash = parts[1]
		case "CONFIG_HASH":
			hash.ConfigHash = parts[1]
		case "SECRETS_HASH":
			hash.SecretsHash = parts[1]
		case "BUILD_TIME":
			if t, err := time.Parse(time.RFC3339, parts[1]); err == nil {
				hash.BuildTime = t
			}
		}
	}
	return hash, scanner.Err()
}

// WriteBuildHash writes the .build-hash file
func WriteBuildHash(layer *Layer) error {
	scriptHash, err := hashFile(layer.ScriptPath())
	if err != nil {
		return fmt.Errorf("failed to hash layer.sh: %w", err)
	}

	configHash, err := hashFile(layer.ConfigPath())
	if err != nil {
		return fmt.Errorf("failed to hash layer.conf: %w", err)
	}

	// layer.secrets is optional, so we ignore errors (missing file = empty hash)
	secretsHash, _ := hashFile(layer.SecretsPath())

	content := fmt.Sprintf("SCRIPT_HASH=%s\nCONFIG_HASH=%s\nSECRETS_HASH=%s\nBUILD_TIME=%s\n",
		scriptHash, configHash, secretsHash, time.Now().UTC().Format(time.RFC3339))

	return os.WriteFile(layer.BuildHashPath(), []byte(content), 0644)
}

// hashFile computes SHA256 hash of a file
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
