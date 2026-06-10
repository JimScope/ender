package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModemConfig holds the configuration for a single modem.
type ModemConfig struct {
	APIKey      string `yaml:"api_key"`
	CommandPort string `yaml:"command_port"`
	NotifyPort  string `yaml:"notify_port"`
	SimPIN      string `yaml:"sim_pin"`
	Profile     string `yaml:"profile"`
}

// Config holds the global configuration.
type Config struct {
	VendelURL string        `yaml:"vendel_url"`
	Modems    []ModemConfig `yaml:"modems"`
}

// loadDotEnv loads KEY=VALUE pairs from a .env file into the process
// environment. Variables already set in the environment take precedence.
// A missing file is not an error — .env is optional.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip optional surrounding quotes
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' ||
			value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

func loadConfig() Config {
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = "modems.yaml"
	}

	// A bind mount of a host path that does not exist yet makes Docker create
	// an empty DIRECTORY at the target, so os.ReadFile below would fail with an
	// opaque "is a directory". Detect it and explain the real cause.
	if info, err := os.Stat(configFile); err == nil && info.IsDir() {
		log.Fatalf("config path %s is a directory, not a file — create it on the host before starting the container (cp modem-agent/modems.example.yaml modem-agent/modems.yaml)", configFile)
	}

	checkConfigPermissions(configFile)

	data, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatalf("failed to read config file %s: %v", configFile, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("failed to parse config file %s: %v", configFile, err)
	}

	// VENDEL_URL env var overrides the config file value
	if envURL := os.Getenv("VENDEL_URL"); envURL != "" {
		cfg.VendelURL = envURL
	}
	if cfg.VendelURL == "" {
		cfg.VendelURL = "http://localhost:8090"
	}

	if err := validateConfig(&cfg); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	return cfg
}

// checkConfigPermissions warns when the config file (which contains device
// API keys in plain text) is readable by group/others. chmod 600 is the
// recommended mode.
func checkConfigPermissions(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return // the read below will report the real error
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		log.Printf("WARNING: %s is readable by group/others (mode %04o) and contains API keys — run: chmod 600 %s", path, mode, path)
	}
}

func validateConfig(cfg *Config) error {
	if err := validateVendelURL(cfg.VendelURL); err != nil {
		return err
	}
	if len(cfg.Modems) == 0 {
		return fmt.Errorf("no modems configured")
	}
	for i := range cfg.Modems {
		m := &cfg.Modems[i]
		if m.APIKey == "" {
			return fmt.Errorf("modem[%d]: api_key is required", i)
		}
		if m.CommandPort == "" {
			return fmt.Errorf("modem[%d]: command_port is required", i)
		}
		// Default notify_port to command_port (single-port modem)
		if m.NotifyPort == "" {
			m.NotifyPort = m.CommandPort
		}
	}
	return nil
}

// validateVendelURL refuses to start with a plain-http URL pointing at a
// non-loopback host: every request carries the device API key in a header,
// so it would cross the network in clear text. Loopback is fine (local
// dev), and VENDEL_ALLOW_INSECURE_HTTP=true downgrades the error to a
// warning for trusted networks (e.g. an internal docker bridge).
func validateVendelURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("vendel_url %q is not a valid URL: %w", rawURL, err)
	}

	switch u.Scheme {
	case "https":
		return nil
	case "http":
		// continue with the loopback check below
	default:
		return fmt.Errorf("vendel_url %q: unsupported scheme %q (use http or https)", rawURL, u.Scheme)
	}

	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}

	if allow := os.Getenv("VENDEL_ALLOW_INSECURE_HTTP"); allow == "true" || allow == "1" {
		log.Printf("WARNING: using plain http to non-loopback host %q — API keys travel unencrypted (VENDEL_ALLOW_INSECURE_HTTP is set)", host)
		return nil
	}

	return fmt.Errorf("vendel_url %q uses plain http to a non-loopback host: API keys would travel unencrypted. Use https, or set VENDEL_ALLOW_INSECURE_HTTP=true if the network is trusted (e.g. docker internal)", rawURL)
}
