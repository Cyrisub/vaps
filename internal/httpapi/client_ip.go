package httpapi

import (
	"net"
	"strings"
)

// NormalizeClientIP maps loopback addresses to "localhost" for storage/display.
func NormalizeClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	if ip == "" {
		return ""
	}
	parsed := net.ParseIP(ip)
	if parsed != nil && parsed.IsLoopback() {
		return "localhost"
	}
	switch strings.ToLower(ip) {
	case "localhost", "::1", "127.0.0.1":
		return "localhost"
	default:
		return ip
	}
}
