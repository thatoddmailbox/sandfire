package sshproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// VM represents a virtual machine from the Sandfire API
type VM struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	OSImageID       string    `json:"os_image_id"`
	RamMB           int       `json:"ram_mb"`
	DiskSizeGB      int       `json:"disk_size_gb"`
	VCPUCount       int       `json:"vcpu_count"`
	InternetEnabled bool      `json:"internet_enabled"`
	State           string    `json:"state"`
	IPAddress       *string   `json:"ip_address"`
	TapDevice       *string   `json:"tap_device"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// APIClient is a client for the Sandfire API
type APIClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAPIClient creates a new Sandfire API client
func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ListVMs returns all VMs from the Sandfire API
func (c *APIClient) ListVMs() ([]VM, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/vms")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var vms []VM
	if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
		return nil, fmt.Errorf("failed to decode VMs: %w", err)
	}

	return vms, nil
}

// GetVM returns a specific VM by ID
func (c *APIClient) GetVM(id string) (*VM, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/vms/" + id)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var vm VM
	if err := json.NewDecoder(resp.Body).Decode(&vm); err != nil {
		return nil, fmt.Errorf("failed to decode VM: %w", err)
	}

	return &vm, nil
}
