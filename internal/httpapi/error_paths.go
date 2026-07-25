package httpapi

import "strings"

func isMonitoredErrorPath(path string) bool {
	path = strings.TrimSpace(path)
	switch {
	case path == "/health":
		return true
	case strings.HasPrefix(path, "/dashboard"):
		return true
	case strings.HasPrefix(path, "/v2/"):
		return true
	default:
		return false
	}
}

func isDashboardErrorPath(path string) bool {
	return strings.HasPrefix(strings.TrimSpace(path), "/dashboard")
}
