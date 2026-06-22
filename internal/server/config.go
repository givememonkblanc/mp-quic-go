package server

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mp-quic-go/internal/mpquic/path"
)

const uint32MaxPathID = 0xFFFFFFFF

func DefaultConfig() *Config {
	return &Config{
		ListenAddr:       ":4433",
		MaxIdleTimeout:   30 * time.Second,
		MaxStreamNum:     100,
		InitialMaxPathID: 1,
		MaxPathID:        4,
		ServerName:       "mp-quic-server",
		EdgeEnvFile:      "",
		CertFile:         filepath.Join("config", "tls", "cert.pem"),
		KeyFile:          filepath.Join("config", "tls", "key.pem"),
	}
}

// ParsePathInterfaces parses a comma-separated list of "interface=pathID" pairs.
// Example: "wlan0=0,wlan1=1"
func ParsePathInterfaces(input string) (map[string]path.ID, error) {
	result := make(map[string]path.ID)
	if input == "" {
		return result, nil
	}
	ifaceSet := make(map[string]bool)
	pathIDSet := make(map[path.ID]bool)
	pairs := strings.Split(input, ",")
	for i, pair := range pairs {
		originalPair := pair
		pair = strings.TrimSpace(pair)
		if pair == "" {
			// Empty pair (e.g., trailing comma, leading comma, or double comma)
			if i == 0 && len(pairs) == 1 {
				continue // empty string case already handled above
			}
			return nil, pathInterfaceError("empty path interface pair in: %q", input)
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, pathInterfaceError("invalid path interface pair: %q (expected iface=pathID)", originalPair)
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "" {
			return nil, pathInterfaceError("empty interface name in pair: %q", originalPair)
		}
		if ifaceSet[iface] {
			return nil, pathInterfaceError("duplicate interface %q in path_interfaces", iface)
		}
		ifaceSet[iface] = true
		pathID64, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return nil, pathInterfaceError("invalid path ID in pair %q: %v", originalPair, err)
		}
		if pathID64 < 0 {
			return nil, pathInterfaceError("negative path ID in pair %q", originalPair)
		}
		if pathID64 > uint32MaxPathID {
			return nil, pathInterfaceError("path ID overflow in pair %q", originalPair)
		}
		pathID := path.ID(pathID64)
		if pathIDSet[pathID] {
			return nil, pathInterfaceError("duplicate path ID %d in path_interfaces", pathID64)
		}
		pathIDSet[pathID] = true
		result[iface] = pathID
	}
	return result, nil
}

func pathInterfaceError(format string, args ...interface{}) error {
	return &pathInterfaceParseError{msg: fmt.Sprintf(format, args...)}
}

type pathInterfaceParseError struct {
	msg string
}

func (e *pathInterfaceParseError) Error() string {
	return e.msg
}
