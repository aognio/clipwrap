// Package main is the entry point for the clipwrapd daemon.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/clipwrap/clipwrapd/internal/auth"
	"github.com/clipwrap/clipwrapd/internal/broker"
	"github.com/clipwrap/clipwrapd/internal/config"
	"github.com/clipwrap/clipwrapd/internal/domain"
	"github.com/clipwrap/clipwrapd/internal/history"
	"github.com/clipwrap/clipwrapd/internal/httpserver"
	"github.com/clipwrap/clipwrapd/internal/ingest"
)

var version = "0.1.0-dev"

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]

	if len(args) == 0 {
		// Start daemon with default config
		return startDaemon("")
	}

	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			printUsage()
			return 0
		case "--version", "-v":
			fmt.Printf("clipwrapd %s\n", version)
			return 0
		case "--check-config":
			return checkConfig(args)
		case "--set-admin-password":
			return setPassword(args)
		default:
			if strings.HasPrefix(arg, "--config=") {
				// Will be handled in startDaemon
			}
		}
	}

	// If no special flag matched, start daemon
	return startDaemon(resolveConfig(args))
}

func printUsage() {
	fmt.Println("clipwrapd: clipboard capture and distribution daemon")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  clipwrapd                          Start the daemon")
	fmt.Println("  clipwrapd --config PATH            Start with config file")
	fmt.Println("  clipwrapd --set-admin-password     Set administrator password")
	fmt.Println("  clipwrapd --check-config           Validate configuration")
	fmt.Println("  clipwrapd --version                Print version")
	fmt.Println("  clipwrapd --help                   Print this help")
}

func resolveConfig(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config=")
		}
		if arg == "--config" {
			// Next arg is the path
			for i, a := range args {
				if a == "--config" && i+1 < len(args) {
					return args[i+1]
				}
			}
		}
	}
	return ""
}

func checkConfig(args []string) int {
	path := config.ResolveConfigPath(resolveConfig(args))
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Println("Configuration is valid.")
	if !cfg.HasPassword() {
		fmt.Println("Warning: no administrator password is set.")
	}
	return 0
}

func setPassword(args []string) int {
	path := config.ResolveConfigPath(resolveConfig(args))

	// Check if config exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: config file not found: %s\n", path)
		fmt.Fprintf(os.Stderr, "Create it first: cp clipwrap-server.example.toml %s\n", path)
		return 1
	}

	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return 1
	}

	// Prompt for password
	fmt.Print("Enter new password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError reading password: %v\n", err)
		return 1
	}
	fmt.Println()

	if len(password) < 8 {
		fmt.Fprintf(os.Stderr, "Error: password must be at least 8 characters\n")
		return 1
	}

	fmt.Print("Confirm password: ")
	confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError reading password: %v\n", err)
		return 1
	}
	fmt.Println()

	if string(password) != string(confirm) {
		fmt.Fprintf(os.Stderr, "Error: passwords do not match\n")
		return 1
	}

	hash, err := auth.HashPassword(string(password))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error hashing password: %v\n", err)
		return 1
	}

	cfg.Auth.PasswordHash = hash
	if err := config.Save(path, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		return 1
	}

	fmt.Println("Password set successfully.")
	fmt.Println("Restart the daemon for changes to take effect.")
	return 0
}

func startDaemon(configPath string) int {
	path := config.ResolveConfigPath(configPath)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return 1
	}

	if !cfg.HasPassword() {
		fmt.Fprintf(os.Stderr, "Error: no administrator password is set.\n")
		fmt.Fprintf(os.Stderr, "Run: clipwrapd --set-admin-password --config %s\n", path)
		return 1
	}

	// Create components
	instanceID := domain.NewInstanceID()
	store := history.NewStore(instanceID, cfg.History.Capacity)
	b := broker.NewBroker(cfg.WebSocket.ReplayLimit)

	// Ingestion listener
	ingestCfg := ingest.IngestConfig{
		SocketPath:     cfg.Ingest.Socket,
		MaxItemBytes:   cfg.Ingest.MaxItemBytes,
		MaxConnections: cfg.Ingest.MaxConnections,
		SocketMode:     0660,
	}
	ingestListener := ingest.NewListener(ingestCfg, store, b)

	// Ensure socket directory exists
	if err := os.MkdirAll(cfg.Ingest.Socket[:strings.LastIndex(cfg.Ingest.Socket, "/")], 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating socket directory: %v\n", err)
		return 1
	}

	if err := ingestListener.Listen(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting ingest listener: %v\n", err)
		return 1
	}

	// HTTP server
	srv := httpserver.NewServer(cfg, store, b)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpSrv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	srv.Start(httpSrv)

	fmt.Printf("clipwrapd %s starting (instance %s)\n", version, instanceID)
	fmt.Printf("HTTP listening on %s\n", cfg.Server.Listen)
	fmt.Printf("Ingest socket: %s\n", cfg.Ingest.Socket)

	// Start HTTP server
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ingestListener.Shutdown(5 * time.Second)
	httpSrv.Shutdown(ctx)
	b.Shutdown()

	fmt.Println("Shutdown complete.")
	return 0
}
