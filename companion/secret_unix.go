//go:build !windows

package main

import "os"

func writeSecret(p string, b []byte) error { return os.WriteFile(p, b, 0600) }
func readSecret(p string) ([]byte, error)  { return os.ReadFile(p) }
