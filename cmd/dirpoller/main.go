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
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

var version = "dev"

// main is the entry point for the DirPoller application.
// It handles CLI flag parsing, Windows service installation/removal,
// and orchestrates the transition between CLI and Service run modes.
func main() {
	configPath := flag.String("config", "", "Path to JSON configuration file")
	install := flag.Bool("install", false, "Install as Windows service")
	remove := flag.Bool("remove", false, "Remove Windows service")
	serviceName := flag.String("name", "", "Custom Windows service name (optional, defaults to config or 'DirPoller')")
	user := flag.String("user", "", "Windows service user account (optional, default: LocalSystem)")
	pass := flag.String("pass", "", "Windows service user password (optional)")
	debug := flag.Bool("debug", false, "Run in debug mode")
	versionFlag := flag.Bool("version", false, "Print version information")
	logName := flag.String("log", "", "Enable custom logging with specific log name")
	logRetention := flag.Int("log-retention", 0, "Log retention in days (0 = disabled)")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("DirPoller version: %s\n", version)
		return
	}

	if *configPath == "" {
		log.Fatal("-config argument is required")
	}

	absConfigPath, err := filepath.Abs(*configPath)
	if err != nil {
		log.Fatalf("Failed to get absolute path for config: %v", err)
	}

	// Run as CLI
	cfg, err := config.LoadConfig(absConfigPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Override config with CLI flags
	if *logName != "" {
		cfg.Logging = []config.LoggingConfig{
			{
				LogName:      *logName,
				LogRetention: *logRetention,
			},
		}
	} else if *logRetention > 0 && len(cfg.Logging) > 0 {
		cfg.Logging[0].LogRetention = *logRetention
	}

	if *install || *remove {
		if !isAdmin() {
			log.Fatal("Administrative privileges are required to install or remove the service. Please run as Administrator.")
		}
	}

	// Override config service name if flag is provided
	if *serviceName != "" {
		cfg.ServiceName = *serviceName
	}

	// Check if running as a service
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Failed to determine if running as service: %v", err)
	}

	if isService {
		service.RunService(cfg.ServiceName, absConfigPath, *debug)
		return
	}

	if *install {
		displayName := fmt.Sprintf("Directory Poller (%s)", cfg.ServiceName)
		if cfg.ServiceName == "DirPoller" {
			displayName = "Directory Poller"
		}
		err = service.InstallService(cfg.ServiceName, displayName, absConfigPath, *user, *pass)
		if err != nil {
			log.Fatalf("Failed to install service: %v", err)
		}
		fmt.Printf("Service '%s' installed successfully.\n", cfg.ServiceName)
		return
	}

	if *remove {
		err = service.RemoveService(cfg.ServiceName)
		if err != nil {
			log.Fatalf("Failed to remove service: %v", err)
		}
		fmt.Printf("Service '%s' removed successfully.\n", cfg.ServiceName)
		return
	}

	engine, err := service.NewEngine(cfg, false)
	if err != nil {
		log.Fatalf("Failed to create engine: %v", err)
	}
	defer engine.Close()

	// Set up context with signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Starting DirPoller CLI with algorithm: %s (Press Ctrl+C to exit)\n", cfg.Poll.Algorithm)
	if err := engine.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Engine stopped: %v", err)
	}
	fmt.Println("DirPoller CLI stopped gracefully.")
}

// isAdmin checks if the current process is running with administrative privileges.
// This is required for installing or removing Windows services and EventLog sources.
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid)
	if err != nil {
		return false
	}
	defer func() {
		_ = windows.FreeSid(sid)
	}()

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}
