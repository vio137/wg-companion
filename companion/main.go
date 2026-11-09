package main

// wg-companion: userspace WireGuard tunnel + local SOCKS5 proxy, controlled
// by the WireGuard Companion Chrome extension over native messaging.
//
// Modes:
//   host                 native messaging host (what Chrome launches)
//   proxy -config X      standalone: bring tunnel up and serve SOCKS5
//   install              register this binary as a Chrome native messaging host
//   uninstall            remove the registration
//   service              run as the Windows background service (SCM only)
//   install-service      register + start the Windows service (admin)
//   uninstall-service    stop + remove the Windows service (admin)
//   start-service / stop-service

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const version = "0.2.0"

const socksListenAddr = "127.0.0.1:18080"

func main() {
	// Chrome launches the manifest path with its origin, not a "host" command.
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "chrome-extension://") {
		_ = runHost(hostController(), socksListenAddr)
		return
	}
	socksAddr := "127.0.0.1:18080"

	switch os.Args[1] {
	case "host":
		fs := flag.NewFlagSet("host", flag.ExitOnError)
		fs.StringVar(&socksAddr, "socks", socksAddr, "SOCKS5 listen address")
		fs.Parse(os.Args[2:])
		if err := runHost(hostController(), socksAddr); err != nil {
			fatal(err)
		}

	case "proxy":
		fs := flag.NewFlagSet("proxy", flag.ExitOnError)
		cfgName := fs.String("config", "", "config name or path to .conf file")
		fs.StringVar(&socksAddr, "socks", socksAddr, "SOCKS5 listen address")
		fs.Parse(os.Args[2:])
		if *cfgName == "" {
			fatal(fmt.Errorf("proxy mode needs -config"))
		}
		cfg, err := loadConfigArg(*cfgName)
		if err != nil {
			fatal(err)
		}
		t := NewTunnel()
		if err := t.Connect(cfg, socksAddr); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "tunnel up, SOCKS5 on %s (ctrl-c to stop)\n", socksAddr)
		waitInterrupt()
		t.Disconnect("shutdown")

	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		extID := fs.String("extension-id", "", "Chrome extension ID to allow")
		fs.Parse(os.Args[2:])
		if err := installHost(*extID); err != nil {
			fatal(err)
		}

	case "uninstall":
		if err := uninstallHost(); err != nil {
			fatal(err)
		}

	case "service":
		if err := runService(); err != nil {
			fatal(err)
		}
	case "install-service":
		if err := installService(); err != nil {
			fatal(err)
		}
	case "uninstall-service":
		if err := uninstallService(); err != nil {
			fatal(err)
		}
	case "start-service", "stop-service":
		if err := controlService(os.Args[1]); err != nil {
			fatal(err)
		}
	case "service-connect":
		fs := flag.NewFlagSet("service-connect", flag.ExitOnError)
		name := fs.String("name", "", "config name")
		fs.Parse(os.Args[2:])
		if err := serviceConnect(*name); err != nil {
			fatal(err)
		}
	case "service-disconnect":
		if err := serviceDisconnect(); err != nil {
			fatal(err)
		}
	case "service-status":
		if err := serviceStatus(); err != nil {
			fatal(err)
		}
	case "configs":
		list, err := ListConfigs()
		if err != nil {
			fatal(err)
		}
		if list == nil {
			list = []ConfigInfo{}
		}
		if err := printJSON(list); err != nil {
			fatal(err)
		}

	case "version":
		fmt.Println(version)

	default:
		usage()
		os.Exit(2)
	}
}

func loadConfigArg(arg string) (*WGConfig, error) {
	// A path (contains a separator or ends in .conf on disk) vs a saved name.
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		b, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(arg)
		return ParseWGConfig(name, string(b))
	}
	return LoadConfig(arg)
}

func usage() {
	fmt.Fprintf(os.Stderr, `wg-companion %s - userspace WireGuard tunnel for the WireGuard Companion Chrome extension

usage:
  wg-companion host                        Chrome native messaging host mode
  wg-companion proxy -config NAME|FILE     standalone tunnel+SOCKS5 (ctrl-c stops)
  wg-companion install -extension-id ID    register native messaging host for Chrome
  wg-companion uninstall                   remove registration
`, version)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func hostManifestDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts")}
	case "windows":
		return []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "wg-companion")}
	default: // linux and friends
		return []string{
			filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts"),
			filepath.Join(home, ".config", "chromium", "NativeMessagingHosts"),
		}
	}
}

func printJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}
