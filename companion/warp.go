package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// No registration happens until the user presses Create after checking terms.
// Keep wgcf logs private: they can contain account credentials.
func registerWARP() error {
	if _, err := LoadConfig("WARP"); err == nil {
		return fmt.Errorf("WARP profile already exists; remove it first to replace it")
	}
	d, err := configDir()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(d, ".registration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	self, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(filepath.Dir(self), "wgcf")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	account := filepath.Join(tmp, "account.toml")
	profile := filepath.Join(tmp, "profile.conf")
	cmd := exec.CommandContext(ctx, bin, "--config", account, "register", "--accept-tos")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WARP registration failed; wgcf may be missing, Cloudflare unavailable, or its API changed. Try importing a profile instead")
	}
	cmd = exec.CommandContext(ctx, bin, "--config", account, "generate", "--profile", profile, "--keepalive=25")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WARP profile generation failed; retry later or import an existing profile")
	}
	b, err := os.ReadFile(profile)
	if err != nil {
		return fmt.Errorf("could not read generated profile")
	}
	if _, err := SaveConfig("WARP", string(b)); err != nil {
		return fmt.Errorf("could not save WARP profile: %w", err)
	}
	// Keep the account for future management, encrypted with DPAPI on Windows.
	if b, err := os.ReadFile(account); err == nil {
		if err := writeSecret(filepath.Join(d, "warp-account.bin"), b); err != nil {
			return fmt.Errorf("profile saved but account backup failed")
		}
	}
	return nil
}
