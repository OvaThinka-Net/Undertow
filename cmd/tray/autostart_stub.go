//go:build !darwin && !windows

package main

import "log"

func isAutoStartEnabled() bool {
	return false
}

func enableAutoStart() error {
	log.Println("Auto-start not supported on this OS")
	return nil
}

func disableAutoStart() error {
	return nil
}

func ensureAutoStartPath() {}
