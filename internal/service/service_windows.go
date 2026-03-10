package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

type WindowsService struct {
	cfgPath string
}

func (m *WindowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
	changes <- svc.Status{State: svc.StartPending}

	cfg, err := config.LoadConfig(m.cfgPath)
	if err != nil {
		return false, 1
	}

	engine, err := NewEngine(cfg, true)
	if err != nil {
		return false, 2
	}
	defer engine.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				// Testing of pause/continue
				time.Sleep(100 * time.Millisecond)
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				// We don't break loop here, we wait for errChan to return from engine.Run
			case svc.Pause:
				changes <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
			case svc.Continue:
				changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
			default:
				engine.logError(fmt.Sprintf("Unexpected control request #%d", c))
			}
		case err := <-errChan:
			if err != nil && err != context.Canceled {
				engine.logError(fmt.Sprintf("Engine stopped with error: %v", err))
				errno = 3 // custom exit code for engine failure
			}
			break loop
		}
	}
	changes <- svc.Status{State: svc.StopPending}
	return
}

func RunService(name string, cfgPath string, isDebug bool) {
	var err error
	if isDebug {
		err = debug.Run(name, &WindowsService{cfgPath: cfgPath})
	} else {
		err = svc.Run(name, &WindowsService{cfgPath: cfgPath})
	}
	if err != nil {
		elog, err := eventlog.Open(name)
		if err == nil {
			_ = elog.Error(1, fmt.Sprintf("Service %s failed: %v", name, err))
			_ = elog.Close()
		}
	}
}

// InstallService installs the application as a Windows service.
func InstallService(name, display, cfgPath, user, pass string) error {
	exepath, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()

	s, err := m.OpenService(name)
	if err == nil {
		_ = s.Close()
		return fmt.Errorf("service %s already exists", name)
	}

	config := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  display,
		Description:  "Multi-threaded Directory Poller Service",
	}

	if user != "" {
		config.ServiceStartName = user
		config.Password = pass
	}

	s, err = m.CreateService(name, exepath, config, "-config", cfgPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = s.Close()
	}()

	err = eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		_ = s.Delete()
		return fmt.Errorf("InstallAsEventCreate() failed: %w", err)
	}
	return nil
}

// RemoveService removes the application from Windows services.
func RemoveService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() {
		_ = m.Disconnect()
	}()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %s is not installed", name)
	}
	defer func() {
		_ = s.Close()
	}()

	// Attempt to stop the service if it's running
	status, err := s.Control(svc.Stop)
	if err == nil {
		// Wait up to 10 seconds for service to stop
		timeout := time.Now().Add(10 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(timeout) {
			time.Sleep(300 * time.Millisecond)
			status, err = s.Query()
			if err != nil {
				break
			}
		}
	}

	err = s.Delete()
	if err != nil {
		return err
	}
	err = eventlog.Remove(name)
	if err != nil {
		return fmt.Errorf("RemoveEventLogSource() failed: %w", err)
	}
	return nil
}
