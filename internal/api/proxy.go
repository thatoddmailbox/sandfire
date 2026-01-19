package api

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// vmIDRegex matches VM IDs like "vm-e2aa90c4"
var vmIDRegex = regexp.MustCompile(`^vm-[a-f0-9]{8}$`)

// getBaseDomain returns the base domain from SANDFIRE_DOMAIN env var
func getBaseDomain() string {
	domain := os.Getenv("SANDFIRE_DOMAIN")
	if domain == "" {
		return "sand.studer.dev"
	}
	return domain
}

// extractVMID parses the Host header and extracts the VM ID if present.
// Returns empty string for base domain requests.
// Returns error if the subdomain pattern is invalid (too many levels).
//
// Examples:
//   - sand.studer.dev -> "", nil (base domain)
//   - vm-abc12345.sand.studer.dev -> "vm-abc12345", nil
//   - app.vm-abc12345.sand.studer.dev -> "vm-abc12345", nil
//   - foo.bar.vm-xxx.sand.studer.dev -> "", error (too many levels)
func extractVMID(host string) (string, error) {
	baseDomain := getBaseDomain()

	// Strip port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Exact match with base domain
	if host == baseDomain {
		return "", nil
	}

	// Must be a subdomain of base domain
	suffix := "." + baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", fmt.Errorf("host %q is not under base domain %q", host, baseDomain)
	}

	// Extract subdomain part
	subdomain := strings.TrimSuffix(host, suffix)
	parts := strings.Split(subdomain, ".")

	// Validate subdomain depth (max 2 levels: optional app prefix + vm-id)
	if len(parts) > 2 {
		return "", fmt.Errorf("too many subdomain levels: %q", subdomain)
	}

	// The VM ID is either the only part or the last part (after app prefix)
	var vmID string
	if len(parts) == 1 {
		vmID = parts[0]
	} else {
		vmID = parts[1] // e.g., "app.vm-abc12345" -> "vm-abc12345"
	}

	// Validate VM ID format
	if !vmIDRegex.MatchString(vmID) {
		return "", fmt.Errorf("invalid VM ID format: %q", vmID)
	}

	return vmID, nil
}

// handleCaddyGetCertificate handles GET /api/caddy/get-certificate
// This is called by Caddy's get_certificate HTTP module to fetch certificates.
// The domain parameter should be a VM subdomain like "vm-abc12345.sand.studer.dev"
// or "app.vm-abc12345.sand.studer.dev".
// Returns PEM-encoded certificate chain and private key concatenated.
func (s *Server) handleCaddyGetCertificate(w http.ResponseWriter, r *http.Request) {
	if s.certManager == nil {
		log.Printf("get-certificate: certificate manager not initialized")
		http.Error(w, "certificate manager not available", http.StatusServiceUnavailable)
		return
	}

	// Caddy sends server_name query param, but we also support domain for testing
	domain := r.URL.Query().Get("server_name")
	if domain == "" {
		domain = r.URL.Query().Get("domain")
	}
	if domain == "" {
		http.Error(w, "missing server_name parameter", http.StatusBadRequest)
		return
	}

	vmID, err := extractVMID(domain)
	if err != nil {
		log.Printf("get-certificate: rejecting %q: %v", domain, err)
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}

	// Base domain certificates should be handled differently (or not at all by us)
	if vmID == "" {
		log.Printf("get-certificate: rejecting base domain %q (should use static cert)", domain)
		http.Error(w, "base domain not supported", http.StatusBadRequest)
		return
	}

	// Check if VM exists
	vm, err := s.db.GetVM(vmID)
	if err != nil {
		log.Printf("get-certificate: database error for %q: %v", domain, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if vm == nil {
		log.Printf("get-certificate: rejecting %q: VM %s not found", domain, vmID)
		http.Error(w, "VM not found", http.StatusNotFound)
		return
	}

	// Get or issue certificate
	ctx := r.Context()
	certData, err := s.certManager.GetCertificate(ctx, vmID)
	if err != nil {
		log.Printf("get-certificate: failed to get certificate for %q: %v", domain, err)
		http.Error(w, "failed to get certificate", http.StatusInternalServerError)
		return
	}

	log.Printf("get-certificate: serving certificate for %q (VM %s)", domain, vmID)
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Write(certData)
}

// handleVMProxy handles requests to VM subdomains by proxying to the VM
func (s *Server) handleVMProxy(w http.ResponseWriter, r *http.Request, vmID string) {
	vm, err := s.db.GetVM(vmID)
	if err != nil {
		log.Printf("proxy: database error for VM %s: %v", vmID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if vm == nil {
		s.renderVMNotFound(w, vmID)
		return
	}

	if vm.State != "running" {
		s.renderVMOffline(w, vm.ID, vm.Name)
		return
	}

	if vm.IPAddress == nil {
		log.Printf("proxy: VM %s is running but has no IP address", vmID)
		s.renderVMUnreachable(w, vm.ID, vm.Name)
		return
	}

	// Create reverse proxy to VM
	target, err := url.Parse(fmt.Sprintf("http://%s:80", *vm.IPAddress))
	if err != nil {
		log.Printf("proxy: failed to parse target URL for VM %s: %v", vmID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Configure for WebSocket and SSE support
	proxy.FlushInterval = -1 // Flush immediately for SSE

	// Custom error handler for unreachable VMs
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy: error proxying to VM %s (%s): %v", vmID, *vm.IPAddress, err)
		s.renderVMUnreachable(w, vm.ID, vm.Name)
	}

	// Longer timeout for WebSocket/SSE connections
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: 0, // No timeout for response headers (for SSE)
	}

	log.Printf("proxy: forwarding request to VM %s at %s", vmID, *vm.IPAddress)
	proxy.ServeHTTP(w, r)
}

// renderVMNotFound renders a 404 page for non-existent VMs
func (s *Server) renderVMNotFound(w http.ResponseWriter, vmID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, vmNotFoundHTML, vmID)
}

// renderVMOffline renders a page explaining the VM is stopped
func (s *Server) renderVMOffline(w http.ResponseWriter, vmID, vmName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, vmOfflineHTML, vmName, vmID)
}

// renderVMUnreachable renders a page when the VM is running but unreachable
func (s *Server) renderVMUnreachable(w http.ResponseWriter, vmID, vmName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, vmUnreachableHTML, vmName, vmID)
}

const vmNotFoundHTML = `<!DOCTYPE html>
<html>
<head>
    <title>VM Not Found</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
        }
        .container {
            text-align: center;
            padding: 2rem;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            max-width: 400px;
        }
        h1 { color: #e53e3e; margin-bottom: 0.5rem; }
        p { color: #666; }
        code { background: #f0f0f0; padding: 0.2rem 0.5rem; border-radius: 4px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>VM Not Found</h1>
        <p>The virtual machine <code>%s</code> does not exist.</p>
    </div>
</body>
</html>`

const vmOfflineHTML = `<!DOCTYPE html>
<html>
<head>
    <title>VM Offline - %s</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
        }
        .container {
            text-align: center;
            padding: 2rem;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            max-width: 400px;
        }
        h1 { color: #dd6b20; margin-bottom: 0.5rem; }
        p { color: #666; }
        code { background: #f0f0f0; padding: 0.2rem 0.5rem; border-radius: 4px; }
        .status {
            display: inline-block;
            background: #fed7d7;
            color: #c53030;
            padding: 0.25rem 0.75rem;
            border-radius: 999px;
            font-size: 0.875rem;
            margin-top: 1rem;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>VM Offline</h1>
        <p>The virtual machine <code>%s</code> is currently stopped.</p>
        <span class="status">Stopped</span>
    </div>
</body>
</html>`

const vmUnreachableHTML = `<!DOCTYPE html>
<html>
<head>
    <title>VM Unreachable - %s</title>
    <meta http-equiv="refresh" content="5">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
        }
        .container {
            text-align: center;
            padding: 2rem;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            max-width: 400px;
        }
        h1 { color: #d69e2e; margin-bottom: 0.5rem; }
        p { color: #666; }
        code { background: #f0f0f0; padding: 0.2rem 0.5rem; border-radius: 4px; }
        .spinner {
            margin: 1rem auto;
            width: 24px;
            height: 24px;
            border: 3px solid #e2e8f0;
            border-top-color: #d69e2e;
            border-radius: 50%%;
            animation: spin 1s linear infinite;
        }
        @keyframes spin { to { transform: rotate(360deg); } }
        .hint { font-size: 0.875rem; color: #999; margin-top: 1rem; }
    </style>
</head>
<body>
    <div class="container">
        <h1>VM Starting...</h1>
        <p>The virtual machine <code>%s</code> is running but not yet responding.</p>
        <div class="spinner"></div>
        <p class="hint">This page will automatically refresh.</p>
    </div>
</body>
</html>`

// isVMSubdomain checks if the request is for a VM subdomain
// and returns the VM ID if so. Returns empty string for base domain,
// non-domain requests (localhost, IP, other hostnames), and returns
// error only for invalid subdomain patterns under the base domain.
func (s *Server) isVMSubdomain(r *http.Request) (string, error) {
	host := r.Host
	baseDomain := getBaseDomain()

	// Strip port if present for comparison
	hostWithoutPort := host
	if idx := strings.Index(host, ":"); idx != -1 {
		hostWithoutPort = host[:idx]
	}

	// If not related to our base domain at all, use normal routing
	// This handles localhost, IP addresses, and other hostnames
	if hostWithoutPort != baseDomain && !strings.HasSuffix(hostWithoutPort, "."+baseDomain) {
		return "", nil
	}

	// It's our domain - extract VM ID or validate pattern
	return extractVMID(host)
}

// proxyTimeout is the timeout for proxy connections
var proxyTimeout = 30 * time.Second
