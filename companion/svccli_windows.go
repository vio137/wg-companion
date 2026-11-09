//go:build windows

package main

// CLI verbs the tray app (and power users) use to drive the service. They run
// as the logged-in user: configs are read from the user's DPAPI-protected
// store here and passed to the service over the pipe, so SYSTEM never sees
// the config at rest.

import (
	"fmt"
)

func serviceConnect(name string) error {
	if name == "" {
		list, _ := ListConfigs()
		switch len(list) {
		case 0:
			return fmt.Errorf("no config imported yet")
		case 1:
			name = list[0].Name
		default:
			return fmt.Errorf("multiple configs; specify -name")
		}
	}
	cfg, err := LoadConfig(name)
	if err != nil {
		return err
	}
	ctl := newPipeController()
	if err := ctl.Connect(cfg, socksListenAddr); err != nil {
		return err
	}
	return printJSON(ctl.Stats())
}

func serviceDisconnect() error {
	ctl := newPipeController()
	ctl.Disconnect("cli")
	return printJSON(ctl.Stats())
}

func serviceStatus() error { return printJSON(newPipeController().Stats()) }
