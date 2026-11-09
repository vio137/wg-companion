//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
)

const pinnedExtensionID = "dfcikohmdkdkkaampidiknlgkognlkah"
const startupKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Machine manifest lives with protected program binaries, never in a user's
// writable directory. The service pipe still permits only the selected owner.
func installMachineHost() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	manifest := filepath.Join(filepath.Dir(exe), hostName+".json")
	b, err := json.MarshalIndent(hostManifest{Name: hostName, Description: "WireGuard Companion service host", Path: exe, Type: "stdio", AllowedOrigins: []string{"chrome-extension://" + pinnedExtensionID + "/"}}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(manifest, b, 0644); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, regKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	err = k.SetStringValue("", manifest)
	k.Close()
	if err != nil {
		return err
	}
	sid, err := ownerSID()
	if err != nil {
		return err
	}
	// This is the installing desktop user's already-loaded hive, not HKCU of
	// an alternate administrator supplied to UAC.
	run, _, err := registry.CreateKey(registry.USERS, sid.String()+`\`+startupKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("register tray for desktop owner: %w", err)
	}
	defer run.Close()
	return run.SetStringValue("WGCompanion", `"`+filepath.Join(filepath.Dir(exe), "wg-tray.exe")+`"`)
}
func uninstallMachineHost() {
	if sid, err := ownerSID(); err == nil {
		if k, err := registry.OpenKey(registry.USERS, sid.String()+`\`+startupKey, registry.SET_VALUE); err == nil {
			k.DeleteValue("WGCompanion")
			k.Close()
		}
	}
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, regKey)
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(filepath.Join(filepath.Dir(exe), hostName+".json"))
	}
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, regPath)
}
