package rssi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEdgeConfig(t *testing.T) {
	t.Run("loads required fields and sets default command", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_address = '192.168.0.13'
ssh_id = 'jetson'
ssh_password = 'secret'
`)

		cfg, err := LoadEdgeConfig(envPath)
		if err != nil {
			t.Fatalf("load edge config: %v", err)
		}

		if cfg.Address != "192.168.0.13" {
			t.Fatalf("expected address 192.168.0.13, got %q", cfg.Address)
		}
		if cfg.User != "jetson" {
			t.Fatalf("expected user jetson, got %q", cfg.User)
		}
		if cfg.Password != "secret" {
			t.Fatalf("expected password secret, got %q", cfg.Password)
		}
		if cfg.Command == "" {
			t.Fatal("expected default command to be set")
		}
	})

	t.Run("loads values with double quotes", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_address = "192.168.0.13"
ssh_id = "jetson"
ssh_password = "secret"
`)

		cfg, err := LoadEdgeConfig(envPath)
		if err != nil {
			t.Fatalf("load edge config: %v", err)
		}

		if cfg.Address != "192.168.0.13" || cfg.User != "jetson" || cfg.Password != "secret" {
			t.Fatalf("unexpected config: %#v", cfg)
		}
	})

	t.Run("loads values without quotes", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_address = 192.168.0.13
ssh_id = jetson
ssh_password = secret
`)

		cfg, err := LoadEdgeConfig(envPath)
		if err != nil {
			t.Fatalf("load edge config: %v", err)
		}

		if cfg.Address != "192.168.0.13" || cfg.User != "jetson" || cfg.Password != "secret" {
			t.Fatalf("unexpected config: %#v", cfg)
		}
	})

	t.Run("ignores blank lines and comments", func(t *testing.T) {
		envPath := writeTempEnv(t, `
# edge ssh config

ssh_address = '192.168.0.13'

# login
ssh_id = 'jetson'
ssh_password = 'secret'
`)

		cfg, err := LoadEdgeConfig(envPath)
		if err != nil {
			t.Fatalf("load edge config: %v", err)
		}

		if cfg.Address != "192.168.0.13" || cfg.User != "jetson" || cfg.Password != "secret" {
			t.Fatalf("unexpected config: %#v", cfg)
		}
	})

	t.Run("returns error when file does not exist", func(t *testing.T) {
		_, err := LoadEdgeConfig(filepath.Join(t.TempDir(), "missing.env"))
		if err == nil {
			t.Fatal("expected error for missing env file")
		}
	})

	t.Run("returns error when address is missing", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_id = 'jetson'
ssh_password = 'secret'
`)

		_, err := LoadEdgeConfig(envPath)
		if err == nil {
			t.Fatal("expected error for missing ssh_address")
		}
	})

	t.Run("returns error when user is missing", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_address = '192.168.0.13'
ssh_password = 'secret'
`)

		_, err := LoadEdgeConfig(envPath)
		if err == nil {
			t.Fatal("expected error for missing ssh_id")
		}
	})

	t.Run("returns error when password is missing", func(t *testing.T) {
		envPath := writeTempEnv(t, `
ssh_address = '192.168.0.13'
ssh_id = 'jetson'
`)

		_, err := LoadEdgeConfig(envPath)
		if err == nil {
			t.Fatal("expected error for missing ssh_password")
		}
	})
}

func TestParseRSSIOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]int
		wantErr bool
	}{
		{
			name:  "single interface",
			input: "wlan0:-47\n",
			want: map[string]int{
				"wlan0": -47,
			},
		},
		{
			name:  "multiple interfaces",
			input: "wlan0:-47\nwlan1:-62\n",
			want: map[string]int{
				"wlan0": -47,
				"wlan1": -62,
			},
		},
		{
			name:  "trims whitespace around interface and value",
			input: " wlan0 : -47 \n wlan1 : -62 \n",
			want: map[string]int{
				"wlan0": -47,
				"wlan1": -62,
			},
		},
		{
			name:  "supports CRLF line endings",
			input: "wlan0:-47\r\nwlan1:-62\r\n",
			want: map[string]int{
				"wlan0": -47,
				"wlan1": -62,
			},
		},
		{
			name:  "ignores blank lines",
			input: "\nwlan0:-47\n\nwlan1:-62\n",
			want: map[string]int{
				"wlan0": -47,
				"wlan1": -62,
			},
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only whitespace output",
			input:   " \n \t \n",
			wantErr: true,
		},
		{
			name:    "invalid format without separator",
			input:   "not-a-valid-line\n",
			wantErr: true,
		},
		{
			name:    "invalid format with too many separators",
			input:   "wlan0:-47:extra\n",
			wantErr: true,
		},
		{
			name:    "missing interface name",
			input:   ":-47\n",
			wantErr: true,
		},
		{
			name:    "missing rssi value",
			input:   "wlan0:\n",
			wantErr: true,
		},
		{
			name:    "non numeric rssi value",
			input:   "wlan0:not-number\n",
			wantErr: true,
		},
		{
			name:    "mixed valid and invalid lines should fail",
			input:   "wlan0:-47\ninvalid-line\n",
			wantErr: true,
		},
		{
			name:  "duplicate interface keeps latest value",
			input: "wlan0:-70\nwlan0:-55\n",
			want: map[string]int{
				"wlan0": -55,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRSSIOutput([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected parse error for input %q, got result %v", tt.input, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected parse error for input %q: %v", tt.input, err)
			}

			assertRSSIMapEqual(t, got, tt.want)
		})
	}
}

func writeTempEnv(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	return envPath
}

func assertRSSIMapEqual(t *testing.T, got, want map[string]int) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected RSSI map length: got=%v want=%v", got, want)
	}

	for iface, wantRSSI := range want {
		gotRSSI, ok := got[iface]
		if !ok {
			t.Fatalf("missing interface %q in result: got=%v", iface, got)
		}

		if gotRSSI != wantRSSI {
			t.Fatalf("unexpected RSSI for %s: got=%d want=%d", iface, gotRSSI, wantRSSI)
		}
	}
}