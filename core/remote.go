package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func Resolve(address string) (string, error) {
	// SRV
	if !strings.Contains(address, ":") {
		_, addrs, err := net.LookupSRV("minecraft", "tcp", address)

		if err != nil || len(addrs) == 0 {
			// use default port if SRV failed
			return net.JoinHostPort(address, "25565"), nil
		}

		return net.JoinHostPort(addrs[0].Target, strconv.Itoa(int(addrs[0].Port))), nil
	}

	return address, nil
}

func DialMC(a string, localAddr string) (net.Conn, error) {
	addr, err := Resolve(a)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	// If localAddr is specified, use it for the outgoing connection
	if localAddr != "" {

		// Parse the local address
		local, err := net.ResolveTCPAddr("tcp", localAddr)
		if err != nil {
			return nil, fmt.Errorf("resolve local TCP addr: %w", err)
		}

		// Create a dialer with the local address and timeout
		dialer := &net.Dialer{
			LocalAddr: local,
			Timeout:   5 * time.Second, // Add a 5-second timeout
		}

		// Dial with the specified local address
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial with local addr %s: %w", localAddr, err)
		}

		return conn, nil
	}

	// Otherwise, use the system default with timeout
	dialer := &net.Dialer{
		Timeout: 5 * time.Second, // Add a 5-second timeout
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	return conn, nil
}
