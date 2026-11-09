//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type hostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func installHost(extID string) error {
	if extID == "" {
		return fmt.Errorf("-extension-id is required (load the extension once, copy its ID, or use the pinned ID from the README)")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	m := hostManifest{
		Name:           hostName,
		Description:    "WireGuard Companion tunnel host",
		Path:           self,
		Type:           "stdio",
		AllowedOrigins: []string{"chrome-extension://" + extID + "/"},
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	for _, dir := range hostManifestDirs() {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		p := filepath.Join(dir, hostName+".json")
		if err := os.WriteFile(p, b, 0644); err != nil {
			return err
		}
		fmt.Println("registered:", p)
	}
	return nil
}

func uninstallHost() error {
	for _, dir := range hostManifestDirs() {
		p := filepath.Join(dir, hostName+".json")
		if err := os.Remove(p); err == nil {
			fmt.Println("removed:", p)
		}
	}
	return nil
}

func waitInterrupt() {
	ch := make(chan os.Signal, 1)
	// signal.Notify for SIGINT/SIGTERM
	signalNotify(ch)
	<-ch
}
