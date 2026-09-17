package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"controlpanel/internal/app"
	"controlpanel/internal/config"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Printf("invalid configuration: %v", err)
		os.Exit(1)
	}
	if err := app.Run(ctx, cfg); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
