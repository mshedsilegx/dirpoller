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

func main() {
	configPath := flag.String("config", "", "Path to JSON configuration file")
	install := flag.Bool("install", false, "Install as Windows service")
	remove := flag.Bool("remove", false, "Remove Windows service")
	user := flag.String("user", "", "Windows service user account (optional, default: LocalSystem)")
	pass := flag.String("pass", "", "Windows service user password (optional)")
	debug := flag.Bool("debug", false, "Run in debug mode")
	versionFlag := flag.Bool("version", false, "Print version information")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("DirPoller version: %s\n", version)
		return
	}

	if *install || *remove {
		if !isAdmin() {
			log.Fatal("Administrative privileges are required to install or remove the service. Please run as Administrator.")
		}
	}

	if *configPath == "" {
		log.Fatal("-config argument is required")
	}

	absConfigPath, err := filepath.Abs(*configPath)
	if err != nil {
		log.Fatalf("Failed to get absolute path for config: %v", err)
	}

	// Check if running as a service
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Failed to determine if running as service: %v", err)
	}

	if isService {
		service.RunService("DirPoller", absConfigPath, *debug)
		return
	}

	if *install {
		err = service.InstallService("DirPoller", "Directory Poller", absConfigPath, *user, *pass)
		if err != nil {
			log.Fatalf("Failed to install service: %v", err)
		}
		fmt.Println("Service installed successfully.")
		return
	}

	if *remove {
		err = service.RemoveService("DirPoller")
		if err != nil {
			log.Fatalf("Failed to remove service: %v", err)
		}
		fmt.Println("Service removed successfully.")
		return
	}

	// Run as CLI
	cfg, err := config.LoadConfig(absConfigPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
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
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}
