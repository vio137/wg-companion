//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"wireguard-companion/internal/serviceipc"
)

func verifyPipeServer(h windows.Handle) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return serviceipc.VerifyServer(h, self)
}
