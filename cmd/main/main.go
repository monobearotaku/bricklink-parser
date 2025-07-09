package main

import (
	"context"

	"bricklink/parser/internal/config"
	"bricklink/parser/internal/container"

	log "github.com/sirupsen/logrus"
)

func main() {
	log.Info("Starting BrickLink parser...")

	// Load configuration using viper
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	log.Info("Configuration loaded successfully")

	// Initialize container with all dependencies
	app, err := container.New(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize container: %v", err)
	}

	// Run the application
	if err := app.Run(context.Background()); err != nil {
		log.Fatalf("Application exited with error: %v", err)
	}

	log.Info("Application finished successfully")
}
