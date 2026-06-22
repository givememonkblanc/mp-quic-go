package server

import (
	"testing"
)

func TestParsePathInterfacesEmpty(t *testing.T) {
	result, err := ParsePathInterfaces("")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestParsePathInterfacesWhitespaceOnly(t *testing.T) {
	result, err := ParsePathInterfaces("   \t\n  ")
	if err != nil {
		t.Fatalf("unexpected error for whitespace-only input: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestParsePathInterfacesSingle(t *testing.T) {
	result, err := ParsePathInterfaces("wlan0=0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected one mapping, got %v", result)
	}
	if result["wlan0"] != 0 {
		t.Fatalf("expected wlan0 to map to path 0, got %v", result)
	}
}

func TestParsePathInterfacesMultiple(t *testing.T) {
	result, err := ParsePathInterfaces("wlan0=0,wlan1=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected two mappings, got %v", result)
	}
	if result["wlan0"] != 0 {
		t.Fatalf("expected wlan0 to map to path 0, got %v", result)
	}
	if result["wlan1"] != 1 {
		t.Fatalf("expected wlan1 to map to path 1, got %v", result)
	}
}

func TestParsePathInterfacesWhitespace(t *testing.T) {
	result, err := ParsePathInterfaces("  wlan0 = 0 , wlan1 = 1  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected two mappings, got %v", result)
	}
	if result["wlan0"] != 0 {
		t.Fatalf("expected wlan0 to map to path 0, got %v", result)
	}
	if result["wlan1"] != 1 {
		t.Fatalf("expected wlan1 to map to path 1, got %v", result)
	}
}

func TestParsePathInterfacesAllowsLargeValidPathID(t *testing.T) {
	result, err := ParsePathInterfaces("wlan0=4294967295")
	if err != nil {
		t.Fatalf("unexpected error for large uint32 path id: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected one mapping, got %v", result)
	}
	if result["wlan0"] != 4294967295 {
		t.Fatalf("expected wlan0 to map to path 4294967295, got %v", result)
	}
}

func TestParsePathInterfacesRejectsInvalidFormat(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0")
	if err == nil {
		t.Fatal("expected error for missing = in pair")
	}
}

func TestParsePathInterfacesRejectsExtraEquals(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=0=1")
	if err == nil {
		t.Fatal("expected error for extra = in pair")
	}
}

func TestParsePathInterfacesRejectsEmptyInterface(t *testing.T) {
	_, err := ParsePathInterfaces("=0")
	if err == nil {
		t.Fatal("expected error for empty interface name")
	}
}

func TestParsePathInterfacesRejectsEmptyPathID(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=")
	if err == nil {
		t.Fatal("expected error for empty path ID")
	}
}

func TestParsePathInterfacesRejectsInvalidPathID(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=abc")
	if err == nil {
		t.Fatal("expected error for non-numeric path ID")
	}
}

func TestParsePathInterfacesRejectsNegativePathID(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=-1")
	if err == nil {
		t.Fatal("expected error for negative path ID")
	}
}

func TestParsePathInterfacesRejectsTrailingComma(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=0,")
	if err == nil {
		t.Fatal("expected error for trailing comma")
	}
}

func TestParsePathInterfacesRejectsLeadingComma(t *testing.T) {
	_, err := ParsePathInterfaces(",wlan0=0")
	if err == nil {
		t.Fatal("expected error for leading comma")
	}
}

func TestParsePathInterfacesRejectsEmptyPair(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=0,,wlan1=1")
	if err == nil {
		t.Fatal("expected error for empty pair")
	}
}

func TestParsePathInterfacesRejectsDuplicateInterface(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=0,wlan0=1")
	if err == nil {
		t.Fatal("expected error for duplicate interface")
	}
}

func TestParsePathInterfacesRejectsDuplicatePathID(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=0,wlan1=0")
	if err == nil {
		t.Fatal("expected error for duplicate path ID")
	}
}

func TestParsePathInterfacesRejectsPathIDOverflow(t *testing.T) {
	_, err := ParsePathInterfaces("wlan0=4294967296")
	if err == nil {
		t.Fatal("expected error for path ID overflow")
	}
}