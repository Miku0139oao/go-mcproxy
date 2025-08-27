package config

import (
	"encoding/json"
	"log"
	"os"
)

// LogConfig contains configuration for the logging system
type LogConfig struct {
	DBPath string `json:"db_path"` // Path to the SQLite database file
}

type ProxyConfig struct {
	Listen      string   `json:"listen"`
	Description string   `json:"description"`
	Remote      string   `json:"remote"`
	LocalAddr   string   `json:"local_addr"` // Local address for outgoing connections
	Favicon     string   `json:"favicon"`
	MaxPlayer   int      `json:"max_player"`
	PingMode    string   `json:"ping_mode"` // fake, real
	FakePing    int      `json:"fake_ping"`
	RewirteHost string   `json:"rewrite_host"`
	RewirtePort int      `json:"rewrite_port"`
	Auth        string   `json:"auth"` // none, whitelist, blacklist
	Whitelist   []string `json:"whitelist"`
	Blacklist   []string `json:"blacklist"`
}

// Config represents the root configuration that can contain multiple proxy configurations
type Config struct {
	Proxies []ProxyConfig `json:"proxies"`
	Logging LogConfig     `json:"logging"`
}

// For backward compatibility
type LegacyConfig ProxyConfig

func ParseConfig(path string) *Config {

	bytes, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("[ERROR] Failed to read config %s: %s", path, err)
		return nil
	}

	// First try to parse as new multi-proxy config
	config := Config{}
	err = json.Unmarshal(bytes, &config)

	// If no proxies defined or error occurred, try to parse as legacy single-proxy config
	if err != nil || len(config.Proxies) == 0 {
		var legacyConfig LegacyConfig
		err = json.Unmarshal(bytes, &legacyConfig)
		if err != nil {
			log.Fatalf("[ERROR] Invalid JSON in config file: %s", err)
			return nil
		}

		// Convert legacy config to new format
		proxyConfig := ProxyConfig(legacyConfig)
		validateProxyConfig(&proxyConfig)
		config.Proxies = []ProxyConfig{proxyConfig}
		log.Printf("[INFO] Loaded legacy config format with single proxy: listen=%s, remote=%s", proxyConfig.Listen, proxyConfig.Remote)
	} else {
 	// Validate each proxy config in the new format
 	for i := range config.Proxies {
 		validateProxyConfig(&config.Proxies[i])
 		log.Printf("[INFO] Loaded proxy %d: listen=%s, remote=%s, auth=%s", 
 			i+1, config.Proxies[i].Listen, config.Proxies[i].Remote, config.Proxies[i].Auth)
 	}

 	// Set default logging configuration if not provided
 	if config.Logging.DBPath == "" {
 		config.Logging.DBPath = "logs/mcproxy.db"
 		log.Printf("[INFO] Using default logging database path: %s", config.Logging.DBPath)
 	} else {
 		log.Printf("[INFO] Using configured logging database path: %s", config.Logging.DBPath)
 	}
	}

	return &config
}

// validateProxyConfig validates a single proxy configuration
func validateProxyConfig(config *ProxyConfig) {
	if config.PingMode != "fake" && config.PingMode != "real" {
		log.Fatalf("[ERROR] Invalid ping_mode in config: %s", config.PingMode)
	}

	if config.Auth != "none" && config.Auth != "blacklist" && config.Auth != "whitelist" {
		log.Fatalf("[ERROR] Invalid auth in config: %s", config.Auth)
	}
}
