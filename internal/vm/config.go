package vm

import "encoding/json"

// Firecracker configuration structures for JSON serialization

type FirecrackerConfig struct {
	BootSource    BootSource    `json:"boot-source"`
	Drives        []Drive       `json:"drives"`
	MachineConfig MachineConfig `json:"machine-config"`
	NetworkInterfaces []NetworkInterface `json:"network-interfaces,omitempty"`
}

type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

type Drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type MachineConfig struct {
	VCPUCount  int `json:"vcpu_count"`
	MemSizeMib int `json:"mem_size_mib"`
}

type NetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	GuestMAC    string `json:"guest_mac,omitempty"`
	HostDevName string `json:"host_dev_name"`
}

// MMDSConfig configures the Microvm Metadata Service
type MMDSConfig struct {
	NetworkInterfaces []string `json:"network_interfaces"`
	Version           string   `json:"version,omitempty"`
	IPv4Address       string   `json:"ipv4_address,omitempty"`
}

// MMDSMetadata is the metadata structure stored in MMDS
type MMDSMetadata struct {
	Sandfire SandfireMetadata `json:"sandfire"`
}

// SandfireMetadata contains Sandfire-specific VM metadata
type SandfireMetadata struct {
	VMID              string          `json:"vm_id"`
	VMName            string          `json:"vm_name"`
	ClaudeCredentials json.RawMessage `json:"claude_credentials,omitempty"`
}

func NewFirecrackerConfig(kernelPath, rootfsPath string, vcpuCount, ramMB int, tapDevice, guestMAC, guestIP string) *FirecrackerConfig {
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off random.trust_cpu=on random.trust_bootloader=on net.ifnames=0 biosdevname=0"
	if guestIP != "" {
		// Configure static IP via kernel command line
		// Format: ip=<client-ip>:<server-ip>:<gw-ip>:<netmask>:<hostname>:<device>:<autoconf>
		bootArgs += " ip=" + guestIP + "::10.20.30.1:255.255.255.0::eth0:off"
	}

	cfg := &FirecrackerConfig{
		BootSource: BootSource{
			KernelImagePath: kernelPath,
			BootArgs:        bootArgs,
		},
		Drives: []Drive{
			{
				DriveID:      "rootfs",
				PathOnHost:   rootfsPath,
				IsRootDevice: true,
				IsReadOnly:   false,
			},
		},
		MachineConfig: MachineConfig{
			VCPUCount:  vcpuCount,
			MemSizeMib: ramMB,
		},
	}

	if tapDevice != "" {
		cfg.NetworkInterfaces = []NetworkInterface{
			{
				IfaceID:     "eth0",
				GuestMAC:    guestMAC,
				HostDevName: tapDevice,
			},
		}
	}

	return cfg
}
