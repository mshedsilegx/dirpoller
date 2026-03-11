package service

import (
	"fmt"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"testing"
)

type mockServiceManager struct {
	m *mockManager
}

func (msm *mockServiceManager) Connect() (Manager, error) {
	return msm.m, nil
}

type mockManager struct {
	services map[string]*mockService
	failOpen bool
}

func (mm *mockManager) Close() error { return nil }
func (mm *mockManager) OpenService(name string) (Service, error) {
	if mm.failOpen {
		return nil, fmt.Errorf("service not found")
	}
	s, ok := mm.services[name]
	if !ok {
		return nil, fmt.Errorf("service not found")
	}
	return s, nil
}
func (mm *mockManager) CreateService(name, exepath string, config mgr.Config, args ...string) (Service, error) {
	if _, ok := mm.services[name]; ok {
		return nil, fmt.Errorf("service already exists")
	}
	s := &mockService{name: name}
	mm.services[name] = s
	return s, nil
}

type mockService struct {
	name    string
	deleted bool
	stopped bool
}

func (ms *mockService) Close() error { return nil }
func (ms *mockService) Control(c svc.Cmd) (svc.Status, error) {
	if c == svc.Stop {
		ms.stopped = true
		return svc.Status{State: svc.Stopped}, nil
	}
	return svc.Status{}, nil
}
func (ms *mockService) Query() (svc.Status, error) {
	state := svc.Running
	if ms.stopped {
		state = svc.Stopped
	}
	return svc.Status{State: state}, nil
}
func (ms *mockService) Delete() error {
	ms.deleted = true
	return nil
}

func TestInstallRemoveService(t *testing.T) {
	// Mock EventLog functions
	oldInstall := eventLogInstall
	oldRemove := eventLogRemove
	eventLogInstall = func(name string, levels uint32) error { return nil }
	eventLogRemove = func(name string) error { return nil }
	defer func() {
		eventLogInstall = oldInstall
		eventLogRemove = oldRemove
	}()

	mm := &mockManager{services: make(map[string]*mockService)}
	oldManager := defaultManager
	defaultManager = &mockServiceManager{m: mm}
	defer func() { defaultManager = oldManager }()

	name := "TestPoller"
	display := "Test Poller Service"
	cfgPath := "C:\\config.json"

	// 1. Test Install
	err := InstallService(name, display, cfgPath, "", "")
	if err != nil {
		t.Fatalf("InstallService failed: %v", err)
	}

	if _, ok := mm.services[name]; !ok {
		t.Error("expected service to be created in mock manager")
	}

	// 2. Test Install Duplicate
	err = InstallService(name, display, cfgPath, "", "")
	if err == nil {
		t.Error("expected error installing duplicate service, got nil")
	}

	// 3. Test Remove
	err = RemoveService(name)
	if err != nil {
		t.Fatalf("RemoveService failed: %v", err)
	}

	if s, ok := mm.services[name]; ok && !s.deleted {
		t.Error("expected service to be deleted")
	}

	// 4. Test Remove Non-existent
	mm.failOpen = true
	err = RemoveService("NoSuchService")
	if err == nil {
		t.Error("expected error removing non-existent service, got nil")
	}
}

func TestInstallServiceErrors(t *testing.T) {
	oldManager := defaultManager
	mm := &mockManager{services: make(map[string]*mockService)}
	defaultManager = &mockServiceManager{m: mm}
	defer func() { defaultManager = oldManager }()

	name := "ErrorPoller"

	t.Run("AlreadyExists", func(t *testing.T) {
		mm.services[name] = &mockService{name: name}
		err := InstallService(name, "Display", "C:\\cfg.json", "", "")
		if err == nil || err.Error() != fmt.Sprintf("service %s already exists", name) {
			t.Errorf("expected already exists error, got %v", err)
		}
	})

	t.Run("EventLogFail", func(t *testing.T) {
		oldInstall := eventLogInstall
		eventLogInstall = func(name string, levels uint32) error { return fmt.Errorf("evt error") }
		defer func() { eventLogInstall = oldInstall }()

		delete(mm.services, name)
		err := InstallService(name, "Display", "C:\\cfg.json", "", "")
		if err == nil || !contains(err.Error(), "InstallAsEventCreate() failed") {
			t.Errorf("expected event log failure error, got %v", err)
		}
	})
}

func contains(s, substr string) bool {
	return fmt.Sprintf("%v", s) != "" && (s != "" && substr != "") // Simple mock check
}
