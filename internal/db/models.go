package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type OSImage struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	KernelPath     string    `json:"kernel_path"`
	RootfsPath     string    `json:"rootfs_path"`
	RootfsSizeGB   int       `json:"rootfs_size_gb"`
	SuggestedRamMB int       `json:"suggested_ram_mb"`
	CreatedAt      time.Time `json:"created_at"`
}

type VM struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	OSImageID       string     `json:"os_image_id"`
	RamMB           int        `json:"ram_mb"`
	DiskSizeGB      int        `json:"disk_size_gb"`
	VCPUCount       int        `json:"vcpu_count"`
	InternetEnabled bool       `json:"internet_enabled"`
	State           string     `json:"state"`
	IPAddress       *string    `json:"ip_address"`
	TapDevice       *string    `json:"tap_device"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// OS Image operations

func (db *DB) ListOSImages() ([]OSImage, error) {
	rows, err := db.Query(`SELECT id, name, kernel_path, rootfs_path, rootfs_size_gb, suggested_ram_mb, created_at FROM os_images ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query os_images: %w", err)
	}
	defer rows.Close()

	var images []OSImage
	for rows.Next() {
		var img OSImage
		if err := rows.Scan(&img.ID, &img.Name, &img.KernelPath, &img.RootfsPath, &img.RootfsSizeGB, &img.SuggestedRamMB, &img.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan os_image: %w", err)
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

func (db *DB) GetOSImage(id string) (*OSImage, error) {
	var img OSImage
	err := db.QueryRow(`SELECT id, name, kernel_path, rootfs_path, rootfs_size_gb, suggested_ram_mb, created_at FROM os_images WHERE id = ?`, id).
		Scan(&img.ID, &img.Name, &img.KernelPath, &img.RootfsPath, &img.RootfsSizeGB, &img.SuggestedRamMB, &img.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query os_image: %w", err)
	}
	return &img, nil
}

func (db *DB) CreateOSImage(name, kernelPath, rootfsPath string, rootfsSizeGB, suggestedRamMB int) (*OSImage, error) {
	id := uuid.New().String()
	_, err := db.Exec(`INSERT INTO os_images (id, name, kernel_path, rootfs_path, rootfs_size_gb, suggested_ram_mb) VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, kernelPath, rootfsPath, rootfsSizeGB, suggestedRamMB)
	if err != nil {
		return nil, fmt.Errorf("insert os_image: %w", err)
	}
	return db.GetOSImage(id)
}

func (db *DB) DeleteOSImage(id string) error {
	result, err := db.Exec(`DELETE FROM os_images WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete os_image: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("os_image not found")
	}
	return nil
}

// VM operations

func (db *DB) ListVMs() ([]VM, error) {
	rows, err := db.Query(`SELECT id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count,
		internet_enabled, state, ip_address, tap_device, created_at, updated_at
		FROM vms ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query vms: %w", err)
	}
	defer rows.Close()

	var vms []VM
	for rows.Next() {
		var vm VM
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.OSImageID, &vm.RamMB, &vm.DiskSizeGB,
			&vm.VCPUCount, &vm.InternetEnabled, &vm.State, &vm.IPAddress, &vm.TapDevice,
			&vm.CreatedAt, &vm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

func (db *DB) GetVM(id string) (*VM, error) {
	var vm VM
	err := db.QueryRow(`SELECT id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count,
		internet_enabled, state, ip_address, tap_device, created_at, updated_at
		FROM vms WHERE id = ?`, id).
		Scan(&vm.ID, &vm.Name, &vm.OSImageID, &vm.RamMB, &vm.DiskSizeGB,
			&vm.VCPUCount, &vm.InternetEnabled, &vm.State, &vm.IPAddress, &vm.TapDevice,
			&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query vm: %w", err)
	}
	return &vm, nil
}

func (db *DB) GetVMByName(name string) (*VM, error) {
	var vm VM
	err := db.QueryRow(`SELECT id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count,
		internet_enabled, state, ip_address, tap_device, created_at, updated_at
		FROM vms WHERE name = ?`, name).
		Scan(&vm.ID, &vm.Name, &vm.OSImageID, &vm.RamMB, &vm.DiskSizeGB,
			&vm.VCPUCount, &vm.InternetEnabled, &vm.State, &vm.IPAddress, &vm.TapDevice,
			&vm.CreatedAt, &vm.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query vm by name: %w", err)
	}
	return &vm, nil
}

func (db *DB) CreateVM(name, osImageID string, ramMB, diskSizeGB, vcpuCount int, internetEnabled bool) (*VM, error) {
	id := "vm-" + uuid.New().String()[:8]
	_, err := db.Exec(`INSERT INTO vms (id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count, internet_enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, osImageID, ramMB, diskSizeGB, vcpuCount, internetEnabled)
	if err != nil {
		return nil, fmt.Errorf("insert vm: %w", err)
	}
	return db.GetVM(id)
}

func (db *DB) UpdateVM(id string, name string, ramMB, diskSizeGB, vcpuCount int, internetEnabled bool) (*VM, error) {
	_, err := db.Exec(`UPDATE vms SET name = ?, ram_mb = ?, disk_size_gb = ?, vcpu_count = ?,
		internet_enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND state = 'stopped'`,
		name, ramMB, diskSizeGB, vcpuCount, internetEnabled, id)
	if err != nil {
		return nil, fmt.Errorf("update vm: %w", err)
	}
	return db.GetVM(id)
}

func (db *DB) UpdateVMState(id, state string, ipAddress, tapDevice *string) error {
	_, err := db.Exec(`UPDATE vms SET state = ?, ip_address = ?, tap_device = ?,
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		state, ipAddress, tapDevice, id)
	if err != nil {
		return fmt.Errorf("update vm state: %w", err)
	}
	return nil
}

func (db *DB) DeleteVM(id string) error {
	result, err := db.Exec(`DELETE FROM vms WHERE id = ? AND state = 'stopped'`, id)
	if err != nil {
		return fmt.Errorf("delete vm: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("vm not found or not stopped")
	}
	return nil
}

func (db *DB) GetRunningVMs() ([]VM, error) {
	rows, err := db.Query(`SELECT id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count,
		internet_enabled, state, ip_address, tap_device, created_at, updated_at
		FROM vms WHERE state = 'running'`)
	if err != nil {
		return nil, fmt.Errorf("query running vms: %w", err)
	}
	defer rows.Close()

	var vms []VM
	for rows.Next() {
		var vm VM
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.OSImageID, &vm.RamMB, &vm.DiskSizeGB,
			&vm.VCPUCount, &vm.InternetEnabled, &vm.State, &vm.IPAddress, &vm.TapDevice,
			&vm.CreatedAt, &vm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

func (db *DB) GetAllocatedIPs() ([]string, error) {
	rows, err := db.Query(`SELECT ip_address FROM vms WHERE ip_address IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("query allocated ips: %w", err)
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("scan ip: %w", err)
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

// GetVMsToRestart returns VMs that were running before shutdown and need to be restarted
func (db *DB) GetVMsToRestart() ([]VM, error) {
	rows, err := db.Query(`SELECT id, name, os_image_id, ram_mb, disk_size_gb, vcpu_count,
		internet_enabled, state, ip_address, tap_device, created_at, updated_at
		FROM vms WHERE was_running = 1`)
	if err != nil {
		return nil, fmt.Errorf("query vms to restart: %w", err)
	}
	defer rows.Close()

	var vms []VM
	for rows.Next() {
		var vm VM
		if err := rows.Scan(&vm.ID, &vm.Name, &vm.OSImageID, &vm.RamMB, &vm.DiskSizeGB,
			&vm.VCPUCount, &vm.InternetEnabled, &vm.State, &vm.IPAddress, &vm.TapDevice,
			&vm.CreatedAt, &vm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

// MarkRunningVMsForRestart sets was_running=true for all currently running VMs
func (db *DB) MarkRunningVMsForRestart() error {
	_, err := db.Exec(`UPDATE vms SET was_running = 1 WHERE state = 'running'`)
	if err != nil {
		return fmt.Errorf("mark running vms for restart: %w", err)
	}
	return nil
}

// ClearWasRunning clears the was_running flag for a VM after it has been restarted
func (db *DB) ClearWasRunning(id string) error {
	_, err := db.Exec(`UPDATE vms SET was_running = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("clear was_running flag: %w", err)
	}
	return nil
}

// PrepareOrphanedVMsForRestart handles VMs that were left in 'running' state
// after an unclean shutdown. It marks them for restart and clears their stale
// network info so new IPs can be allocated.
func (db *DB) PrepareOrphanedVMsForRestart() (int, error) {
	// Find VMs that are in 'running' state - these were running when the server
	// crashed since graceful shutdown would have set state='stopped'
	result, err := db.Exec(`UPDATE vms SET was_running = 1, ip_address = NULL,
		tap_device = NULL WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("prepare orphaned vms: %w", err)
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// ClearStaleIPAllocations clears IP addresses from VMs that will be restarted
// This allows them to get fresh IPs during restart
func (db *DB) ClearStaleIPAllocations() error {
	_, err := db.Exec(`UPDATE vms SET ip_address = NULL, tap_device = NULL
		WHERE was_running = 1`)
	if err != nil {
		return fmt.Errorf("clear stale ip allocations: %w", err)
	}
	return nil
}
