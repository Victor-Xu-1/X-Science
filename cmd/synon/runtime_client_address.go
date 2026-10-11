package main

import (
	"net"

	"synon-go/internal/config"
)

// runtimeClientAddress keeps local clients in the listener's address family.
// An unspecified IPv6 listener is not necessarily reachable over IPv4.
func runtimeClientAddress(cfg config.Config) (string, error) {
	host, port, err := net.SplitHostPort(cfg.Address())
	if err != nil {
		return "", err
	}
	if host == "" {
		host = "127.0.0.1"
	} else if address := net.ParseIP(host); address != nil && address.IsUnspecified() {
		host = "127.0.0.1"
		if address.To4() == nil {
			host = "::1"
		}
	}
	return net.JoinHostPort(host, port), nil
}

func browserLoginURL(cfg config.Config) string {
	address, err := runtimeClientAddress(cfg)
	if err != nil {
		return "http://" + cfg.Address() + "/#/login"
	}
	return "http://" + address + "/#/login"
}
