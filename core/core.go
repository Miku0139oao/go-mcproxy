package core

import (
	"bufio"
	"fmt"
	"log"
	"mcproxy/config"
	"net"
	"strings"
	"sync"
	"time"
)

// proxyInstance represents a running proxy server
type proxyInstance struct {
	config   config.ProxyConfig
	listener net.Listener
	index    int
	stopChan chan struct{}
}

// activeProxies maps listen addresses to their proxy instances
var activeProxies = make(map[string]*proxyInstance)
var proxyMutex sync.RWMutex

// Start starts all proxy servers defined in the configuration
func Start(c config.Config) {
	// Start a separate proxy server for each configuration
	var wg sync.WaitGroup

	for i, proxyConfig := range c.Proxies {
		wg.Add(1)
		go func(idx int, cfg config.ProxyConfig) {
			defer wg.Done()
			startProxy(idx, cfg)
		}(i, proxyConfig)
	}

	// Wait for all proxy servers to finish (which should never happen unless they crash)
	wg.Wait()
	log.Printf("[WARN] All proxy servers have stopped")
}

// StopAll stops all running proxy servers
func StopAll() {
	proxyMutex.Lock()
	defer proxyMutex.Unlock()

	log.Printf("[INFO] Stopping all proxy servers...")

	// Signal all proxies to stop
	for addr, proxy := range activeProxies {
		log.Printf("[INFO] Stopping proxy on %s", addr)
		close(proxy.stopChan)
	}

	// Wait a moment for proxies to clean up
	time.Sleep(500 * time.Millisecond)

	// Clear the active proxies map
	for k := range activeProxies {
		delete(activeProxies, k)
	}

	log.Printf("[INFO] All proxy servers stopped")
}

// Restart stops all running proxy servers and starts new ones with the given configuration
func Restart(c config.Config) {
	// Stop all running proxies
	StopAll()

	// Start new proxies with the updated configuration
	log.Printf("[INFO] Restarting proxy servers with new configuration...")

	// Start in a new goroutine to avoid blocking
	go Start(c)
}

func startProxy(idx int, cfg config.ProxyConfig) {
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("[ERROR] Proxy %d: Failed to listen on %s: %v", idx+1, cfg.Listen, err)
		return
	}

	// Register this proxy instance
	proxy := &proxyInstance{
		config:   cfg,
		listener: listener,
		index:    idx,
		stopChan: make(chan struct{}),
	}

	proxyMutex.Lock()
	activeProxies[cfg.Listen] = proxy
	proxyMutex.Unlock()

	log.Printf("[INFO] Proxy %d: Server listening on %s", idx+1, cfg.Listen)

	// Run the accept loop in a separate goroutine
	go func() {
		for {
			// Check if we should stop
			select {
			case <-proxy.stopChan:
				log.Printf("[INFO] Proxy %d: Stopping server on %s", idx+1, cfg.Listen)
				listener.Close()

				// Unregister this proxy instance
				proxyMutex.Lock()
				delete(activeProxies, cfg.Listen)
				proxyMutex.Unlock()

				return
			default:
				// Set a very short accept timeout so we can check the stop channel regularly
				// Reduced from 1 second to 100ms for better responsiveness
				listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))

				conn, err := listener.Accept()
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						// This is just a timeout, continue and check the stop channel
						continue
					}

					log.Printf("[ERROR] Proxy %d: Failed to accept connection: %v", idx+1, err)

					// Unregister this proxy instance
					proxyMutex.Lock()
					delete(activeProxies, cfg.Listen)
					proxyMutex.Unlock()

					return
				}

				go handler(conn, cfg, idx)
			}
		}
	}()
}

func handler(conn net.Conn, cfg config.ProxyConfig, idx int) {
	clientAddr := conn.RemoteAddr().String()
	defer conn.Close()
	defer log.Printf("[INFO] Proxy %d: Connection ended: %s", idx+1, clientAddr)
	log.Printf("[INFO] Proxy %d: New connection from: %s", idx+1, clientAddr)

	reader := bufio.NewReader(conn)
	defer reader.Reset(nil)

	pkt, err := ReadPacket(reader)
	if err != nil {
		log.Printf("[ERROR] Proxy %d: Failed to read packet from %s: %v", idx+1, clientAddr, err)
		return
	}

	// packet handshake
	var protocol VarInt
	var address String
	var port UShort
	var nextState VarInt
	_, err = pkt.Scan(&protocol, &address, &port, &nextState)
	if err != nil {
		log.Printf("[ERROR] Proxy %d: Failed to parse handshake from %s: %v", idx+1, clientAddr, err)
		return
	}

	log.Printf("[INFO] Proxy %d: Client %s connecting to %s:%d, protocol=%d, state=%d", 
		idx+1, clientAddr, address, port, protocol, nextState)

	switch nextState {
	case 1: // status
		log.Printf("[DEBUG] Proxy %d: Handling ping request from %s", idx+1, clientAddr)
		err := handlePing(reader, conn, int(protocol), cfg)
		if err != nil {
			log.Printf("[ERROR] Proxy %d: Failed to handle ping from %s: %v", idx+1, clientAddr, err)
		}

	case 2: // login
		if protocol < VERSION_1_8_9 {
			log.Printf("[WARN] Proxy %d: Client %s using unsupported protocol version: %d", idx+1, clientAddr, protocol)
			err := sendDisconnect(conn, "unsupported client version")
			if err != nil {
				log.Printf("[ERROR] Proxy %d: Failed to disconnect %s: %v", idx+1, clientAddr, err)
			}
			return
		}

		// disconnect if server is full
		if onlineCount.Load() >= int32(cfg.MaxPlayer) {
			log.Printf("[WARN] Proxy %d: Server full, rejecting client %s", idx+1, clientAddr)
			err := sendDisconnect(conn, "The server is full")
			if err != nil {
				log.Printf("[ERROR] Proxy %d: Failed to disconnect %s: %v", idx+1, clientAddr, err)
			}
			return
		}

		// Create a connection ID and get the public IP
		connID := fmt.Sprintf("%s-%d", clientAddr, time.Now().UnixNano())
		publicIP := GetPublicIP(cfg.LocalAddr)

		// Check if we've reached the connection limit for this IP
		currentCount := GetConnectionCountForIP(publicIP)
		if currentCount >= MaxConnectionsPerIP {
			log.Printf("[WARN] Proxy %d: Connection limit reached for IP %s (%d connections), rejecting client %s", 
				idx+1, publicIP, currentCount, clientAddr)
			err := sendDisconnect(conn, "Connection limit reached for your IP")
			if err != nil {
				log.Printf("[ERROR] Proxy %d: Failed to disconnect %s: %v", idx+1, clientAddr, err)
			}
			return
		}

		// Create and register the connection
		connection := &Connection{
			ID:          connID,
			ClientAddr:  clientAddr,
			ProxyAddr:   cfg.Listen,
			RemoteAddr:  cfg.Remote,
			ConnectedAt: time.Now(),
			ClientConn:  conn,
			ProxyIndex:  idx,
			PublicIP:    publicIP,
		}
		RegisterConnection(connection)
		defer UnregisterConnection(connID)

		isFML := strings.HasSuffix(string(address), "\x00FML\x00")
		if isFML {
			log.Printf("[INFO] Proxy %d: FML client detected: %s", idx+1, clientAddr)
		}

		err := handleForward(reader, conn, isFML, int(protocol), cfg)
		if err != nil {
			log.Printf("[ERROR] Proxy %d: Failed to handle forward for %s: %v", idx+1, clientAddr, err)
		}
	}
}
