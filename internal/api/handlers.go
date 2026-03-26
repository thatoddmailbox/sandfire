package api

import (
	"encoding/json"
	"net/http"

	"sandfire/internal/db"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListOSImages(w http.ResponseWriter, r *http.Request) {
	images, err := s.db.ListOSImages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if images == nil {
		images = []db.OSImage{}
	}
	writeJSON(w, http.StatusOK, images)
}

func (s *Server) handleListVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.db.ListVMs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vms == nil {
		vms = []db.VM{}
	}
	writeJSON(w, http.StatusOK, vms)
}

type createVMRequest struct {
	Name            string `json:"name"`
	OSImageID       string `json:"os_image_id"`
	RamMB           int    `json:"ram_mb"`
	DiskSizeGB      int    `json:"disk_size_gb"`
	VCPUCount       int    `json:"vcpu_count"`
	InternetEnabled bool   `json:"internet_enabled"`
}

func (s *Server) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	var req createVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.OSImageID == "" {
		writeError(w, http.StatusBadRequest, "os_image_id is required")
		return
	}

	// Check if VM with same name already exists
	existingVM, err := s.db.GetVMByName(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existingVM != nil {
		writeError(w, http.StatusConflict, "a VM with this name already exists")
		return
	}

	// Verify OS image exists
	img, err := s.db.GetOSImage(req.OSImageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if img == nil {
		writeError(w, http.StatusBadRequest, "os_image_id not found")
		return
	}

	// Set defaults
	if req.RamMB == 0 {
		req.RamMB = 512
	}
	if req.DiskSizeGB == 0 {
		req.DiskSizeGB = 8
	}
	if req.VCPUCount == 0 {
		req.VCPUCount = 1
	}

	vm, err := s.db.CreateVM(req.Name, req.OSImageID, req.RamMB, req.DiskSizeGB, req.VCPUCount, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Prepare VM disk
	if err := s.vmManager.PrepareVMDisk(vm.ID, img); err != nil {
		s.db.DeleteVM(vm.ID)
		writeError(w, http.StatusInternalServerError, "failed to prepare VM disk: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, vm)
}

func (s *Server) handleGetVM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vm == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

type updateVMRequest struct {
	Name            string `json:"name"`
	RamMB           int    `json:"ram_mb"`
	DiskSizeGB      int    `json:"disk_size_gb"`
	VCPUCount       int    `json:"vcpu_count"`
	InternetEnabled bool   `json:"internet_enabled"`
}

func (s *Server) handleUpdateVM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	if existing.State != "stopped" {
		writeError(w, http.StatusBadRequest, "vm must be stopped to modify")
		return
	}

	var req updateVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Use existing values for unset fields
	if req.Name == "" {
		req.Name = existing.Name
	}

	// Check if renaming to a name that already exists
	if req.Name != existing.Name {
		existingVM, err := s.db.GetVMByName(req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if existingVM != nil {
			writeError(w, http.StatusConflict, "a VM with this name already exists")
			return
		}
	}

	if req.RamMB == 0 {
		req.RamMB = existing.RamMB
	}
	if req.DiskSizeGB == 0 {
		req.DiskSizeGB = existing.DiskSizeGB
	}
	if req.VCPUCount == 0 {
		req.VCPUCount = existing.VCPUCount
	}

	vm, err := s.db.UpdateVM(id, req.Name, req.RamMB, req.DiskSizeGB, req.VCPUCount, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

func (s *Server) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	vm, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vm == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	if vm.State != "stopped" {
		writeError(w, http.StatusBadRequest, "vm must be stopped to delete")
		return
	}

	// Clean up VM resources
	if err := s.vmManager.CleanupVM(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cleanup VM: "+err.Error())
		return
	}

	// Clean up certificate if cert manager is configured
	if s.certManager != nil {
		if err := s.certManager.DeleteCertificate(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to cleanup certificate: "+err.Error())
			return
		}
	}

	if err := s.db.DeleteVM(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Maximum size for context JSON (64KB)
const MaxContextSize = 64 * 1024

type startVMRequest struct {
	Context json.RawMessage `json:"context,omitempty"`
}

func (s *Server) handleResetVMDisk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	vm, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vm == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	if vm.State != "stopped" {
		writeError(w, http.StatusBadRequest, "vm must be stopped to reset disk")
		return
	}

	img, err := s.db.GetOSImage(vm.OSImageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if img == nil {
		writeError(w, http.StatusInternalServerError, "os image not found")
		return
	}

	if err := s.vmManager.ResetVMDisk(id, img); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset VM disk: "+err.Error())
		return
	}

	vm, _ = s.db.GetVM(id)
	writeJSON(w, http.StatusOK, vm)
}

func (s *Server) handleStartVM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	vm, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vm == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	if vm.State == "running" {
		writeError(w, http.StatusBadRequest, "vm is already running")
		return
	}

	// Parse optional request body for context
	var req startVMRequest
	if r.ContentLength > 0 {
		if r.ContentLength > MaxContextSize {
			writeError(w, http.StatusBadRequest, "context too large (max 64KB)")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		// Validate context size (in case Content-Length was not accurate)
		if len(req.Context) > MaxContextSize {
			writeError(w, http.StatusBadRequest, "context too large (max 64KB)")
			return
		}
	}

	img, err := s.db.GetOSImage(vm.OSImageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if img == nil {
		writeError(w, http.StatusInternalServerError, "os image not found")
		return
	}

	ipAddress, tapDevice, err := s.vmManager.StartVM(vm, img, req.Context)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start VM: "+err.Error())
		return
	}

	if err := s.db.UpdateVMState(id, "running", &ipAddress, &tapDevice); err != nil {
		s.vmManager.StopVM(id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	vm, _ = s.db.GetVM(id)
	writeJSON(w, http.StatusOK, vm)
}

func (s *Server) handleStopVM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	vm, err := s.db.GetVM(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if vm == nil {
		writeError(w, http.StatusNotFound, "vm not found")
		return
	}
	if vm.State != "running" {
		writeError(w, http.StatusBadRequest, "vm is not running")
		return
	}

	if err := s.vmManager.StopVM(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stop VM: "+err.Error())
		return
	}

	if err := s.db.UpdateVMState(id, "stopped", nil, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	vm, _ = s.db.GetVM(id)
	writeJSON(w, http.StatusOK, vm)
}
