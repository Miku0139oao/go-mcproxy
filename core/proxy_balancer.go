package core

import (
	"bufio"
	"fmt"
	"log"
	"mcproxy/config"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// proxyStatistics tracks performance and health metrics for a proxy
type proxyStatistics struct {
	// Number of successful connections
	successfulConnections atomic.Int64
	// Number of failed connections
	failedConnections atomic.Int64
	// Last time this proxy was selected
	lastSelected time.Time
	// Is this proxy considered healthy
	healthy atomic.Bool
}

// ProxyBalancer manages load balancing across multiple proxies
type ProxyBalancer struct {
	listenAddr string
	proxies    []config.ProxyConfig
	listener   net.Listener
	stopChan   chan struct{}
	mutex      sync.RWMutex
	// Track the last selected proxy index for round-robin
	lastIndex int
	// Track proxy health and statistics
	proxyStats map[int]*proxyStatistics
}

// NewProxyBalancer creates a new proxy balancer
func NewProxyBalancer(listenAddr string, proxies []config.ProxyConfig) *ProxyBalancer {
	// Create proxy statistics map
	proxyStats := make(map[int]*proxyStatistics)
	for i := range proxies {
		stats := &proxyStatistics{}
		stats.healthy.Store(true) // Start with all proxies considered healthy
		proxyStats[i] = stats
	}

	return &ProxyBalancer{
		listenAddr: listenAddr,
		proxies:    proxies,
		stopChan:   make(chan struct{}),
		lastIndex:  -1, // Start with -1 so first selection will be index 0
		proxyStats: proxyStats,
	}
}

// Start starts the proxy balancer
func (pb *ProxyBalancer) Start() error {
	listener, err := net.Listen("tcp", pb.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", pb.listenAddr, err)
	}

	pb.mutex.Lock()
	pb.listener = listener
	pb.mutex.Unlock()

	log.Printf("[INFO] Proxy balancer listening on %s", pb.listenAddr)

	// Run the accept loop in a separate goroutine
	go pb.acceptLoop()

	return nil
}

// Stop stops the proxy balancer
func (pb *ProxyBalancer) Stop() {
	pb.mutex.Lock()
	defer pb.mutex.Unlock()

	close(pb.stopChan)
	if pb.listener != nil {
		pb.listener.Close()
	}
}

// acceptLoop accepts incoming connections and forwards them to the least loaded proxy
func (pb *ProxyBalancer) acceptLoop() {
	for {
		// Check if we should stop
		select {
		case <-pb.stopChan:
			log.Printf("[INFO] Stopping proxy balancer on %s", pb.listenAddr)
			return
		default:
			// Set a very short accept timeout so we can check the stop channel regularly
			pb.listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))

			conn, err := pb.listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// This is just a timeout, continue and check the stop channel
					continue
				}

				log.Printf("[ERROR] Failed to accept connection: %v", err)
				return
			}

			// Handle the connection in a separate goroutine
			go pb.handleConnection(conn)
		}
	}
}

// handleConnection handles a Minecraft connection using the selected proxy's network interface
func (pb *ProxyBalancer) handleConnection(clientConn net.Conn) {
	clientAddr := clientConn.RemoteAddr().String()
	defer clientConn.Close()
	defer log.Printf("[INFO] Balancer: Connection ended: %s", clientAddr)
	log.Printf("[INFO] Balancer: New connection from: %s", clientAddr)

	// Create a buffered reader for the client connection
	reader := bufio.NewReader(clientConn)
	defer reader.Reset(nil)

	// Read the handshake packet
	pkt, err := ReadPacket(reader)
	if err != nil {
		log.Printf("[ERROR] Balancer: Failed to read packet from %s: %v", clientAddr, err)
		return
	}

	// Parse the handshake packet
	var protocol VarInt
	var address String
	var port UShort
	var nextState VarInt
	_, err = pkt.Scan(&protocol, &address, &port, &nextState)
	if err != nil {
		log.Printf("[ERROR] Balancer: Failed to parse handshake from %s: %v", clientAddr, err)
		return
	}

	log.Printf("[INFO] Balancer: Client %s connecting to %s:%d, protocol=%d, state=%d",
		clientAddr, address, port, protocol, nextState)

	// Find the best proxy to use
	proxyConfig, proxyIndex := pb.selectBestProxy()
	if proxyConfig == nil {
		log.Printf("[ERROR] Balancer: No suitable proxy found for connection from %s", clientAddr)
		return
	}

	log.Printf("[INFO] Balancer: Selected proxy %d interface %s (remote: %s) for client %s", 
		proxyIndex+1, proxyConfig.LocalAddr, proxyConfig.Remote, clientAddr)

	// Get the public IP for the selected proxy
	publicIP := GetPublicIP(proxyConfig.LocalAddr)

	// Handle different types of requests based on the next state
	switch nextState {
	case 1: // status (ping)
		log.Printf("[DEBUG] Balancer: Handling ping request from %s", clientAddr)
		err := handlePing(reader, clientConn, int(protocol), *proxyConfig)
		if err != nil {
			log.Printf("[ERROR] Balancer: Failed to handle ping from %s: %v", clientAddr, err)
		}

	case 2: // login
		if protocol < VERSION_1_8_9 {
			log.Printf("[WARN] Balancer: Client %s using unsupported protocol version: %d", clientAddr, protocol)
			err := sendDisconnect(clientConn, "Unsupported client version")
			if err != nil {
				log.Printf("[ERROR] Balancer: Failed to disconnect %s: %v", clientAddr, err)
			}
			return
		}

		// Check if the server is full
		if onlineCount.Load() >= int32(proxyConfig.MaxPlayer) {
			log.Printf("[WARN] Balancer: Server full, rejecting client %s", clientAddr)
			err := sendDisconnect(clientConn, "The server is full")
			if err != nil {
				log.Printf("[ERROR] Balancer: Failed to disconnect %s: %v", clientAddr, err)
			}
			return
		}

		// Check if we've reached the connection limit for this IP
		currentCount := GetConnectionCountForIP(publicIP)
		if currentCount >= MaxConnectionsPerIP {
			log.Printf("[WARN] Balancer: Connection limit reached for IP %s (%d connections), rejecting client %s",
				publicIP, currentCount, clientAddr)
			err := sendDisconnect(clientConn, "Connection limit reached for your IP")
			if err != nil {
				log.Printf("[ERROR] Balancer: Failed to disconnect %s: %v", clientAddr, err)
			}
			return
		}

		// Create a connection ID
		connID := fmt.Sprintf("%s-%d", clientAddr, time.Now().UnixNano())

		// Check if the client is using FML (Forge Mod Loader)
		isFML := strings.HasSuffix(string(address), "\x00FML\x00")
		if isFML {
			log.Printf("[INFO] Balancer: FML client detected: %s", clientAddr)
		}

		// Create and register the connection
		connection := &Connection{
			ID:          connID,
			ClientAddr:  clientAddr,
			ProxyAddr:   proxyConfig.Listen,
			RemoteAddr:  proxyConfig.Remote,
			ConnectedAt: time.Now(),
			ClientConn:  clientConn,
			ProxyIndex:  -1, // -1 indicates it's a balancer connection
			PublicIP:    publicIP,
		}
		RegisterConnection(connection)
		defer UnregisterConnection(connID)

		// Increment global connection count
		onlineCount.Add(1)
 	defer decrementOnlineCount()

		// Only increment connection count for the load balancer itself
		// The individual proxy's connection count will be incremented in handleForward
		cp := GetControlPanel()
		cp.IncrementConnectionCount(pb.listenAddr)
		defer cp.DecrementConnectionCount(pb.listenAddr)

	// Get the proxy statistics
	proxyStats := pb.proxyStats[proxyIndex]

	// Handle the forwarding
	err := handleForward(reader, clientConn, isFML, int(protocol), *proxyConfig)
	if err != nil {
		log.Printf("[ERROR] Balancer: Failed to handle forward for %s: %v", clientAddr, err)
		// Record failed connection
		proxyStats.failedConnections.Add(1)

		// If we have too many failures, mark the proxy as unhealthy
		if proxyStats.failedConnections.Load() > proxyStats.successfulConnections.Load()*2 && 
		   proxyStats.failedConnections.Load() > 5 {
			proxyStats.healthy.Store(false)
			log.Printf("[WARN] Proxy %d marked as unhealthy due to too many failures", proxyIndex+1)
		}
	} else {
		// Record successful connection
		proxyStats.successfulConnections.Add(1)

		// If we have enough successful connections, mark the proxy as healthy again
		if !proxyStats.healthy.Load() && 
		   proxyStats.successfulConnections.Load() > proxyStats.failedConnections.Load() {
			proxyStats.healthy.Store(true)
			log.Printf("[INFO] Proxy %d marked as healthy again", proxyIndex+1)
		}
	}
	}
}

// selectBestProxy selects the best proxy based on a more realistic load balancing strategy
// Returns the selected proxy config and its index
func (pb *ProxyBalancer) selectBestProxy() (*config.ProxyConfig, int) {
	pb.mutex.RLock()
	defer pb.mutex.RUnlock()

	if len(pb.proxies) == 0 {
		return nil, -1
	}

	// Define a struct to hold proxy scoring information
	type proxyScore struct {
		index        int     // Index of the proxy in the proxies array
		connectionCount int  // Current number of connections
		maxConnections int   // Maximum allowed connections
		loadPercent   float64 // Current load percentage
		weight        float64 // Weight for selection (higher is better)
		healthy       bool    // Is this proxy healthy
	}

	// Calculate scores for each proxy
	scores := make([]proxyScore, 0, len(pb.proxies))
	totalMaxConnections := 0

	// First pass: gather data and calculate total capacity
	for i, proxy := range pb.proxies {
		// Get the proxy's public IP
		ip := GetPublicIP(proxy.LocalAddr)

		// Get current connection count
		connectionCount := GetConnectionCountForIP(ip)

		// Get max connections (use MaxPlayer as capacity indicator)
		maxConnections := proxy.MaxPlayer
		if maxConnections <= 0 {
			maxConnections = MaxConnectionsPerIP // Use default if not specified
		}

		// Add to total capacity for weighted distribution
		totalMaxConnections += maxConnections

		// Calculate load percentage
		loadPercent := 0.0
		if maxConnections > 0 {
			loadPercent = float64(connectionCount) / float64(maxConnections) * 100.0
		}

		// Check if this proxy is healthy
		stats := pb.proxyStats[i]
		healthy := stats.healthy.Load()

		// Add to scores array
		scores = append(scores, proxyScore{
			index:          i,
			connectionCount: connectionCount,
			maxConnections: maxConnections,
			loadPercent:    loadPercent,
			healthy:        healthy,
		})
	}

	// Second pass: calculate weights based on capacity and current load
	for i := range scores {
		// Start with a base weight proportional to capacity
		capacityWeight := float64(scores[i].maxConnections) / float64(totalMaxConnections) * 100.0

		// Adjust weight based on current load (less load = higher weight)
		loadAdjustment := 100.0 - scores[i].loadPercent

		// Calculate final weight (capacity weight + load adjustment)
		// This gives preference to proxies with higher capacity and lower current load
		scores[i].weight = capacityWeight * 0.4 + loadAdjustment * 0.6

		// If proxy is not healthy, significantly reduce its weight
		if !scores[i].healthy {
			scores[i].weight *= 0.1
		}

		// If proxy is at or above connection limit, make it less likely to be chosen
		if scores[i].connectionCount >= scores[i].maxConnections {
			scores[i].weight *= 0.2
		}

		log.Printf("[DEBUG] Proxy %d: connections=%d, max=%d, load=%.1f%%, weight=%.1f", 
			scores[i].index+1, scores[i].connectionCount, scores[i].maxConnections, 
			scores[i].loadPercent, scores[i].weight)
	}

	// Sort by weight (descending)
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].weight > scores[j].weight
	})

	// Add some randomization to prevent all connections going to the same proxy
	// when multiple proxies have similar weights

	// Get the top candidates (proxies with weights within 10% of the best one)
	topCandidates := make([]proxyScore, 0)
	if len(scores) > 0 {
		bestWeight := scores[0].weight
		for _, score := range scores {
			// If weight is within 10% of the best weight, consider it a top candidate
			if score.weight >= bestWeight * 0.9 {
				topCandidates = append(topCandidates, score)
			}
		}
	}

	// If we have multiple top candidates, randomly select one
	var selectedIndex int
	if len(topCandidates) > 1 {
		// Use a random index from the top candidates
		randomIndex := int(time.Now().UnixNano() % int64(len(topCandidates)))
		selectedIndex = topCandidates[randomIndex].index
		log.Printf("[DEBUG] Randomly selected proxy %d from %d top candidates", 
			selectedIndex+1, len(topCandidates))
	} else if len(scores) > 0 {
		// Just use the highest weighted proxy
		selectedIndex = scores[0].index
		log.Printf("[DEBUG] Selected highest weighted proxy %d", selectedIndex+1)
	} else {
		// Fallback to round-robin if no scores
		pb.lastIndex = (pb.lastIndex + 1) % len(pb.proxies)
		selectedIndex = pb.lastIndex
		log.Printf("[DEBUG] Fallback to round-robin, selected proxy %d", selectedIndex+1)
	}

	// Update statistics for the selected proxy
	if stats, ok := pb.proxyStats[selectedIndex]; ok {
		stats.lastSelected = time.Now()
	}

	// Return the selected proxy and its index
	return &pb.proxies[selectedIndex], selectedIndex
}

// StartBalancer starts a proxy balancer with the given configuration
func StartBalancer(listenAddr string, cfg *config.Config) {
	balancer := NewProxyBalancer(listenAddr, cfg.Proxies)
	err := balancer.Start()
	if err != nil {
		log.Fatalf("[ERROR] Failed to start proxy balancer: %v", err)
	}
}
