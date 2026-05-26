package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// BindConfig holds the minimal local settings for a single SMPP bind.
// Every other SMPP bind parameter (host, port, system_id, password, TON/NPI…)
// is fetched from the backend using the device api_key, so operators don't
// need to duplicate secrets in the agent's YAML.
type BindConfig struct {
	APIKey string `yaml:"api_key"`
}

// Config holds the global configuration.
type Config struct {
	VendelURL string       `yaml:"vendel_url"`
	Binds     []BindConfig `yaml:"binds"`
}

func loadConfig() Config {
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = "configs.yaml"
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatalf("failed to read config file %s: %v", configFile, err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		log.Fatalf("failed to parse config file %s: %v", configFile, err)
	}

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

func validateConfig(cfg *Config) error {
	if len(cfg.Binds) == 0 {
		return fmt.Errorf("no SMPP binds configured")
	}
	for i, b := range cfg.Binds {
		if b.APIKey == "" {
			return fmt.Errorf("binds[%d]: api_key is required", i)
		}
	}
	return nil
}
