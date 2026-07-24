package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vaultdb/internal/crm"
)

func main() {
	port := flag.Int("port", 9090, "Port for VaultCRM HTTP Server")
	dataDir := flag.String("data", "./data_crm", "Data directory for VaultCRM")
	flag.Parse()

	log.Printf("[VaultCRM] Starting VaultCRM testing application...")
	log.Printf("[VaultCRM] Storage Directory: %s", *dataDir)

	crmDB, err := crm.InitCRMDatabase(*dataDir)
	if err != nil {
		log.Fatalf("[VaultCRM] Failed to initialize database: %v", err)
	}
	defer crmDB.Close()

	service := crm.NewCRMService(crmDB)
	server := crm.NewServer(service)

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[VaultCRM] Server listening on http://localhost:%d", *port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[VaultCRM] Server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("[VaultCRM] Shutting down gracefully...")
}
