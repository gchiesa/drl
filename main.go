package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("DRL - Distributed Rate Limiter starting...")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("DRL is running. Press Ctrl+C to exit.")
	<-sigChan

	log.Println("DRL shutting down...")
}
