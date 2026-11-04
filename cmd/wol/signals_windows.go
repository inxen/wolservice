//go:build windows

package main

import "os"

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func reloadSignal(os.Signal) bool {
	return false
}
