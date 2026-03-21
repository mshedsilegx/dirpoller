// Package main is the entry point for the DirPoller application.
//
// Objective:
// Orchestrate the bootstrap and execution lifecycle of the DirPoller application.
// It handles CLI argument parsing, configuration loading, and determines the
// execution mode (Standalone CLI or native Windows/Linux Service).
//
// Core Functions:
// - run: The testable main entry point that coordinates setup and engine execution.
// - handleWindowsService: Manages platform-specific service installation/lifecycle (misnamed for legacy reasons).
// - newEngine: Factory function (mockable) for initializing the core Engine.
//
// Data Flow:
// 1. Bootstrap: CLI flags are parsed to determine config paths and operation modes.
// 2. Configuration: The JSON config is loaded and optionally overridden by CLI flags.
// 3. Platform Detection: Checks if running under a service manager (SCM/systemd).
// 4. Orchestration: Initializes the Engine and manages graceful shutdown signals.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/service"
)

// EngineRunner defines the interface for the core processing engine.
type EngineRunner interface {
	Run(ctx context.Context) error
	Close()
}

var (
	version = "dev"
	// newEngine is used to mock service.NewEngine in tests.
	newEngine = func(cfg *config.Config, isService bool) (EngineRunner, error) {
		return service.NewEngine(cfg, isService)
	}
)

// run is the testable entry point for the DirPoller application.
//
// Objective: Orchestrate application startup, handle CLI flags, and manage
// transition between CLI and Service run modes.
//
// Data Flow:
// 1. Flag Parsing: Reads configuration path, service installation flags, and overrides.
// 2. Config Loading: Parses the JSON configuration and enforces mandatory fields.
// 3. Service Check: Determines if running as a native Service (SCM on Windows, systemd on Linux).
// 4. Installation/Removal: Handles service management logic if flags are set (-install/-remove).
// 5. Engine Bootstrap: Initializes the core processing engine.
// 6. Graceful Shutdown: Sets up signal handling (Interrupt/SIGTERM) for clean exits.
// 7. Execution: Starts the engine loop via engine.Run().
func run(args []string) int {
	// Reset flags for each call
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)

	configPath := flag.String("config", "", "Path to JSON configuration file")
	install := flag.Bool("install", false, "Install as service (Windows: Windows Service, Linux: systemd unit)")
	remove := flag.Bool("remove", false, "Remove service (Windows: Windows Service, Linux: systemd unit)")
	serviceName := flag.String("name", "", "Service name (Windows: optional override, Linux: REQUIRED 'unit@instance' format)")
	user := flag.String("user", "", "Service user account (Windows: optional, Linux: REQUIRED 'user:group')")
	pass := flag.String("pass", "", "Windows service user password (optional)")
	debug := flag.Bool("debug", false, "Run in debug mode")
	versionFlag := flag.Bool("version", false, "Print version information")
	logName := flag.String("log", "", "Enable custom logging with specific log name")
	logRetention := flag.Int("log-retention", 0, "Log retention in days (0 = disabled)")

	if err := flag.CommandLine.Parse(args[1:]); err != nil {
		return 1
	}

	if *versionFlag {
		fmt.Printf("DirPoller version: %s\n", version)
		return 0
	}

	if *configPath == "" {
		log.Println("-config argument is required")
		return 1
	}

	if !filepath.IsAbs(*configPath) {
		log.Printf("-config path must be an absolute path: %s", *configPath)
		return 1
	}

	absConfigPath := filepath.Clean(*configPath)

	// Run as CLI
	cfg, _, err := config.LoadConfig(absConfigPath)
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		return 1
	}

	// Override config with CLI flags
	if *logName != "" {
		if !filepath.IsAbs(*logName) {
			log.Printf("Log path must be an absolute path: %s", *logName)
			return 1
		}
		cfg.Logging = []config.LoggingConfig{
			{
				LogName:      *logName,
				LogRetention: *logRetention,
			},
		}
	} else if *logRetention > 0 && len(cfg.Logging) > 0 {
		cfg.Logging[0].LogRetention = *logRetention
	}

	// Override config service name if flag is provided
	if *serviceName != "" {
		cfg.ServiceName = *serviceName
	}

	// Handle Windows-specific service logic
	if handled, exitCode := handleWindowsService(cfg, absConfigPath, *debug, *install, *remove, *user, *pass); handled {
		return exitCode
	}

	engine, err := newEngine(cfg, false)
	if err != nil {
		log.Printf("Failed to create engine: %v", err)
		return 1
	}
	defer engine.Close()

	// Set up context with signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Starting DirPoller CLI with algorithm: %s (Press Ctrl+C to exit)\n", cfg.Poll.Algorithm)
	if err := engine.Run(ctx); err != nil && err != context.Canceled {
		log.Printf("Engine stopped: %v", err)
		return 1
	}
	fmt.Println("DirPoller CLI stopped gracefully.")
	return 0
}

func main() {
	code := run(os.Args)
	if code != 0 {
		os.Exit(code)
	}
}
