package main

import (
	"flag"
	"log"
	"mcproxy/config"
	"mcproxy/core"
	"mcproxy/logger"
	"os"
	"time"
)

const version = "2.1.0"

func main() {
	// Set up formatted logging with timestamp, file location, and log level
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.SetOutput(os.Stdout)
	log.Printf("[INFO] gomcproxy (version %s) starting up", version)

	configPath := flag.String("config", "config.json", "path to config.json")
	controlPanelAddr := flag.String("control", "0.0.0.0:8080", "control panel address")
	balancerAddr := flag.String("balancer", "", "load balancer address (e.g., 0.0.0.0:25565)")
	flag.Parse()

	startTime := time.Now()
	cfg := config.ParseConfig(*configPath)
	log.Printf("[INFO] Configuration loaded in %v", time.Since(startTime))

	// Initialize the logger
	l := logger.GetLogger()
	err := l.Initialize(cfg.Logging.DBPath)
	if err != nil {
		log.Fatalf("[ERROR] Failed to initialize logger: %v", err)
	}
	defer l.Close()

	l.Info("gomcproxy (version %s) starting up with SQLite logging", version)

	// Initialize the control panel
	core.InitControlPanel(cfg, *configPath)

	// Start the control panel
	l.Info("Starting control panel on %s", *controlPanelAddr)
	core.StartControlPanel(*controlPanelAddr)

	// If balancer address is provided, start the load balancer
	if *balancerAddr != "" {
		l.Info("Starting load balancer on %s", *balancerAddr)
		go core.StartBalancer(*balancerAddr, cfg)
	}

	// Start the proxy servers
	go core.Start(*cfg)

	// Create a channel to keep the main goroutine alive
	keepAlive := make(chan struct{})
	l.Info("Server is now running. Press Ctrl+C to exit.")

	// Block indefinitely
	<-keepAlive
}
