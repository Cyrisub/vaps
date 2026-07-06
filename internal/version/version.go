package version

import "fmt"

// Version and Channel are set at build time via -ldflags.
var (
	Version = "dev"
	Channel = "dev"
)

// String returns human-readable version and channel information.
func String() string {
	return fmt.Sprintf("vaps %s (%s)", Version, Channel)
}
