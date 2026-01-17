package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sandfire/internal/db"
	"sandfire/internal/vm"
)

type Server struct {
	db        *db.DB
	vmManager *vm.Manager
	mux       *http.ServeMux
}

func NewServer(database *db.DB, vmManager *vm.Manager) *Server {
	s := &Server{
		db:        database,
		vmManager: vmManager,
		mux:       http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)

	s.mux.HandleFunc("GET /api/os-images", s.handleListOSImages)

	s.mux.HandleFunc("GET /api/vms", s.handleListVMs)
	s.mux.HandleFunc("POST /api/vms", s.handleCreateVM)
	s.mux.HandleFunc("GET /api/vms/{id}", s.handleGetVM)
	s.mux.HandleFunc("PUT /api/vms/{id}", s.handleUpdateVM)
	s.mux.HandleFunc("DELETE /api/vms/{id}", s.handleDeleteVM)
	s.mux.HandleFunc("POST /api/vms/{id}/start", s.handleStartVM)
	s.mux.HandleFunc("POST /api/vms/{id}/stop", s.handleStopVM)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wrapped := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	s.mux.ServeHTTP(wrapped, r)
	log.Printf("%s %s %d", r.Method, r.URL.Path, wrapped.statusCode)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
