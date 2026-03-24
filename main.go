package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"

	"FileEngine/internal/api"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func main() {
	// Load config
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database
	if err := db.Init(cfg.Database); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	repo := db.NewRepository(db.GetDB())

	// Setup frontend static files
	var frontend fs.FS
	subFS, err := fs.Sub(frontendFS, "web/dist")
	if err != nil {
		log.Printf("Warning: frontend not embedded: %v", err)
	} else {
		frontend = subFS
	}

	// Create and start server
	server := api.NewServer(repo, frontend)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("Starting FileEngine on %s", addr)
	if err := server.Run(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
