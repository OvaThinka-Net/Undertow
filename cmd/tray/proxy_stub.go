//go:build !darwin && !windows

package main

import "log"

func enableProxy(host string, port int) error {
	log.Printf("Auto-proxy not supported on this OS. Manually set SOCKS5 proxy to %s:%d", host, port)
	return nil
}

func disableProxy() error {
	return nil
}
