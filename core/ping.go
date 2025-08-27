package core

import (
	"fmt"
	"io"
	"log"
	"mcproxy/config"
	"time"
)

func handlePing(reader io.Reader, writer io.Writer, protocol int, cfg config.ProxyConfig) error {
	// request
	pkt, err := ReadPacket(reader)
	if err != nil {
		return err
	}
	if pkt.ID != 0x00 {
		return fmt.Errorf("expect packet Request, got %d", pkt.ID)
	}

	// fake ping mode
	if cfg.PingMode == "fake" {
		// response
		if publicIP := GetPublicIP(cfg.LocalAddr); publicIP != "" {
			cfg.Description += " (從: " + publicIP + " 連線)"
		}

		err = sendResponse(writer, protocol, cfg)
		if err != nil {
			return err
		}

		time.Sleep(time.Millisecond * time.Duration(cfg.FakePing))

		// ping
		pkt, err = ReadPacket(reader)
		if err != nil {
			return err
		}
		if pkt.ID != 0x01 {
			return fmt.Errorf("expect packet Ping, got %d", pkt.ID)
		}
		var payload Long
		_, err = pkt.Scan(&payload)
		if err != nil {
			return fmt.Errorf("scan ping: %w", err)
		}

		// pong
		pktBytes, err := Pack(payload)
		if err != nil {
			return fmt.Errorf("pack pong: %w", err)
		}
		err = WritePacket(0x01, pktBytes, writer)
		return err
	}

	// real ping mode
	if cfg.PingMode == "real" {
		// Connect to the target server
		log.Printf("[DEBUG] Pinging remote server: %s", cfg.Remote)
		if cfg.LocalAddr != "" {
			log.Printf("[DEBUG] Using local address for outgoing connection: %s", cfg.LocalAddr)
		}
		remote, err := DialMC(cfg.Remote, cfg.LocalAddr)
		if err != nil {
			log.Printf("[ERROR] Failed to connect to remote server %s for ping: %v", cfg.Remote, err)
			// If we can't connect to the remote server, fall back to fake response
			err = sendResponse(writer, protocol, cfg)
			if err != nil {
				return err
			}
			return handlePingFallback(reader, writer)
		}
		defer remote.Close()

		// Send handshake packet to remote server
		pktHandshake, err := Pack(
			VarInt(protocol),
			String(cfg.RewirteHost),
			UShort(cfg.RewirtePort),
			VarInt(1), // next state status
		)
		if err != nil {
			return err
		}
		err = WritePacket(0x00, pktHandshake, remote)
		if err != nil {
			log.Printf("[ERROR] Failed to send handshake to remote server: %v", err)
			// Fall back to fake response
			err = sendResponse(writer, protocol, cfg)
			if err != nil {
				return err
			}
			return handlePingFallback(reader, writer)
		}

		// Send request packet to remote server
		err = WritePacket(0x00, []byte{}, remote)
		if err != nil {
			log.Printf("[ERROR] Failed to send request to remote server: %v", err)
			// Fall back to fake response
			err = sendResponse(writer, protocol, cfg)
			if err != nil {
				return err
			}
			return handlePingFallback(reader, writer)
		}

		// Read response packet from remote server
		respPkt, err := ReadPacket(remote)
		if err != nil {
			log.Printf("[ERROR] Failed to read response from remote server: %v", err)
			// Fall back to fake response
			err = sendResponse(writer, protocol, cfg)
			if err != nil {
				return err
			}
			return handlePingFallback(reader, writer)
		}
		if respPkt.ID != 0x00 {
			log.Printf("[ERROR] Unexpected packet ID from remote server: %d", respPkt.ID)
			// Fall back to fake response
			err = sendResponse(writer, protocol, cfg)
			if err != nil {
				return err
			}
			return handlePingFallback(reader, writer)
		}

		// Forward the response to the client
		err = WritePacket(0x00, respPkt.Payload, writer)
		if err != nil {
			return err
		}

		// Read ping packet from client
		pingPkt, err := ReadPacket(reader)
		if err != nil {
			return err
		}
		if pingPkt.ID != 0x01 {
			return fmt.Errorf("expect packet Ping, got %d", pingPkt.ID)
		}

		// Set a deadline for writing the ping packet
		remote.SetWriteDeadline(time.Now().Add(5 * time.Second))

		// Send ping packet to remote server
		err = WritePacket(0x01, pingPkt.Payload, remote)

		// Clear the deadline
		remote.SetWriteDeadline(time.Time{})

		if err != nil {
			log.Printf("[ERROR] Failed to send ping to remote server: %v", err)
			// Fall back to echoing the ping
			var payload Long
			_, err = pingPkt.Scan(&payload)
			if err != nil {
				return fmt.Errorf("scan ping: %w", err)
			}
			pktBytes, err := Pack(payload)
			if err != nil {
				return fmt.Errorf("pack pong: %w", err)
			}
			err = WritePacket(0x01, pktBytes, writer)
			return err
		}

		// Set a deadline for reading the pong packet
		remote.SetReadDeadline(time.Now().Add(5 * time.Second))

		// Read pong packet from remote server
		pongPkt, err := ReadPacket(remote)

		// Clear the deadline
		remote.SetReadDeadline(time.Time{})

		if err != nil {
			log.Printf("[ERROR] Failed to read pong from remote server: %v", err)
			// Fall back to echoing the ping
			var payload Long
			_, err = pingPkt.Scan(&payload)
			if err != nil {
				return fmt.Errorf("scan ping: %w", err)
			}
			pktBytes, err := Pack(payload)
			if err != nil {
				return fmt.Errorf("pack pong: %w", err)
			}
			err = WritePacket(0x01, pktBytes, writer)
			return err
		}
		if pongPkt.ID != 0x01 {
			log.Printf("[ERROR] Unexpected packet ID from remote server: %d", pongPkt.ID)
			// Fall back to echoing the ping
			var payload Long
			_, err = pingPkt.Scan(&payload)
			if err != nil {
				return fmt.Errorf("scan ping: %w", err)
			}
			pktBytes, err := Pack(payload)
			if err != nil {
				return fmt.Errorf("pack pong: %w", err)
			}
			err = WritePacket(0x01, pktBytes, writer)
			return err
		}

		// Forward the pong to the client
		err = WritePacket(0x01, pongPkt.Payload, writer)
		return err
	}

	// This should never happen as the config validation ensures PingMode is either "fake" or "real"
	return fmt.Errorf("invalid ping mode: %s", cfg.PingMode)
}

// handlePingFallback handles the ping when we can't connect to the remote server
func handlePingFallback(reader io.Reader, writer io.Writer) error {
	// ping
	pkt, err := ReadPacket(reader)
	if err != nil {
		return err
	}
	if pkt.ID != 0x01 {
		return fmt.Errorf("expect packet Ping, got %d", pkt.ID)
	}
	var payload Long
	_, err = pkt.Scan(&payload)
	if err != nil {
		return fmt.Errorf("scan ping: %w", err)
	}

	// pong
	pktBytes, err := Pack(payload)
	if err != nil {
		return fmt.Errorf("pack pong: %w", err)
	}
	err = WritePacket(0x01, pktBytes, writer)
	return err
}
