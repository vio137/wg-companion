//go:build windows

package main

import "syscall"

var sysProcHideWindow = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
