package httpapi

import (
	"regexp"
	"strings"
)

// UE client example:
// NFDemo/UE5-CL-0 (http-eventloop) Windows/10.0.22631.1.256.64bit
var ueUserAgentPattern = regexp.MustCompile(`(?i)^([^/\s]+)/(UE[^\s]*)\s+\(([^)]+)\)\s+(Windows|Linux|Mac|Darwin|Android|iOS)/(\S+)$`)

type ParsedUserAgent struct {
	Raw          string `json:"raw"`
	Parsed       bool   `json:"parsed"`
	Project      string `json:"project,omitempty"`
	Engine       string `json:"engine,omitempty"`
	Transport    string `json:"transport,omitempty"`
	System       string `json:"system,omitempty"`
	SystemDetail string `json:"system_detail,omitempty"`
}

func ParseUserAgent(raw string) ParsedUserAgent {
	raw = strings.TrimSpace(raw)
	result := ParsedUserAgent{Raw: raw}
	if raw == "" {
		return result
	}
	matches := ueUserAgentPattern.FindStringSubmatch(raw)
	if matches == nil {
		return result
	}
	result.Parsed = true
	result.Project = matches[1]
	result.Engine = matches[2]
	result.Transport = matches[3]
	result.System = matches[4]
	result.SystemDetail = matches[5]
	return result
}
