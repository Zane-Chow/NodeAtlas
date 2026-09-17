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
	"controlpanel/internal/httpapi"
	"controlpanel/internal/webassets"
)

var version = "dev"

func main() {
	address := flag.String("address", "127.0.0.1:8080", "HTTP listen address")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	router := httpapi.NewRouter(httpapi.Dependencies{Assets: webassets.FileSystem()})
	if err := app.Run(ctx, *address, router); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
