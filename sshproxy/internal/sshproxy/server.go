package sshproxy

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	vmUsername = "sandfire"
	vmPassword = "sandfire"
)

// ptyRequestMsg contains the parsed PTY request data
type ptyRequestMsg struct {
	Term     string
	Columns  uint32
	Rows     uint32
	Width    uint32
	Height   uint32
	Modes    ssh.TerminalModes
}

// Server is the SSH proxy server
type Server struct {
	hostKey    ssh.Signer
	apiClient  *APIClient
	listenAddr string
}

// NewServer creates a new SSH proxy server
func NewServer(hostKey ssh.Signer, apiClient *APIClient, listenAddr string) *Server {
	return &Server{
		hostKey:    hostKey,
		apiClient:  apiClient,
		listenAddr: listenAddr,
	}
}

// Start starts the SSH server
func (s *Server) Start() error {
	config := &ssh.ServerConfig{
		PublicKeyCallback: s.authenticatePublicKey,
	}
	config.AddHostKey(s.hostKey)

	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.listenAddr, err)
	}
	defer listener.Close()

	log.Printf("SSH proxy listening on %s", s.listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go s.handleConnection(conn, config)
	}
}

// authenticatePublicKey validates the user's public key against their ~/.ssh/authorized_keys
func (s *Server) authenticatePublicKey(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	username := conn.User()

	// Look up the system user
	u, err := user.Lookup(username)
	if err != nil {
		log.Printf("Auth failed for %s: user not found", username)
		return nil, fmt.Errorf("user not found")
	}

	// Read their authorized_keys file
	authKeysPath := filepath.Join(u.HomeDir, ".ssh", "authorized_keys")
	authKeysFile, err := os.Open(authKeysPath)
	if err != nil {
		log.Printf("Auth failed for %s: cannot read authorized_keys: %v", username, err)
		return nil, fmt.Errorf("cannot read authorized_keys")
	}
	defer authKeysFile.Close()

	// Get the marshaled form of the presented key for comparison
	presentedKey := key.Marshal()

	// Scan through authorized_keys line by line
	scanner := bufio.NewScanner(authKeysFile)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())

		// Skip empty lines and comments
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		// Parse the authorized key
		authorizedKey, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			// Skip malformed lines
			continue
		}

		// Compare the keys
		if bytes.Equal(authorizedKey.Marshal(), presentedKey) {
			log.Printf("Auth successful for %s from %s", username, conn.RemoteAddr())
			return &ssh.Permissions{
				Extensions: map[string]string{
					"username": username,
				},
			}, nil
		}
	}

	log.Printf("Auth failed for %s: no matching key found", username)
	return nil, fmt.Errorf("no matching key found")
}

func (s *Server) handleConnection(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		log.Printf("SSH handshake failed: %v", err)
		return
	}
	defer sshConn.Close()

	log.Printf("New SSH connection from %s (%s)", sshConn.RemoteAddr(), sshConn.ClientVersion())

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	// Handle channels
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("Could not accept channel: %v", err)
			continue
		}

		go s.handleSession(channel, requests)
	}
}

func (s *Server) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	var execCmd string
	var ptyReq *ptyRequestMsg

	// We need to handle requests in a goroutine for window-change events during proxy
	// But first, collect the initial setup requests
	for req := range requests {
		switch req.Type {
		case "exec":
			// Direct exec mode: ssh -p 2222 user@host connect vm-xxx
			if len(req.Payload) >= 4 {
				cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 |
					uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
				if len(req.Payload) >= int(4+cmdLen) {
					execCmd = string(req.Payload[4 : 4+cmdLen])
				}
			}
			req.Reply(true, nil)
			cmd := strings.TrimSpace(execCmd)
			// Run the command
			if s.handleCommand(channel, cmd, ptyReq, requests) {
				// Command wants to continue - enter interactive shell
				s.handleInteractiveShell(channel, ptyReq, requests)
			}
			return

		case "shell":
			req.Reply(true, nil)
			s.handleInteractiveShell(channel, ptyReq, requests)
			return

		case "pty-req":
			ptyReq = parsePtyRequest(req.Payload)
			req.Reply(true, nil)

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func parsePtyRequest(payload []byte) *ptyRequestMsg {
	if len(payload) < 4 {
		return nil
	}

	msg := &ptyRequestMsg{}

	// Parse term string (length-prefixed)
	termLen := binary.BigEndian.Uint32(payload[0:4])
	if len(payload) < int(4+termLen+16) {
		return nil
	}
	msg.Term = string(payload[4 : 4+termLen])
	offset := 4 + termLen

	// Parse dimensions
	msg.Columns = binary.BigEndian.Uint32(payload[offset : offset+4])
	msg.Rows = binary.BigEndian.Uint32(payload[offset+4 : offset+8])
	msg.Width = binary.BigEndian.Uint32(payload[offset+8 : offset+12])
	msg.Height = binary.BigEndian.Uint32(payload[offset+12 : offset+16])
	offset += 16

	// Parse terminal modes (length-prefixed, then encoded mode pairs)
	if len(payload) >= int(offset+4) {
		modesLen := binary.BigEndian.Uint32(payload[offset : offset+4])
		offset += 4
		if len(payload) >= int(offset)+int(modesLen) {
			msg.Modes = parseTerminalModes(payload[offset : offset+modesLen])
		}
	}

	// If no modes were parsed, use sensible defaults
	if msg.Modes == nil {
		msg.Modes = ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
	}

	return msg
}

// parseTerminalModes decodes SSH terminal modes from the encoded format.
// Format: repeated (opcode uint8, value uint32) pairs, terminated by opcode 0.
func parseTerminalModes(data []byte) ssh.TerminalModes {
	modes := make(ssh.TerminalModes)
	for len(data) >= 5 {
		opcode := data[0]
		if opcode == 0 { // TTY_OP_END
			break
		}
		value := binary.BigEndian.Uint32(data[1:5])
		modes[opcode] = value
		data = data[5:]
	}
	return modes
}

func (s *Server) handleInteractiveShell(channel ssh.Channel, ptyReq *ptyRequestMsg, requests <-chan *ssh.Request) {
	// Drain any remaining requests in background
	go func() {
		for req := range requests {
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}()

	s.printWelcome(channel)

	// Channel for input data from reader goroutine
	inputChan := make(chan []byte, 10)
	inputErr := make(chan error, 1)

	// Reader goroutine - reads from channel and sends to inputChan
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := channel.Read(buf)
			if err != nil {
				inputErr <- err
				return
			}
			inputChan <- append([]byte(nil), buf[:n]...)
		}
	}()

	var line []byte

	// Channel for sending input to proxy mode
	var proxyInput chan []byte
	var proxyDone chan struct{}

	for {
		fmt.Fprintf(channel, "sandfire> ")

	inputLoop:
		for {
			select {
			case data := <-inputChan:
				// If we're in proxy mode, forward input there
				if proxyInput != nil {
					select {
					case proxyInput <- data:
						continue inputLoop
					case <-proxyDone:
						// Proxy ended while we were trying to send
						proxyInput = nil
						proxyDone = nil
						fmt.Fprintf(channel, "\r\nDisconnected from VM.\r\n")
						break inputLoop
					}
				}

				for i := 0; i < len(data); i++ {
					b := data[i]

					switch b {
					case '\r', '\n':
						fmt.Fprintf(channel, "\r\n")
						cmd := strings.TrimSpace(string(line))
						line = nil

						if cmd == "exit" || cmd == "quit" {
							fmt.Fprintf(channel, "Goodbye!\r\n")
							return
						}

						if cmd != "" {
							// Check if this is a connect command
							parts := strings.Fields(cmd)
							if len(parts) >= 2 && parts[0] == "connect" {
								// Start proxy mode synchronously
								proxyInput = make(chan []byte, 10)
								proxyDone = make(chan struct{})
								go func(vmID string, input chan []byte, done chan struct{}) {
									defer close(done)
									s.handleConnectWithInput(channel, vmID, ptyReq, input)
								}(parts[1], proxyInput, proxyDone)
								continue inputLoop
							}

							if !s.handleCommand(channel, cmd, ptyReq, requests) {
								return
							}
						}
						break inputLoop

					case 127, 8: // Backspace
						if len(line) > 0 {
							line = line[:len(line)-1]
							fmt.Fprintf(channel, "\b \b")
						}

					case 3: // Ctrl+C
						fmt.Fprintf(channel, "^C\r\n")
						line = nil
						break inputLoop

					case 4: // Ctrl+D
						fmt.Fprintf(channel, "\r\nGoodbye!\r\n")
						return

					default:
						if b >= 32 && b < 127 {
							line = append(line, b)
							channel.Write([]byte{b})
						}
					}
				}

			case <-proxyDone:
				// Proxy ended
				proxyInput = nil
				proxyDone = nil
				fmt.Fprintf(channel, "\r\nDisconnected from VM.\r\n")
				break inputLoop

			case err := <-inputErr:
				if err != io.EOF {
					log.Printf("Read error: %v", err)
				}
				return
			}
		}
	}
}

func (s *Server) printWelcome(channel ssh.Channel) {
	fmt.Fprintf(channel, "\r\n")
	fmt.Fprintf(channel, "===========================================\r\n")
	fmt.Fprintf(channel, "  Sandfire SSH Proxy\r\n")
	fmt.Fprintf(channel, "===========================================\r\n")
	fmt.Fprintf(channel, "\r\n")
	fmt.Fprintf(channel, "Commands:\r\n")
	fmt.Fprintf(channel, "  list              - List all VMs\r\n")
	fmt.Fprintf(channel, "  connect <vm-id>   - Connect to a VM\r\n")
	fmt.Fprintf(channel, "  help              - Show this help\r\n")
	fmt.Fprintf(channel, "  exit              - Exit the proxy\r\n")
	fmt.Fprintf(channel, "\r\n")
}

// handleCommand handles a command. Returns false if the session should end.
func (s *Server) handleCommand(channel ssh.Channel, cmd string, ptyReq *ptyRequestMsg, requests <-chan *ssh.Request) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return true
	}

	switch parts[0] {
	case "help":
		s.printWelcome(channel)
		return true

	case "list":
		s.handleList(channel)
		return true

	case "connect":
		if len(parts) < 2 {
			fmt.Fprintf(channel, "Usage: connect <vm-id>\r\n")
			return true
		}
		s.handleConnect(channel, parts[1], ptyReq, requests)
		return true // Always continue shell after connect (success or failure)

	default:
		fmt.Fprintf(channel, "Unknown command: %s\r\n", parts[0])
		fmt.Fprintf(channel, "Type 'help' for available commands.\r\n")
		return true
	}
}

func (s *Server) handleList(channel ssh.Channel) {
	vms, err := s.apiClient.ListVMs()
	if err != nil {
		fmt.Fprintf(channel, "Error listing VMs: %v\r\n", err)
		return
	}

	if len(vms) == 0 {
		fmt.Fprintf(channel, "No VMs found.\r\n")
		return
	}

	fmt.Fprintf(channel, "\r\n")
	fmt.Fprintf(channel, "%-15s %-20s %-10s %-15s\r\n", "ID", "NAME", "STATE", "IP")
	fmt.Fprintf(channel, "%-15s %-20s %-10s %-15s\r\n", "---", "----", "-----", "--")

	for _, vm := range vms {
		ip := "-"
		if vm.IPAddress != nil {
			ip = *vm.IPAddress
		}
		name := vm.Name
		if len(name) > 18 {
			name = name[:18] + ".."
		}
		fmt.Fprintf(channel, "%-15s %-20s %-10s %-15s\r\n", vm.ID, name, vm.State, ip)
	}
	fmt.Fprintf(channel, "\r\n")
}

// handleConnectWithInput handles connect command with input provided via channel
func (s *Server) handleConnectWithInput(channel ssh.Channel, vmID string, ptyReq *ptyRequestMsg, input <-chan []byte) {
	// Look up the VM
	vm, err := s.apiClient.GetVM(vmID)
	if err != nil {
		fmt.Fprintf(channel, "Error looking up VM: %v\r\n", err)
		return
	}

	if vm == nil {
		fmt.Fprintf(channel, "VM not found: %s\r\n", vmID)
		return
	}

	if vm.State != "running" {
		fmt.Fprintf(channel, "VM is not running (state: %s)\r\n", vm.State)
		return
	}

	if vm.IPAddress == nil {
		fmt.Fprintf(channel, "VM has no IP address assigned\r\n")
		return
	}

	fmt.Fprintf(channel, "Connecting to %s (%s) at %s...\r\n", vm.Name, vm.ID, *vm.IPAddress)

	// Connect to the VM's SSH server
	vmAddr := fmt.Sprintf("%s:22", *vm.IPAddress)
	s.proxySSHWithInput(channel, vmAddr, ptyReq, input)
}

func (s *Server) proxySSHWithInput(channel ssh.Channel, vmAddr string, ptyReq *ptyRequestMsg, input <-chan []byte) {
	config := &ssh.ClientConfig{
		User: vmUsername,
		Auth: []ssh.AuthMethod{
			ssh.Password(vmPassword),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", vmAddr, config)
	if err != nil {
		fmt.Fprintf(channel, "Failed to connect to VM: %v\r\n", err)
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(channel, "Failed to create session: %v\r\n", err)
		return
	}
	defer session.Close()

	// Determine terminal settings from client's PTY request
	term := "xterm-256color"
	rows := 24
	cols := 80
	var modes ssh.TerminalModes
	if ptyReq != nil {
		if ptyReq.Term != "" {
			term = ptyReq.Term
		}
		if ptyReq.Rows > 0 {
			rows = int(ptyReq.Rows)
		}
		if ptyReq.Columns > 0 {
			cols = int(ptyReq.Columns)
		}
		modes = ptyReq.Modes
	}

	// Use default modes if none provided
	if modes == nil {
		modes = ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
	}

	if err := session.RequestPty(term, rows, cols, modes); err != nil {
		fmt.Fprintf(channel, "Failed to request PTY: %v\r\n", err)
		return
	}

	// Get stdin pipe
	stdin, err := session.StdinPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stdin pipe: %v\r\n", err)
		return
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stdout pipe: %v\r\n", err)
		return
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stderr pipe: %v\r\n", err)
		return
	}

	// Start shell
	if err := session.Shell(); err != nil {
		fmt.Fprintf(channel, "Failed to start shell: %v\r\n", err)
		return
	}

	done := make(chan struct{})

	// Input channel -> VM stdin
	go func() {
		for {
			select {
			case data, ok := <-input:
				if !ok {
					return
				}
				stdin.Write(data)
			case <-done:
				return
			}
		}
	}()

	// VM stdout -> User
	go func() {
		io.Copy(channel, stdout)
	}()

	// VM stderr -> User
	go func() {
		io.Copy(channel, stderr)
	}()

	// Wait for VM session to complete
	session.Wait()

	// Signal done and cleanup
	close(done)
	stdin.Close()
}

// handleConnect returns true if a connection was established (session should end after)
func (s *Server) handleConnect(channel ssh.Channel, vmID string, ptyReq *ptyRequestMsg, requests <-chan *ssh.Request) bool {
	// Look up the VM
	vm, err := s.apiClient.GetVM(vmID)
	if err != nil {
		fmt.Fprintf(channel, "Error looking up VM: %v\r\n", err)
		return false
	}

	if vm == nil {
		fmt.Fprintf(channel, "VM not found: %s\r\n", vmID)
		return false
	}

	if vm.State != "running" {
		fmt.Fprintf(channel, "VM is not running (state: %s)\r\n", vm.State)
		return false
	}

	if vm.IPAddress == nil {
		fmt.Fprintf(channel, "VM has no IP address assigned\r\n")
		return false
	}

	fmt.Fprintf(channel, "Connecting to %s (%s) at %s...\r\n", vm.Name, vm.ID, *vm.IPAddress)

	// Connect to the VM's SSH server
	vmAddr := fmt.Sprintf("%s:22", *vm.IPAddress)
	return s.proxySSH(channel, vmAddr, ptyReq, requests)
}

func (s *Server) proxySSH(channel ssh.Channel, vmAddr string, ptyReq *ptyRequestMsg, requests <-chan *ssh.Request) bool {
	config := &ssh.ClientConfig{
		User: vmUsername,
		Auth: []ssh.AuthMethod{
			ssh.Password(vmPassword),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", vmAddr, config)
	if err != nil {
		fmt.Fprintf(channel, "Failed to connect to VM: %v\r\n", err)
		return false
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(channel, "Failed to create session: %v\r\n", err)
		return false
	}
	defer session.Close()

	// Determine terminal settings from client's PTY request
	term := "xterm-256color"
	rows := 24
	cols := 80
	var modes ssh.TerminalModes
	if ptyReq != nil {
		if ptyReq.Term != "" {
			term = ptyReq.Term
		}
		if ptyReq.Rows > 0 {
			rows = int(ptyReq.Rows)
		}
		if ptyReq.Columns > 0 {
			cols = int(ptyReq.Columns)
		}
		modes = ptyReq.Modes
	}

	// Use default modes if none provided
	if modes == nil {
		modes = ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
	}

	if err := session.RequestPty(term, rows, cols, modes); err != nil {
		fmt.Fprintf(channel, "Failed to request PTY: %v\r\n", err)
		return false
	}

	// Get stdin/stdout pipes
	stdin, err := session.StdinPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stdin pipe: %v\r\n", err)
		return false
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stdout pipe: %v\r\n", err)
		return false
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		fmt.Fprintf(channel, "Failed to get stderr pipe: %v\r\n", err)
		return false
	}

	// Start shell
	if err := session.Shell(); err != nil {
		fmt.Fprintf(channel, "Failed to start shell: %v\r\n", err)
		return false
	}

	// Channel to signal when session ends
	done := make(chan struct{})

	// Handle window-change requests from the client
	if requests != nil {
		go func() {
			for {
				select {
				case req, ok := <-requests:
					if !ok {
						return
					}
					switch req.Type {
					case "window-change":
						if len(req.Payload) >= 8 {
							w := binary.BigEndian.Uint32(req.Payload[0:4])
							h := binary.BigEndian.Uint32(req.Payload[4:8])
							session.WindowChange(int(h), int(w))
						}
					}
					if req.WantReply {
						req.Reply(false, nil)
					}
				case <-done:
					return
				}
			}
		}()
	}

	// Create a pipe for stdin - we'll write to it from a goroutine
	stdinReader, stdinWriter := io.Pipe()

	// Proxy data between the user and the VM
	var wg sync.WaitGroup
	wg.Add(3)

	// User -> pipe (runs until session ends)
	go func() {
		defer wg.Done()
		defer stdinWriter.Close()
		buf := make([]byte, 1024)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := channel.Read(buf)
			if err != nil {
				return
			}
			select {
			case <-done:
				return
			default:
			}
			if _, err := stdinWriter.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	// Pipe -> VM stdin
	go func() {
		defer wg.Done()
		io.Copy(stdin, stdinReader)
		stdin.Close()
	}()

	// VM stdout -> User
	go func() {
		defer wg.Done()
		io.Copy(channel, stdout)
	}()

	// VM stderr -> User
	go func() {
		io.Copy(channel, stderr)
	}()

	// Wait for VM session to complete
	session.Wait()

	// Signal goroutines to stop and close pipes
	close(done)
	stdinReader.Close()
	stdin.Close()

	// Wait for goroutines to finish (with timeout to avoid hanging)
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// All goroutines finished
	case <-time.After(500 * time.Millisecond):
		// Timeout - some goroutines still running, but that's ok
	}

	return true
}
