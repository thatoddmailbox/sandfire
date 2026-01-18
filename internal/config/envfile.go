package config

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile loads environment variables from an envfile.
// It looks for an envfile in the following locations (in order):
// 1. Alongside the data directory (e.g., /var/lib/sandfire/envfile for systemd)
// 2. In the current working directory (e.g., ./envfile for local development)
//
// If no envfile is found, it logs a warning but does not fail.
// Environment variables already set take precedence over values in the envfile.
func LoadEnvFile(dataDir string) {
	// Determine envfile path - parent of data directory
	// For /var/lib/sandfire/data -> /var/lib/sandfire/envfile
	// For ./data -> ./envfile
	parentDir := filepath.Dir(dataDir)
	envfilePath := filepath.Join(parentDir, "envfile")

	// Check if the envfile exists
	if _, err := os.Stat(envfilePath); os.IsNotExist(err) {
		// Also try current working directory for local development
		cwd, _ := os.Getwd()
		cwdEnvfile := filepath.Join(cwd, "envfile")
		if _, err := os.Stat(cwdEnvfile); os.IsNotExist(err) {
			log.Printf("Warning: No envfile found at %s or %s", envfilePath, cwdEnvfile)
			log.Printf("Create an envfile to configure environment variables like CLOUDFLARE_API_TOKEN")
			return
		}
		envfilePath = cwdEnvfile
	}

	if err := loadEnvFileFrom(envfilePath); err != nil {
		log.Printf("Warning: Failed to load envfile from %s: %v", envfilePath, err)
		return
	}

	log.Printf("Loaded environment from %s", envfilePath)
}

// loadEnvFileFrom reads the envfile and sets environment variables.
// Lines starting with # are treated as comments.
// Empty lines are skipped.
// Format: KEY=value (no export prefix, no quotes handling)
func loadEnvFileFrom(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			log.Printf("Warning: Invalid line %d in envfile: %s", lineNum, line)
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Don't override existing environment variables
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		os.Setenv(key, value)
	}

	return scanner.Err()
}
