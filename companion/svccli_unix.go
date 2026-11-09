//go:build !windows

package main

import "fmt"

func serviceConnect(name string) error { return fmt.Errorf("services are Windows-only") }
func serviceDisconnect() error         { return fmt.Errorf("services are Windows-only") }
func serviceStatus() error             { return fmt.Errorf("services are Windows-only") }
