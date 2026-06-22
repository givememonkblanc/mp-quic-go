package integration

import (
	"net"
	"testing"
	"time"

	"mp-quic-go/internal/server"
)

func TestDefaultConfig(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.ListenAddr != ":4433" {
		t.Fatalf("unexpected listen addr: got=%q want=%q", cfg.ListenAddr, ":4433")
	}

	if cfg.MaxIdleTimeout != 30*time.Second {
		t.Fatalf("unexpected max idle timeout: got=%s want=%s", cfg.MaxIdleTimeout, 30*time.Second)
	}

	if cfg.InitialMaxPathID != 1 {
		t.Fatalf("unexpected initial max path id: got=%d want=%d", cfg.InitialMaxPathID, 1)
	}

	if cfg.MaxPathID != 4 {
		t.Fatalf("unexpected max path id: got=%d want=%d", cfg.MaxPathID, 4)
	}

	if cfg.CertFile == "" {
		t.Fatal("expected cert file path to be set")
	}

	if cfg.KeyFile == "" {
		t.Fatal("expected key file path to be set")
	}
}

func TestDefaultConfigPathIDRangeIsValid(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.MaxPathID < cfg.InitialMaxPathID {
		t.Fatalf(
			"expected MaxPathID >= InitialMaxPathID, got MaxPathID=%d InitialMaxPathID=%d",
			cfg.MaxPathID,
			cfg.InitialMaxPathID,
		)
	}
}

func TestDefaultConfigListenAddrIsResolvable(t *testing.T) {
	cfg := server.DefaultConfig()

	addr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	if err != nil {
		t.Fatalf("default listen addr should be resolvable as UDP addr: %v", err)
	}

	if addr.Port == 0 {
		t.Fatalf("expected non-zero default listen port, got %s", addr)
	}
}

func TestDefaultConfigHasPositiveStreamLimit(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.MaxStreamNum == 0 {
		t.Fatal("expected MaxStreamNum to be greater than zero")
	}
}

func TestDefaultConfigHasReasonableIdleTimeout(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.MaxIdleTimeout <= 0 {
		t.Fatalf("expected positive MaxIdleTimeout, got %s", cfg.MaxIdleTimeout)
	}

	if cfg.MaxIdleTimeout < time.Second {
		t.Fatalf("MaxIdleTimeout is too small for integration tests: %s", cfg.MaxIdleTimeout)
	}
}

func TestDefaultConfigPathInterfacesAreParseableWhenSet(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.PathInterfaces == "" {
		return
	}

	parsed, err := server.ParsePathInterfaces(cfg.PathInterfaces)
	if err != nil {
		t.Fatalf("default PathInterfaces should be parseable: %v", err)
	}

	if len(parsed) == 0 {
		t.Fatalf("default PathInterfaces is non-empty but parsed to empty mapping: %q", cfg.PathInterfaces)
	}
}

func TestDefaultConfigReturnsIndependentValues(t *testing.T) {
	first := server.DefaultConfig()
	second := server.DefaultConfig()

	first.ListenAddr = "127.0.0.1:9999"
	first.MaxIdleTimeout = time.Second
	first.InitialMaxPathID = 99
	first.MaxPathID = 100
	first.CertFile = "changed-cert.pem"
	first.KeyFile = "changed-key.pem"

	if second.ListenAddr == first.ListenAddr {
		t.Fatal("DefaultConfig should return independent config values; ListenAddr was shared")
	}

	if second.MaxIdleTimeout == first.MaxIdleTimeout {
		t.Fatal("DefaultConfig should return independent config values; MaxIdleTimeout was shared")
	}

	if second.InitialMaxPathID == first.InitialMaxPathID {
		t.Fatal("DefaultConfig should return independent config values; InitialMaxPathID was shared")
	}

	if second.MaxPathID == first.MaxPathID {
		t.Fatal("DefaultConfig should return independent config values; MaxPathID was shared")
	}

	if second.CertFile == first.CertFile {
		t.Fatal("DefaultConfig should return independent config values; CertFile was shared")
	}

	if second.KeyFile == first.KeyFile {
		t.Fatal("DefaultConfig should return independent config values; KeyFile was shared")
	}
}

func TestDefaultConfigDoesNotEnableEdgeRSSIByDefault(t *testing.T) {
	cfg := server.DefaultConfig()

	if cfg.EdgeEnvFile != "" {
		t.Fatalf("expected EdgeEnvFile to be empty by default, got %q", cfg.EdgeEnvFile)
	}
}

func TestDefaultConfigPathAddressesEmptyByDefault(t *testing.T) {
	cfg := server.DefaultConfig()

	if len(cfg.PathAddresses) != 0 {
		t.Fatalf("expected no default additional path addresses, got %v", cfg.PathAddresses)
	}
}