//go:build windows

package main

// Service registration with the Windows Service Control Manager. Requires
// elevation (the installer runs it). The service runs as LocalService so the
// tunnel survives sign-out and Chrome restarts; it holds no user secrets on
// disk (see service_windows.go).

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const regPath = `SOFTWARE\WGCompanion`

// recordOwnerSID stores the installing user's SID in HKLM so the service can
// ACL its control pipe to exactly that user (not every interactive user).
// Runs at install time, elevated; the value is a SID, not a secret.
// desktopOwnerSID resolves the shell in this installer's interactive session,
// not the elevated token and not an unrelated active-console/RDP account.
// Alternate-admin UAC therefore still assigns controls to the desktop user.
// No shell / session mismatch fails closed; unattended installs need a future
// explicit owner-selection design, not an Administrators-only guess.
func desktopOwnerSID() (*windows.SID, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	hwnd, _, _ := user32.NewProc("GetShellWindow").Call()
	if hwnd == 0 {
		return nil, fmt.Errorf("no interactive desktop shell; install from the intended user's desktop")
	}
	var pid uint32
	user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	var selfSession, shellSession uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &selfSession); err != nil {
		return nil, err
	}
	if err := windows.ProcessIdToSessionId(pid, &shellSession); err != nil {
		return nil, err
	}
	if selfSession == 0 || selfSession != shellSession {
		return nil, fmt.Errorf("installer and desktop shell sessions differ")
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(proc)
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}
func recordOwnerSID() error {
	sid, err := desktopOwnerSID()
	if err != nil {
		return fmt.Errorf("resolve installing desktop user: %w", err)
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, regPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("write owner SID (run as administrator): %w", err)
	}
	defer k.Close()
	return k.SetStringValue("OwnerSID", sid.String())
}

// ownerSID reads the installing user's SID recorded by recordOwnerSID.
func ownerSID() (*windows.SID, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("owner SID not registered (install-service not run?): %w", err)
	}
	defer k.Close()
	s, _, err := k.GetStringValue("OwnerSID")
	if err != nil {
		return nil, err
	}
	return windows.StringToSid(s)
}

const serviceDesc = "WireGuard Companion tunnel: userspace WireGuard + local SOCKS5 proxy for the Chrome extension."

func svcManager() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	return m, nil
}

// No writes: used before the installer copies or replaces any binaries.
func preflightService() error {
	if _, err := desktopOwnerSID(); err != nil {
		return err
	}
	m, err := svcManager()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("existing service found; uninstall it before updating")
	}
	return nil
}

func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := svcManager()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already installed; uninstall-service first", serviceName)
	}
	if err := recordOwnerSID(); err != nil {
		return err
	}
	s, err = m.CreateService(serviceName, exe, mgr.Config{
		DisplayName:      "WireGuard Companion",
		Description:      serviceDesc,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: `NT AUTHORITY\LocalService`,
	}, "service")
	if err != nil {
		_ = registry.DeleteKey(registry.LOCAL_MACHINE, regPath)
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	_ = eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err := s.Start(); err != nil {
		_ = s.Delete()
		_ = eventlog.Remove(serviceName)
		_ = registry.DeleteKey(registry.LOCAL_MACHINE, regPath)
		return fmt.Errorf("service startup failed; registration rolled back: %w", err)
	}
	if err := installMachineHost(); err != nil {
		_, _ = s.Control(svc.Stop)
		_ = s.Delete()
		uninstallMachineHost()
		return fmt.Errorf("registration failed; service rolled back: %w", err)
	}
	fmt.Println("service", serviceName, "installed and started")
	return nil
}

func uninstallService() error {
	m, err := svcManager()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	for i := 0; i < 20; i++ {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	uninstallMachineHost()
	_ = eventlog.Remove(serviceName)
	fmt.Println("service", serviceName, "removed")
	return nil
}

func controlService(cmd string) error {
	m, err := svcManager()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	switch cmd {
	case "start-service":
		return s.Start()
	case "stop-service":
		_, err := s.Control(svc.Stop)
		return err
	}
	return fmt.Errorf("unknown command %s", cmd)
}
