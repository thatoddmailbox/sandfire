package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"sandfire/internal/api"
	"sandfire/internal/certs"
	"sandfire/internal/config"
	"sandfire/internal/db"
	"sandfire/internal/network"
	"sandfire/internal/storage"
	"sandfire/internal/vm"
)

const (
	ListenAddr = ":9000"
	DataDir    = "./data"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println(`Sandfire - VM Management Service

Sandfire is a VM management service that uses Firecracker VMM for fast,
secure microVM creation. It provides a simple HTTP API for managing
virtual machines with automatic networking via NAT.

Usage: sandfire

The server listens on port 9000 and stores data in ./data/

Environment variables:
  SANDFIRE_DOMAIN        Base domain for VMs (default: sand.studer.dev)
  CLOUDFLARE_API_TOKEN   Cloudflare API token for certificate management
  SANDFIRE_ACME_STAGING  Set to "1" to use ACME staging environment

Use the scripts in scripts/ to interact with the server.`)
		os.Exit(0)
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Sandfire VM Manager...")

	// Get absolute path for data directory
	dataDir, err := filepath.Abs(DataDir)
	if err != nil {
		log.Fatalf("Failed to resolve data directory: %v", err)
	}

	// Load environment variables from envfile (if present)
	config.LoadEnvFile(dataDir)

	// Initialize storage
	diskMgr := storage.NewDiskManager(dataDir)
	if err := diskMgr.EnsureDirectories(); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	// Initialize database
	database, err := db.Open(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Initialize network manager
	networkMgr := network.NewManager()

	// Load allocated IPs from database
	allocatedIPs, err := database.GetAllocatedIPs()
	if err != nil {
		log.Fatalf("Failed to get allocated IPs: %v", err)
	}
	networkMgr.SetAllocatedIPs(allocatedIPs)

	// Ensure bridge exists (warn but don't fail if not root)
	if err := networkMgr.EnsureBridge(); err != nil {
		log.Printf("Warning: Failed to ensure bridge (need root): %v", err)
		log.Printf("VM networking will not work. Run with sudo for full functionality.")
	}

	// Handle VMs that were left in 'running' state after an unclean shutdown
	// This must be done BEFORE loading allocated IPs, since we're clearing stale IPs
	if count, err := database.PrepareOrphanedVMsForRestart(); err != nil {
		log.Printf("Warning: failed to prepare orphaned VMs: %v", err)
	} else if count > 0 {
		log.Printf("Found %d VM(s) orphaned from previous unclean shutdown, will restart them", count)
	}

	// Initialize VM manager
	vmManager := vm.NewManager(dataDir, networkMgr)

	// Clean up any orphaned resources from previous unclean shutdown
	vmManager.CleanupOrphanedResources()

	// Reload allocated IPs after cleanup (to exclude IPs that were cleared)
	networkMgr.ClearAllocatedIPs()
	allocatedIPs, err = database.GetAllocatedIPs()
	if err != nil {
		log.Printf("Warning: failed to reload allocated IPs: %v", err)
	} else {
		networkMgr.SetAllocatedIPs(allocatedIPs)
	}

	// Restore previously running VMs
	restoreRunningVMs(database, vmManager)

	// Initialize certificate manager
	baseDomain := os.Getenv("SANDFIRE_DOMAIN")
	if baseDomain == "" {
		baseDomain = "sand.studer.dev"
	}
	cloudflareToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	if cloudflareToken == "" {
		log.Println("Warning: CLOUDFLARE_API_TOKEN not set, certificate management will not work")
	}
	useACMEStaging := os.Getenv("SANDFIRE_ACME_STAGING") == "1"

	var certManager *certs.Manager
	if cloudflareToken != "" {
		var err error
		certManager, err = certs.NewManager(dataDir, baseDomain, cloudflareToken, useACMEStaging)
		if err != nil {
			log.Printf("Warning: Failed to initialize certificate manager: %v", err)
		} else {
			log.Printf("Certificate manager initialized for domain %s", baseDomain)
		}
	}

	// Create HTTP server
	server := api.NewServer(database, vmManager, certManager)
	httpServer := &http.Server{
		Addr:    ListenAddr,
		Handler: server,
	}

	// Handle shutdown gracefully
	done := make(chan bool, 1)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down...")

		// Mark running VMs for restart before stopping them
		if err := database.MarkRunningVMsForRestart(); err != nil {
			log.Printf("Warning: failed to mark VMs for restart: %v", err)
		}

		// Stop all running VMs
		vmManager.StopAllVMs()

		// Mark all VMs as stopped in database
		if vms, err := database.GetRunningVMs(); err == nil {
			for _, v := range vms {
				database.UpdateVMState(v.ID, "stopped", nil, nil)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		httpServer.SetKeepAlivesEnabled(false)
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}

		close(done)
	}()

	log.Printf("Server listening on %s", ListenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}

	<-done
	log.Println("Sandfire stopped")
}

func restoreRunningVMs(database *db.DB, vmManager *vm.Manager) {
	vms, err := database.GetVMsToRestart()
	if err != nil {
		log.Printf("Warning: failed to get VMs to restart: %v", err)
		return
	}

	if len(vms) == 0 {
		return
	}

	log.Printf("Restoring %d previously running VMs...", len(vms))

	for _, v := range vms {
		log.Printf("Restoring VM %s (%s)...", v.Name, v.ID)

		img, err := database.GetOSImage(v.OSImageID)
		if err != nil || img == nil {
			log.Printf("Warning: failed to get OS image for VM %s: %v", v.ID, err)
			database.UpdateVMState(v.ID, "error", nil, nil)
			database.ClearWasRunning(v.ID)
			continue
		}

		ipAddress, tapDevice, err := vmManager.StartVM(&v, img, nil)
		if err != nil {
			log.Printf("Warning: failed to restore VM %s: %v", v.ID, err)
			database.UpdateVMState(v.ID, "error", nil, nil)
			database.ClearWasRunning(v.ID)
			continue
		}

		database.UpdateVMState(v.ID, "running", &ipAddress, &tapDevice)
		database.ClearWasRunning(v.ID)
		log.Printf("Restored VM %s with IP %s", v.Name, ipAddress)
	}
}
