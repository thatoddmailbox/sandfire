package certs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

const (
	// LetsEncryptProduction is the production ACME directory
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	// LetsEncryptStaging is the staging ACME directory (for testing)
	LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

	// CloudflareAPIBase is the base URL for Cloudflare API
	CloudflareAPIBase = "https://api.cloudflare.com/client/v4"

	// RenewalThreshold is how long before expiry to renew certificates
	RenewalThreshold = 30 * 24 * time.Hour // 30 days
)

// Manager handles certificate issuance and storage
type Manager struct {
	dataDir         string
	baseDomain      string
	cloudflareToken string
	zoneID          string
	acmeClient      *acme.Client
	accountKey      *ecdsa.PrivateKey

	// mu protects concurrent certificate operations
	mu sync.Mutex

	// inProgress tracks domains currently being issued to prevent duplicate requests
	inProgress map[string]chan struct{}
}

// NewManager creates a new certificate manager
func NewManager(dataDir, baseDomain, cloudflareToken string, staging bool) (*Manager, error) {
	certsDir := filepath.Join(dataDir, "certs")
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return nil, fmt.Errorf("create certs directory: %w", err)
	}

	// Load or create account key
	accountKey, err := loadOrCreateAccountKey(filepath.Join(certsDir, "account.key"))
	if err != nil {
		return nil, fmt.Errorf("load account key: %w", err)
	}

	directory := LetsEncryptProduction
	if staging {
		directory = LetsEncryptStaging
		log.Println("certs: using Let's Encrypt staging environment")
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: directory,
	}

	m := &Manager{
		dataDir:         certsDir,
		baseDomain:      baseDomain,
		cloudflareToken: cloudflareToken,
		acmeClient:      client,
		accountKey:      accountKey,
		inProgress:      make(map[string]chan struct{}),
	}

	// Look up the zone ID for the base domain
	zoneID, err := m.getCloudflareZoneID()
	if err != nil {
		return nil, fmt.Errorf("get cloudflare zone ID: %w", err)
	}
	m.zoneID = zoneID
	log.Printf("certs: cloudflare zone ID for %s: %s", baseDomain, zoneID)

	return m, nil
}

// GetCertificate returns the certificate for a VM domain, issuing one if needed.
// The vmID should be like "vm-e2aa90c4".
// Returns the PEM-encoded certificate chain and private key concatenated.
func (m *Manager) GetCertificate(ctx context.Context, vmID string) ([]byte, error) {
	m.mu.Lock()

	// Check if another goroutine is already issuing this certificate
	if ch, ok := m.inProgress[vmID]; ok {
		m.mu.Unlock()
		// Wait for the other goroutine to finish
		select {
		case <-ch:
			// Other goroutine finished, try to load the cert
			return m.loadCertificate(vmID)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Check if we already have a valid certificate
	certData, err := m.loadCertificate(vmID)
	if err == nil {
		m.mu.Unlock()
		return certData, nil
	}

	// Need to issue a new certificate
	// Mark as in progress
	ch := make(chan struct{})
	m.inProgress[vmID] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.inProgress, vmID)
		close(ch)
		m.mu.Unlock()
	}()

	// Issue new certificate
	certData, err = m.issueCertificate(ctx, vmID)
	if err != nil {
		return nil, fmt.Errorf("issue certificate: %w", err)
	}

	return certData, nil
}

// loadCertificate loads and validates an existing certificate
func (m *Manager) loadCertificate(vmID string) ([]byte, error) {
	certPath := filepath.Join(m.dataDir, vmID+".pem")

	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}

	// Parse and check validity
	cert, err := m.parseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	// Check if certificate needs renewal
	if time.Until(cert.NotAfter) < RenewalThreshold {
		return nil, fmt.Errorf("certificate expires soon: %v", cert.NotAfter)
	}

	log.Printf("certs: loaded valid certificate for %s (expires %v)", vmID, cert.NotAfter)
	return data, nil
}

// parseCertificate extracts the first certificate from PEM data
func (m *Manager) parseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate found in PEM data")
	}
	return x509.ParseCertificate(block.Bytes)
}

// challengeInfo holds information about a pending DNS challenge
type challengeInfo struct {
	authzURL        string
	challenge       *acme.Challenge
	challengeDomain string
	keyAuth         string
	recordID        string
}

// issueCertificate requests a new certificate from Let's Encrypt
func (m *Manager) issueCertificate(ctx context.Context, vmID string) ([]byte, error) {
	// Domains for the certificate
	baseDomain := fmt.Sprintf("%s.%s", vmID, m.baseDomain)
	wildcardDomain := fmt.Sprintf("*.%s.%s", vmID, m.baseDomain)
	domains := []string{baseDomain, wildcardDomain}

	log.Printf("certs: issuing certificate for %v", domains)

	// Register account if needed
	acct := &acme.Account{}
	_, err := m.acmeClient.Register(ctx, acct, acme.AcceptTOS)
	if err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("register account: %w", err)
	}

	// Create order
	order, err := m.acmeClient.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
	if err != nil {
		return nil, fmt.Errorf("authorize order: %w", err)
	}

	// Phase 1: Collect all challenges
	var challenges []challengeInfo
	for _, authzURL := range order.AuthzURLs {
		authz, err := m.acmeClient.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, fmt.Errorf("get authorization: %w", err)
		}

		if authz.Status == acme.StatusValid {
			log.Printf("certs: authorization already valid for %s", authz.Identifier.Value)
			continue
		}

		// Find DNS-01 challenge
		var dnsChallenge *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				dnsChallenge = c
				break
			}
		}
		if dnsChallenge == nil {
			return nil, fmt.Errorf("no dns-01 challenge found for %s", authz.Identifier.Value)
		}

		// Get the DNS record value
		keyAuth, err := m.acmeClient.DNS01ChallengeRecord(dnsChallenge.Token)
		if err != nil {
			return nil, fmt.Errorf("get DNS challenge record: %w", err)
		}

		// The challenge domain for wildcard is the same as for the base domain
		challengeDomain := "_acme-challenge." + strings.TrimPrefix(authz.Identifier.Value, "*.")

		challenges = append(challenges, challengeInfo{
			authzURL:        authzURL,
			challenge:       dnsChallenge,
			challengeDomain: challengeDomain,
			keyAuth:         keyAuth,
		})
	}

	if len(challenges) == 0 {
		log.Printf("certs: all authorizations already valid, finalizing order")
	}

	// Phase 2: Create all DNS records
	for i := range challenges {
		recordID, err := m.createDNSRecord(ctx, challenges[i].challengeDomain, challenges[i].keyAuth)
		if err != nil {
			// Clean up any records we already created
			for j := 0; j < i; j++ {
				if challenges[j].recordID != "" {
					m.deleteDNSRecord(ctx, challenges[j].recordID)
				}
			}
			return nil, fmt.Errorf("create DNS record for %s: %w", challenges[i].challengeDomain, err)
		}
		challenges[i].recordID = recordID
	}

	// Helper to clean up all DNS records
	cleanupRecords := func() {
		for _, c := range challenges {
			if c.recordID != "" {
				if err := m.deleteDNSRecord(ctx, c.recordID); err != nil {
					log.Printf("certs: warning: failed to delete DNS record %s: %v", c.recordID, err)
				}
			}
		}
	}

	// Phase 3: Wait for DNS propagation by polling DNS servers
	if len(challenges) > 0 {
		log.Printf("certs: waiting for DNS propagation...")
		for _, c := range challenges {
			if err := m.waitForDNSPropagation(ctx, c.challengeDomain, c.keyAuth); err != nil {
				cleanupRecords()
				return nil, fmt.Errorf("DNS propagation failed for %s: %w", c.challengeDomain, err)
			}
		}
		log.Printf("certs: DNS propagation verified")
	}

	// Phase 4: Accept all challenges
	for _, c := range challenges {
		_, err := m.acmeClient.Accept(ctx, c.challenge)
		if err != nil {
			cleanupRecords()
			return nil, fmt.Errorf("accept challenge for %s: %w", c.challengeDomain, err)
		}
		log.Printf("certs: accepted challenge for %s", c.challengeDomain)
	}

	// Phase 5: Wait for all authorizations to be valid
	for _, c := range challenges {
		_, err := m.acmeClient.WaitAuthorization(ctx, c.authzURL)
		if err != nil {
			cleanupRecords()
			return nil, fmt.Errorf("wait authorization for %s: %w", c.challengeDomain, err)
		}
		log.Printf("certs: authorization valid for %s", c.challengeDomain)
	}

	// Phase 6: Clean up DNS records
	cleanupRecords()

	// Phase 7: Generate certificate key and finalize order
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate cert key: %w", err)
	}

	// Create CSR
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		DNSNames: domains,
	}, certKey)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}

	// Finalize order
	derCerts, _, err := m.acmeClient.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("create order cert: %w", err)
	}

	// Encode certificate and key to PEM
	var buf bytes.Buffer

	// Write certificates
	for _, der := range derCerts {
		pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}

	// Write private key
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	pem.Encode(&buf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Save to file
	certPath := filepath.Join(m.dataDir, vmID+".pem")
	if err := os.WriteFile(certPath, buf.Bytes(), 0600); err != nil {
		return nil, fmt.Errorf("save certificate: %w", err)
	}

	log.Printf("certs: issued certificate for %v, saved to %s", domains, certPath)
	return buf.Bytes(), nil
}

// loadOrCreateAccountKey loads or creates the ACME account private key
func loadOrCreateAccountKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("invalid PEM data")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	// Generate new key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	// Save key
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemData, 0600); err != nil {
		return nil, err
	}

	log.Printf("certs: created new account key at %s", path)
	return key, nil
}

// waitForDNSPropagation polls DNS servers until the TXT record is visible
func (m *Manager) waitForDNSPropagation(ctx context.Context, domain, expectedValue string) error {
	// DNS servers to check - using authoritative Cloudflare nameservers
	// and Google's public DNS for broader verification
	dnsServers := []string{
		"1.1.1.1:53",  // Cloudflare primary
		"1.0.0.1:53",  // Cloudflare secondary
		"8.8.8.8:53",  // Google (used by Let's Encrypt)
	}

	timeout := 60 * time.Second
	pollInterval := 500 * time.Millisecond

	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempt++
		allFound := true

		for _, server := range dnsServers {
			found, err := m.checkDNSTXT(server, domain, expectedValue)
			if err != nil {
				log.Printf("certs: DNS check error for %s via %s: %v", domain, server, err)
				allFound = false
				continue
			}
			if !found {
				allFound = false
			}
		}

		if allFound {
			log.Printf("certs: DNS record visible for %s after %d attempts (%.1fs)",
				domain, attempt, time.Since(deadline.Add(-timeout)).Seconds())
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for DNS propagation after %v", timeout)
}

// checkDNSTXT queries a specific DNS server for TXT records
func (m *Manager) checkDNSTXT(dnsServer, domain, expectedValue string) (bool, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", dnsServer)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	records, err := resolver.LookupTXT(ctx, domain)
	if err != nil {
		// NXDOMAIN or other errors mean the record isn't there yet
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, nil
		}
		return false, err
	}

	for _, record := range records {
		if record == expectedValue {
			return true, nil
		}
	}

	return false, nil
}

// Cloudflare API helpers

type cloudflareResponse struct {
	Success bool              `json:"success"`
	Errors  []cloudflareError `json:"errors"`
	Result  json.RawMessage   `json:"result"`
}

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

func (m *Manager) cloudflareRequest(ctx context.Context, method, path string, body interface{}) (*cloudflareResponse, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, CloudflareAPIBase+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+m.cloudflareToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cfResp cloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, err
	}

	if !cfResp.Success {
		if len(cfResp.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare API error: %s", cfResp.Errors[0].Message)
		}
		return nil, errors.New("cloudflare API error: unknown")
	}

	return &cfResp, nil
}

func (m *Manager) getCloudflareZoneID() (string, error) {
	// Extract the parent domain from baseDomain
	// e.g., "sand.example.com" -> look for "example.com" zone
	parts := strings.Split(m.baseDomain, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid base domain: %s", m.baseDomain)
	}
	parentDomain := strings.Join(parts[len(parts)-2:], ".")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := m.cloudflareRequest(ctx, "GET", "/zones?name="+parentDomain, nil)
	if err != nil {
		return "", err
	}

	var zones []cloudflareZone
	if err := json.Unmarshal(resp.Result, &zones); err != nil {
		return "", err
	}

	if len(zones) == 0 {
		return "", fmt.Errorf("zone not found for %s", parentDomain)
	}

	return zones[0].ID, nil
}

func (m *Manager) createDNSRecord(ctx context.Context, name, content string) (string, error) {
	record := map[string]interface{}{
		"type":    "TXT",
		"name":    name,
		"content": content,
		"ttl":     60,
	}

	resp, err := m.cloudflareRequest(ctx, "POST", "/zones/"+m.zoneID+"/dns_records", record)
	if err != nil {
		return "", err
	}

	var result cloudflareDNSRecord
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", err
	}

	log.Printf("certs: created DNS record %s -> %s (id: %s)", name, content, result.ID)
	return result.ID, nil
}

func (m *Manager) deleteDNSRecord(ctx context.Context, recordID string) error {
	_, err := m.cloudflareRequest(ctx, "DELETE", "/zones/"+m.zoneID+"/dns_records/"+recordID, nil)
	if err != nil {
		return err
	}
	log.Printf("certs: deleted DNS record %s", recordID)
	return nil
}

// DeleteCertificate removes the certificate file for a VM
func (m *Manager) DeleteCertificate(vmID string) error {
	certPath := filepath.Join(m.dataDir, vmID+".pem")
	err := os.Remove(certPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete certificate: %w", err)
	}
	if err == nil {
		log.Printf("certs: deleted certificate for %s", vmID)
	}
	return nil
}

// GetCertificateForTLS is a helper that returns a tls.Certificate
func (m *Manager) GetCertificateForTLS(ctx context.Context, vmID string) (*tls.Certificate, error) {
	data, err := m.GetCertificate(ctx, vmID)
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(data, data)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	return &cert, nil
}
