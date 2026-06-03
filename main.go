package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"

	"github.com/gchiesa/drl/internal/cmd"
)

var version = "development"

func main() {
	if strings.ToLower(strings.TrimSpace(os.Getenv("DRL_DEBUG_PPROF"))) == "true" {
		go func() {
			log.Println("Starting pprof server on :6060")
			if err := http.ListenAndServe(":6060", nil); err != nil {
				log.Fatalf("pprof server failed: %v", err)
			}
		}()
	}
	cmd.Execute(version)
}
