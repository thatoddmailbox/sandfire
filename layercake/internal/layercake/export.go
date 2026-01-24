package layercake

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Exporter handles exporting layers to sandfire
type Exporter struct {
	graph       *LayerGraph
	sandfireDir string
}

// NewExporter creates a new exporter
func NewExporter(graph *LayerGraph, sandfireDir string) *Exporter {
	return &Exporter{
		graph:       graph,
		sandfireDir: sandfireDir,
	}
}

// Export exports all EXPORT=true layers to sandfire
func (e *Exporter) Export() error {
	// Find exportable layers
	var exportable []*Layer
	for _, layer := range e.graph.All() {
		if layer.Export {
			// Check if built
			if _, err := os.Stat(layer.RootfsPath()); os.IsNotExist(err) {
				return fmt.Errorf("layer %s is marked for export but not built", layer.ID)
			}
			exportable = append(exportable, layer)
		}
	}

	if len(exportable) == 0 {
		fmt.Println("No layers marked for export")
		return nil
	}

	// Get base layer for kernel
	base := e.graph.GetBase()
	if base == nil {
		return fmt.Errorf("no base layer found")
	}
	if _, err := os.Stat(base.KernelPath()); os.IsNotExist(err) {
		return fmt.Errorf("base layer kernel not found")
	}

	// Open sandfire database
	dbPath := filepath.Join(e.sandfireDir, "sandfire.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sandfire database: %w", err)
	}
	defer db.Close()

	// Clean up old layercake exports
	fmt.Println("Cleaning up old layercake exports...")
	if err := e.cleanupOldExports(db); err != nil {
		return fmt.Errorf("failed to cleanup old exports: %w", err)
	}

	// Export each layer
	for _, layer := range exportable {
		if err := e.exportLayer(layer, base, db); err != nil {
			return fmt.Errorf("failed to export layer %s: %w", layer.ID, err)
		}
	}

	fmt.Printf("Exported %d layer(s) to sandfire\n", len(exportable))
	return nil
}

// cleanupOldExports removes all layercake-* images from sandfire
func (e *Exporter) cleanupOldExports(db *sql.DB) error {
	// Get list of layercake images to delete
	rows, err := db.Query("SELECT id FROM os_images WHERE id LIKE 'layercake-%'")
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}

	// Delete from database
	_, err = db.Exec("DELETE FROM os_images WHERE id LIKE 'layercake-%'")
	if err != nil {
		return err
	}

	// Delete directories
	imagesDir := filepath.Join(e.sandfireDir, "images")
	entries, err := os.ReadDir(imagesDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "layercake-") {
			dir := filepath.Join(imagesDir, entry.Name())
			fmt.Printf("Removing old export: %s\n", entry.Name())
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("failed to remove %s: %w", dir, err)
			}
		}
	}

	return nil
}

// exportLayer exports a single layer to sandfire
func (e *Exporter) exportLayer(layer *Layer, base *Layer, db *sql.DB) error {
	exportID := "layercake-" + layer.ID
	fmt.Printf("Exporting %s as %s...\n", layer.ID, exportID)

	// Create target directory
	targetDir := filepath.Join(e.sandfireDir, "images", exportID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Copy rootfs
	targetRootfs := filepath.Join(targetDir, "rootfs.ext4")
	fmt.Printf("  Copying rootfs...\n")
	if err := copyFile(layer.RootfsPath(), targetRootfs); err != nil {
		return fmt.Errorf("failed to copy rootfs: %w", err)
	}

	// Copy kernel from base
	targetKernel := filepath.Join(targetDir, "vmlinux")
	fmt.Printf("  Copying kernel...\n")
	if err := copyFile(base.KernelPath(), targetKernel); err != nil {
		return fmt.Errorf("failed to copy kernel: %w", err)
	}

	// Calculate rootfs size in GB (round up to nearest GB)
	// Use effective size which accounts for parent inheritance
	effectiveRootfsSizeMB := e.graph.GetEffectiveRootfsSizeMB(layer.ID)
	rootfsSizeGB := (effectiveRootfsSizeMB + 1023) / 1024
	suggestedRamMB := e.graph.GetEffectiveSuggestedRamMB(layer.ID)

	// Register in database
	fmt.Printf("  Registering in database...\n")
	_, err := db.Exec(`
		INSERT OR REPLACE INTO os_images (id, name, kernel_path, rootfs_path, rootfs_size_gb, suggested_ram_mb)
		VALUES (?, ?, ?, ?, ?, ?)
	`, exportID, layer.Name, targetKernel, targetRootfs, rootfsSizeGB, suggestedRamMB)
	if err != nil {
		return fmt.Errorf("failed to register in database: %w", err)
	}

	return nil
}
