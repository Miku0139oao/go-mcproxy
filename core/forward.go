package core

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"mcproxy/config"
	"net"
	"sync"
)

func handleForward(reader io.Reader, writer io.Writer, fml bool, protocol int, cfg config.ProxyConfig) error {
	// Increment global connection count
	onlineCount.Add(1)
	defer onlineCount.Add(-1)

	// Increment per-proxy connection count
	cp := GetControlPanel()
	cp.IncrementConnectionCount(cfg.Listen)
	defer cp.DecrementConnectionCount(cfg.Listen)

	// Get the client connection from the writer
	clientConn, ok := writer.(net.Conn)
	if !ok {
		return fmt.Errorf("writer is not a net.Conn")
	}

	// Get the connection ID from the client address
	clientAddr := clientConn.RemoteAddr().String()

	// Find the connection in the active connections map
	var connection *Connection
	var isBungeeServerSwitch bool = false

	activeConnections.RLock()
	for _, conn := range activeConnections.connections {
		if conn.ClientAddr == clientAddr {
			connection = conn
			// If this connection already exists and has a username, it might be a BungeeCord server switch
			if conn.Username != "" {
				isBungeeServerSwitch = true
				log.Printf("[DEBUG] Detected potential BungeeCord server switch for user: %s", conn.Username)
			}
			break
		}
	}
	activeConnections.RUnlock()

	if connection == nil {
		log.Printf("[WARN] Could not find connection for client %s", clientAddr)
	}

	// read login start
	pkt, err := ReadPacket(reader)
	if err != nil {
		return fmt.Errorf("read pkt login start: %w", err)
	}
	if pkt.ID != 0x00 {
		return fmt.Errorf("expect packet login start, got %d", pkt.ID)
	}

	var username String
	_, err = pkt.Scan(&username)
	if err != nil {
		return fmt.Errorf("scan login start: %w", err)
	}

	log.Printf("[INFO] User login attempt: %s", username)

	// Update the connection with the username if we found it
	if connection != nil {
		// If the username matches the existing connection, it's likely a BungeeCord server switch
		if connection.Username == string(username) {
			isBungeeServerSwitch = true
			log.Printf("[DEBUG] Confirmed BungeeCord server switch for user: %s", username)
		}
		connection.Username = string(username)
	}

	// whitelist / blacklist
	allow, msg, err := allowJoin(string(username), cfg)
	if err != nil {
		log.Printf("[ERROR] Authentication failed for %s: %v", username, err)
		return nil
	}

	if !allow {
		log.Printf("[WARN] User rejected: %s, reason: %s", username, msg)

		err = sendDisconnect(writer, msg)
		if err != nil {
			return fmt.Errorf("write disconnect: %w", err)
		}
		return nil
	}

	log.Printf("[INFO] User authenticated: %s", username)

	// connect to remote
	log.Printf("[DEBUG] Connecting to remote server: %s", cfg.Remote)
	if cfg.LocalAddr != "" {
		log.Printf("[DEBUG] Using local address for outgoing connection: %s", cfg.LocalAddr)
	}
	remote, err := DialMC(cfg.Remote, cfg.LocalAddr)
	if err != nil {
		log.Printf("[ERROR] Failed to connect to remote server %s: %v", cfg.Remote, err)
		return err
	}
	defer remote.Close()

	// Store the remote connection in the connection object
	if connection != nil {
		connection.RemoteConn = remote
	}

	// If this is a BungeeCord server switch, we need to handle it differently
	// to avoid sending duplicate login packets
	if isBungeeServerSwitch {
		log.Printf("[INFO] Handling BungeeCord server switch for user: %s", username)

		// For BungeeCord server switches, we don't need to send the handshake and login start packets
		// as they are already handled by BungeeCord. Sending them again causes issues.
		log.Printf("[DEBUG] Skipping handshake and login start packets for BungeeCord server switch")

		// Instead, we'll just forward the packets between the client and server
		// The BungeeCord server will handle the server switch properly
	} else {
		// Normal connection (not a BungeeCord server switch)
		// handshake packet
		rewriteHost := cfg.RewirteHost
		if fml {
			rewriteHost += "\x00FML\x00"
		}

		pktHandshake, err := Pack(
			VarInt(protocol),
			String(rewriteHost),
			UShort(cfg.RewirtePort),
			VarInt(2), // next state login
		)
		if err != nil {
			return err
		}
		err = WritePacket(0x00, pktHandshake, remote)
		if err != nil {
			return fmt.Errorf("write handshake: %w", err)
		}

		// write login start
		// the payload of login start varies between verions
		// so we just copy the payload
		pktLoginStart := pkt.Payload
		err = WritePacket(0x00, pktLoginStart, remote)
		if err != nil {
			return fmt.Errorf("write login start: %w", err)
		}
	}

	// start forward
	log.Printf("[INFO] Starting data forwarding for user: %s", username)
	var wg sync.WaitGroup
	wg.Add(2)

	// Use larger buffer size for better performance
	const bufferSize = 64 * 1024 // 64KB buffer

	// Forward data from remote server to client with buffering
	go func() {
		// Use a buffer for copying
		buffer := make([]byte, bufferSize)

		// Manual copy loop with buffering for better performance
		var bytesWritten int64
		var remoteConn net.Conn = remote
		var bufferedRemote *bufio.Reader = bufio.NewReaderSize(remote, bufferSize)

		for {
			// Read from the remote server
			nr, er := bufferedRemote.Read(buffer)

			// If read failed with an error other than EOF, try to reconnect
			if er != nil && er != io.EOF {
				log.Printf("[WARN] Read error from server for %s, attempting to reconnect: %v", username, er)

				// Close the old connection
				remoteConn.Close()

				// Reconnect using DialMC to re-resolve DNS
				newConn, dialErr := DialMC(cfg.Remote, cfg.LocalAddr)
				if dialErr != nil {
					log.Printf("[ERROR] Failed to reconnect to remote server %s: %v", cfg.Remote, dialErr)
					break
				}

				log.Printf("[INFO] Successfully reconnected to remote server %s for user %s", cfg.Remote, username)
				remoteConn = newConn
				bufferedRemote = bufio.NewReaderSize(newConn, bufferSize)

				// Update the connection in the connection object
				if connection != nil {
					connection.RemoteConn = newConn
				}

				// Need to resend handshake and login packets after reconnection
				if !isBungeeServerSwitch {
					// Resend handshake packet
					rewriteHost := cfg.RewirteHost
					if fml {
						rewriteHost += "\x00FML\x00"
					}

					pktHandshake, err := Pack(
						VarInt(protocol),
						String(rewriteHost),
						UShort(cfg.RewirtePort),
						VarInt(2), // next state login
					)
					if err != nil {
						log.Printf("[ERROR] Failed to create handshake packet for reconnection: %v", err)
						break
					}

					err = WritePacket(0x00, pktHandshake, newConn)
					if err != nil {
						log.Printf("[ERROR] Failed to send handshake packet for reconnection: %v", err)
						break
					}

					// Resend login start packet
					pktLoginStart, err := Pack(String(username))
					if err != nil {
						log.Printf("[ERROR] Failed to create login start packet for reconnection: %v", err)
						break
					}

					err = WritePacket(0x00, pktLoginStart, newConn)
					if err != nil {
						log.Printf("[ERROR] Failed to send login start packet for reconnection: %v", err)
						break
					}
				}

				// Try reading again with the new connection
				continue
			}

			if nr > 0 {
				nw, ew := writer.Write(buffer[0:nr])
				if nw < 0 || nr < nw {
					nw = 0
					if ew == nil {
						ew = fmt.Errorf("invalid write result")
					}
				}
				bytesWritten += int64(nw)
				if ew != nil {
					log.Printf("[ERROR] Write error forwarding data from server to client for %s: %v", username, ew)
					break
				}
				if nr != nw {
					log.Printf("[ERROR] Short write forwarding data from server to client for %s", username)
					break
				}
			}

			if er != nil {
				// We already handled non-EOF errors above
				break
			}
		}

		// Make sure to close the current remote connection if it's different from the original
		if remoteConn != remote {
			remoteConn.Close()
		}

		log.Printf("[DEBUG] Forwarded %d bytes from server to client for %s", bytesWritten, username)
		wg.Done()
	}()

	// Forward data from client to remote server with buffering
	go func() {
		// Create a buffered reader if needed
		var bufferedReader *bufio.Reader
		if br, ok := reader.(*bufio.Reader); ok {
			bufferedReader = br
		} else {
			bufferedReader = bufio.NewReaderSize(reader, bufferSize)
		}

		// Use a buffer for copying
		buffer := make([]byte, bufferSize)

		// Manual copy loop with buffering for better performance
		var bytesWritten int64
		var remoteConn net.Conn = remote

		for {
			nr, er := bufferedReader.Read(buffer)
			if nr > 0 {
				// Try to write to the remote server
				var writeErr error
				var nw int

				// Attempt to write to the current connection
				nw, writeErr = remoteConn.Write(buffer[0:nr])

				// If write failed, try to reconnect using DialMC to re-resolve DNS
				if writeErr != nil {
					log.Printf("[WARN] Write error to server for %s, attempting to reconnect: %v", username, writeErr)

					// Close the old connection
					remoteConn.Close()

					// Reconnect using DialMC to re-resolve DNS
					newConn, dialErr := DialMC(cfg.Remote, cfg.LocalAddr)
					if dialErr != nil {
						log.Printf("[ERROR] Failed to reconnect to remote server %s: %v", cfg.Remote, dialErr)
						break
					}

					log.Printf("[INFO] Successfully reconnected to remote server %s for user %s", cfg.Remote, username)
					remoteConn = newConn

					// Update the connection in the connection object
					if connection != nil {
						connection.RemoteConn = newConn
					}

					// Try writing again with the new connection
					nw, writeErr = remoteConn.Write(buffer[0:nr])
				}

				if nw < 0 || nr < nw {
					nw = 0
					if writeErr == nil {
						writeErr = fmt.Errorf("invalid write result")
					}
				}

				bytesWritten += int64(nw)
				if writeErr != nil {
					log.Printf("[ERROR] Write error forwarding data from client to server for %s: %v", username, writeErr)
					break
				}
				if nr != nw {
					log.Printf("[ERROR] Short write forwarding data from client to server for %s", username)
					break
				}
			}

			if er != nil {
				if er != io.EOF {
					log.Printf("[ERROR] Read error forwarding data from client to server for %s: %v", username, er)
				}
				break
			}
		}

		// Make sure to close the current remote connection
		if remoteConn != remote {
			remoteConn.Close()
		}

		log.Printf("[DEBUG] Forwarded %d bytes from client to server for %s", bytesWritten, username)
		wg.Done()
	}()

	wg.Wait()
	log.Printf("[INFO] Data forwarding completed for user: %s", username)
	return nil
}
