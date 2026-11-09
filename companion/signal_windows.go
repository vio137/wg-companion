//go:build windows

package main

import (
	"os"
	"os/signal"
)

func signalNotify(ch chan<- os.Signal) { signal.Notify(ch, os.Interrupt) }
