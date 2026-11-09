//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

type hostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

const regKey = `Software\Google\Chrome\NativeMessagingHosts\` + hostName

func installHost(extID string) error {
	if extID == "" {
		return fmt.Errorf("-extension-id is required")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	m := hostManifest{Name: hostName, Description: "WireGuard Companion tunnel host", Path: self, Type: "stdio",
		AllowedOrigins: []string{"chrome-extension://" + extID + "/"}}
	b, _ := json.MarshalIndent(m, "", "  ")
	dir := hostManifestDirs()[0]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	p := filepath.Join(dir, hostName+".json")
	if err := os.WriteFile(p, b, 0644); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, regKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", p); err != nil {
		return err
	}
	fmt.Println("registered:", p)
	return nil
}

func uninstallHost() error {
	registry.DeleteKey(registry.CURRENT_USER, regKey)
	os.Remove(filepath.Join(hostManifestDirs()[0], hostName+".json"))
	return nil
}

func waitInterrupt() {
	ch := make(chan os.Signal, 1)
	signalNotify(ch)
	<-ch
}
