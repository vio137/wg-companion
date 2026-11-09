//go:build !windows

package main

import "fmt"

// On unix the native messaging host owns the tunnel in-process; there is no
// service layer (the Windows service/pipe model is Windows-only).
func hostController() tunnelController { return NewTunnel() }

func runService() error       { return fmt.Errorf("service mode is Windows-only") }
func installService() error   { return fmt.Errorf("services are Windows-only") }
func uninstallService() error { return fmt.Errorf("services are Windows-only") }
func controlService(cmd string) error {
	return fmt.Errorf("services are Windows-only")
}
