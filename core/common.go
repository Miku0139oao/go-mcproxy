package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mcproxy/config"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const VERSION_1_8_9 = 47
const VERSION_1_18_2 = 758

var onlineCount atomic.Int32

// Connection represents an active client connection
type Connection struct {
	ID          string    // Unique identifier for the connection
	Username    string    // Minecraft username
	ClientAddr  string    // Client's remote address
	ProxyAddr   string    // Proxy listen address
	RemoteAddr  string    // Remote server address
	ConnectedAt time.Time // When the connection was established
	ClientConn  net.Conn  // The client connection
	RemoteConn  net.Conn  // The connection to the remote server
	ProxyIndex  int       // Index of the proxy in the configuration
	PublicIP    string    // Public IP address of the connection
}

// ActiveConnections tracks all active connections
var activeConnections = struct {
	sync.RWMutex
	connections map[string]*Connection
}{
	connections: make(map[string]*Connection),
}

// ConnectionsPerIP tracks the number of connections per public IP
var connectionsPerIP = struct {
	sync.RWMutex
	counts map[string]int
}{
	counts: make(map[string]int),
}

// MaxConnectionsPerIP is the maximum number of connections allowed per public IP
const MaxConnectionsPerIP = 4

// RegisterConnection adds a connection to the tracking system
func RegisterConnection(conn *Connection) {
	activeConnections.Lock()
	defer activeConnections.Unlock()
	activeConnections.connections[conn.ID] = conn

	// Increment connection count for this IP
	if conn.PublicIP != "" && conn.PublicIP != "N/A" && conn.PublicIP != "Error" && conn.PublicIP != "Unknown" {
		connectionsPerIP.Lock()
		connectionsPerIP.counts[conn.PublicIP]++
		count := connectionsPerIP.counts[conn.PublicIP]
		connectionsPerIP.Unlock()
		log.Printf("[INFO] Connection count for IP %s: %d", conn.PublicIP, count)
	}
}

// UnregisterConnection removes a connection from the tracking system
func UnregisterConnection(id string) {
	activeConnections.Lock()
	conn := activeConnections.connections[id]
	delete(activeConnections.connections, id)
	activeConnections.Unlock()

	// Decrement connection count for this IP
	if conn != nil && conn.PublicIP != "" && conn.PublicIP != "N/A" && conn.PublicIP != "Error" && conn.PublicIP != "Unknown" {
		connectionsPerIP.Lock()
		if connectionsPerIP.counts[conn.PublicIP] > 0 {
			connectionsPerIP.counts[conn.PublicIP]--
			log.Printf("[INFO] Connection count for IP %s: %d", conn.PublicIP, connectionsPerIP.counts[conn.PublicIP])
		}
		connectionsPerIP.Unlock()
	}
}

// GetConnectionCountForIP returns the number of outbound connections from a network interface with the given IP
func GetConnectionCountForIP(ip string) int {
	if ip == "" || ip == "N/A" || ip == "Error" || ip == "Unknown" {
		return 0
	}

	// Get all active connections
	connections := GetAllConnections()

	// Count connections that are using this network interface (outbound connections)
	count := 0
	for _, conn := range connections {
		// Check if this connection is using the specified network interface
		// by comparing the local address of the remote connection
		if conn.RemoteConn != nil {
			localAddr := conn.RemoteConn.LocalAddr().String()
			// Extract just the IP address from the localAddr (remove port if present)
			if idx := strings.LastIndex(localAddr, ":"); idx != -1 {
				localAddr = localAddr[:idx]
			}

			// If the local address of the outbound connection matches the specified IP,
			// increment the count
			if localAddr == ip {
				count++
			}
		}
	}

	return count
}

// GetConnection retrieves a connection by ID
func GetConnection(id string) *Connection {
	activeConnections.RLock()
	defer activeConnections.RUnlock()
	return activeConnections.connections[id]
}

// GetAllConnections returns a copy of all active connections
func GetAllConnections() []*Connection {
	activeConnections.RLock()
	defer activeConnections.RUnlock()

	connections := make([]*Connection, 0, len(activeConnections.connections))
	for _, conn := range activeConnections.connections {
		connections = append(connections, conn)
	}
	return connections
}

// DisconnectClient forcibly disconnects a client by ID
func DisconnectClient(id string, reason string) error {
	conn := GetConnection(id)
	if conn == nil {
		return fmt.Errorf("connection not found")
	}

	log.Printf("[INFO] Disconnecting client %s (%s) with reason: %s", conn.Username, conn.ClientAddr, reason)

	// Send disconnect message if possible
	if conn.ClientConn != nil {
		err := sendDisconnect(conn.ClientConn, reason)
		if err != nil {
			// Just log the error, we'll still try to close the connection
			log.Printf("[WARN] Failed to send disconnect message to %s: %v", conn.Username, err)
		}

		// Ensure we flush any buffered data
		if flusher, ok := conn.ClientConn.(interface{ SetWriteDeadline(time.Time) error }); ok {
			flusher.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		}
	}

	// Close connections with a small delay to ensure disconnect message is sent
	time.Sleep(200 * time.Millisecond)

	if conn.ClientConn != nil {
		conn.ClientConn.Close()
		log.Printf("[DEBUG] Closed client connection for %s", conn.Username)
	}

	if conn.RemoteConn != nil {
		conn.RemoteConn.Close()
		log.Printf("[DEBUG] Closed remote connection for %s", conn.Username)
	}

	// Decrement connection counters
	onlineCount.Add(-1)
	cp := GetControlPanel()
	cp.DecrementConnectionCount(conn.ProxyAddr)

	// Unregister the connection
	UnregisterConnection(id)
	log.Printf("[INFO] Successfully disconnected client %s", conn.Username)

	return nil
}

// write disconnect packet
func sendDisconnect(w io.Writer, reason string) error {
	type chat struct {
		Text string `json:"text"`
	}

	bytes, err := json.Marshal(&chat{
		Text: reason,
	})
	if err != nil {
		return err
	}

	pkt, err := Pack(String(string(bytes)))
	if err != nil {
		return fmt.Errorf("pack disconnect: %w", err)
	}

	return WritePacket(0x1A, pkt, w)
}

type statusVersion struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

type statusPlayerSample struct {
	Name string `json:"name"`
	Id   string `json:"id"`
}

type statusPlayers struct {
	Max    int                 `json:"max"`
	Online int                 `json:"online"`
	Sample []statusPlayerSample `json:"sample"`
}

type statusResponse struct {
	Version     statusVersion `json:"version"`
	Players     statusPlayers `json:"players"`
	Description string        `json:"description"`
	Favicon     string        `json:"favicon"`
}

// GetPublicIP gets the public IP address using curl to ipinfo.io through a specific network interface
func GetPublicIP(localAddr string) string {
	if localAddr == "" {
		return "N/A"
	}

	// Extract just the IP address from the localAddr (remove port if present)
	ipOnly := localAddr
	if idx := strings.LastIndex(localAddr, ":"); idx != -1 {
		ipOnly = localAddr[:idx]
	}

	// Create the curl command with the interface binding
	cmd := exec.Command("curl", "-s", "--interface", ipOnly, "ipinfo.io/ip")

	// Execute the command
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[ERROR] Failed to get public IP for interface %s: %v", localAddr, err)
		return "Error"
	}

	// Clean the output
	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return "Unknown"
	}

	return ip
}

// write ping response packet
func sendResponse(w io.Writer, protocol int, cfg config.ProxyConfig) error {
	// Get all active connections to display online users
	connections := GetAllConnections()

	// Create player samples from active connections
	samples := make([]statusPlayerSample, 0, len(connections))
	for _, conn := range connections {
		if conn.Username != "" {
			// Use username as both name and ID for simplicity
			// In a real Minecraft server, ID would be a UUID
			samples = append(samples, statusPlayerSample{
				Name: conn.Username,
				Id:   "player-" + conn.ID, // Use connection ID as a pseudo-UUID
			})
		}
	}

	resp, err := json.Marshal(statusResponse{
		Version: statusVersion{
			Name:     "gomcproxy",
			Protocol: protocol,
		},
		Players: statusPlayers{
			Max:    cfg.MaxPlayer,
			Online: int(onlineCount.Load()),
			Sample: samples,
		},
		Description: cfg.Description,
		Favicon:     cfg.Favicon,
	})

	if err != nil {
		return fmt.Errorf("response marshal: %w", err)
	}

	pkt, err := Pack(String(resp))
	if err != nil {
		return fmt.Errorf("response pack: %w", err)
	}

	return WritePacket(0x00, pkt, w)
}
