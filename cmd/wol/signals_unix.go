//go:build !windows

package main

import (
	"os"
	"syscall"
)

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

func reloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
