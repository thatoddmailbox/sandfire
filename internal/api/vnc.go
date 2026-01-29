package api

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
)

const vncPort = "5900"

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

func (s *Server) handleVNCPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Validate VM exists
	vm, err := s.db.GetVM(id)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if vm == nil {
		http.Error(w, "VM not found", http.StatusNotFound)
		return
	}

	// Serve the VNC HTML page
	content, err := os.ReadFile("static/vnc.html")
	if err != nil {
		http.Error(w, "VNC viewer not available", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func (s *Server) handleVNCWebSocket(w http.ResponseWriter, r *http.Request) {
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
	if vm.IPAddress == nil {
		writeError(w, http.StatusInternalServerError, "vm has no IP address")
		return
	}

	// Connect to VNC server
	vncAddr := net.JoinHostPort(*vm.IPAddress, vncPort)
	vncConn, err := net.Dial("tcp", vncAddr)
	if err != nil {
		log.Printf("VNC proxy: failed to connect to %s: %v", vncAddr, err)
		writeError(w, http.StatusBadGateway, "failed to connect to VNC server")
		return
	}
	defer vncConn.Close()

	// Upgrade to WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("VNC proxy: WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	log.Printf("VNC proxy: connected %s -> %s", r.RemoteAddr, vncAddr)

	// Bidirectional proxy
	var wg sync.WaitGroup
	wg.Add(2)

	// VNC -> WebSocket
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := vncConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("VNC proxy: read from VNC failed: %v", err)
				}
				ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				log.Printf("VNC proxy: write to WebSocket failed: %v", err)
				return
			}
		}
	}()

	// WebSocket -> VNC
	go func() {
		defer wg.Done()
		for {
			messageType, data, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("VNC proxy: read from WebSocket failed: %v", err)
				}
				return
			}
			if messageType == websocket.BinaryMessage {
				if _, err := vncConn.Write(data); err != nil {
					log.Printf("VNC proxy: write to VNC failed: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	log.Printf("VNC proxy: disconnected %s", r.RemoteAddr)
}
