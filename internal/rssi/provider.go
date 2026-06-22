package rssi

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const DefaultEdgeEnvPath = "/home/ryzen395/mpquic/.env"

const defaultRSSICommand = `bash -lc 'for iface in $(iw dev 2>/dev/null | awk '"'"'$1=="Interface"{print $2}'"'"'); do val=$(iw dev "$iface" link 2>/dev/null | awk '"'"'/signal:/ {print int($2); exit}'"'"'); if [ -n "$val" ]; then echo "$iface:$val"; fi; done'`

type Provider interface {
	FetchRSSI(context.Context) (map[string]int, error)
	Source() string
}

type EdgeConfig struct {
	Address  string
	User     string
	Password string
	Command  string
	Timeout  time.Duration
	EnvPath  string
}

type SSHProvider struct {
	config EdgeConfig
	dial   func(network, addr string, config *ssh.ClientConfig) (*ssh.Client, error)
}

func LoadEdgeConfig(path string) (EdgeConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return EdgeConfig{}, err
	}
	defer file.Close()

	cfg := EdgeConfig{
		Command: defaultRSSICommand,
		Timeout: 5 * time.Second,
		EnvPath: path,
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := trimEnvValue(parts[1])
		switch key {
		case "ssh_address":
			cfg.Address = value
		case "ssh_id":
			cfg.User = value
		case "ssh_password":
			cfg.Password = value
		case "ssh_command":
			if value != "" {
				cfg.Command = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return EdgeConfig{}, err
	}
	if cfg.Address == "" || cfg.User == "" || cfg.Password == "" {
		return EdgeConfig{}, fmt.Errorf("incomplete edge ssh config in %s", path)
	}
	return cfg, nil
}

func NewSSHProviderFromEnv(path string) (*SSHProvider, error) {
	cfg, err := LoadEdgeConfig(path)
	if err != nil {
		return nil, err
	}
	return &SSHProvider{config: cfg, dial: ssh.Dial}, nil
}

func (p *SSHProvider) FetchRSSI(ctx context.Context) (map[string]int, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	client, err := p.dial("tcp", p.config.Address+":22", &ssh.ClientConfig{
		User:            p.config.User,
		Auth:            []ssh.AuthMethod{ssh.Password(p.config.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         p.config.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial edge host: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(p.config.Command)
	if err != nil {
		return nil, fmt.Errorf("run rssi command: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return parseRSSIOutput(output)
}

func (p *SSHProvider) Source() string {
	return p.config.Address
}

func trimEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, `"'`)
	return value
}

func parseRSSIOutput(output []byte) (map[string]int, error) {
	result := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimSpace(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid rssi line (expected iface:value): %q", line)
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "" {
			return nil, fmt.Errorf("empty interface name in rssi line: %q", line)
		}
		val, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("parse rssi for interface %q: %w", iface, err)
		}
		result[iface] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no valid rssi values found in output: %q", strings.TrimSpace(string(output)))
	}
	return result, nil
}
