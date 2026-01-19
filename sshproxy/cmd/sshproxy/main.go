package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"flag"
	"log"
	"os"

	"golang.org/x/crypto/ssh"
	"sandfire/sshproxy/internal/sshproxy"
)

func main() {
	listenAddr := flag.String("listen", ":2222", "Address to listen on")
	apiURL := flag.String("api", "http://localhost:9000", "Sandfire API URL")
	hostKeyPath := flag.String("hostkey", "./data/sshproxy_host_key", "Path to host key file")
	flag.Parse()

	// Load or generate host key
	hostKey, err := loadOrGenerateHostKey(*hostKeyPath)
	if err != nil {
		log.Fatalf("Failed to load host key: %v", err)
	}

	// Create API client
	apiClient := sshproxy.NewAPIClient(*apiURL)

	// Create and start server
	server := sshproxy.NewServer(hostKey, apiClient, *listenAddr)
	log.Printf("Starting Sandfire SSH proxy on %s", *listenAddr)
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func loadOrGenerateHostKey(path string) (ssh.Signer, error) {
	// Try to load existing key
	keyData, err := os.ReadFile(path)
	if err == nil {
		signer, err := ssh.ParsePrivateKey(keyData)
		if err == nil {
			log.Printf("Loaded existing host key from %s", path)
			return signer, nil
		}
		log.Printf("Failed to parse existing key, generating new one: %v", err)
	}

	// Generate new ED25519 key
	log.Printf("Generating new ED25519 host key...")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	// Marshal to OpenSSH format
	pemBlock, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, err
	}

	pemData := pem.EncodeToMemory(pemBlock)

	// Ensure directory exists
	if dir := getDir(path); dir != "" {
		os.MkdirAll(dir, 0700)
	}

	// Save to file
	if err := os.WriteFile(path, pemData, 0600); err != nil {
		log.Printf("Warning: could not save host key to %s: %v", path, err)
	} else {
		log.Printf("Saved new host key to %s", path)
	}

	return ssh.ParsePrivateKey(pemData)
}

func getDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}
