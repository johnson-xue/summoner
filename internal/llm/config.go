package llm

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProviderConfig represents LLM provider configuration
type ProviderConfig struct {
	Name       string `yaml:"name,omitempty"`
	BaseURL    string `yaml:"base_url"`
	APIKeyEnv  string `yaml:"api_key_env"`
	Model      string `yaml:"model"`
	ChatPath   string `yaml:"chat_path,omitempty"`
	AuthHeader string `yaml:"auth_header,omitempty"`
	AuthPrefix string `yaml:"auth_prefix,omitempty"`
}

// Config represents the LLM configuration
type Config struct {
	DefaultProvider string                    `yaml:"default_provider"`
	Providers       map[string]ProviderConfig `yaml:"providers"`
}

// setDefaults fills in default values for optional fields
func (p *ProviderConfig) setDefaults(name string) {
	if p.Name == "" {
		p.Name = name
	}
	if p.ChatPath == "" {
		p.ChatPath = "/v1/chat/completions"
	}
	if p.AuthHeader == "" {
		p.AuthHeader = "Authorization"
	}
	if p.AuthPrefix == "" {
		p.AuthPrefix = "Bearer"
	}

	// Security: Validate base URL to prevent SSRF attacks (S6)
	if err := validateBaseURL(p.BaseURL); err != nil {
		log.Printf("Warning: %v, disabling provider %s", err, name)
		p.BaseURL = "" // Disable provider with invalid URL
	}
}

// getDefaultConfig returns the built-in default configuration
func getDefaultConfig() Config {
	return Config{
		DefaultProvider: "litellm",
		Providers: map[string]ProviderConfig{
			"litellm": {
				Name:       "litellm",
				BaseURL:    "https://litellm.funplus.com.cn",
				APIKeyEnv:  "LITELLM_API_KEY",
				Model:      "gpt-4o",
				ChatPath:   "/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
			"deepseek": {
				Name:       "deepseek",
				BaseURL:    "https://api.deepseek.com",
				APIKeyEnv:  "DEEPSEEK_API_KEY",
				Model:      "deepseek-chat",
				ChatPath:   "/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
			"deepseek-anthropic": {
				Name:       "deepseek-anthropic",
				BaseURL:    "https://api.deepseek.com/anthropic",
				APIKeyEnv:  "DEEPSEEK_API_KEY",
				Model:      "deepseek-chat",
				ChatPath:   "/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
			"bigmodel": {
				Name:       "bigmodel",
				BaseURL:    "https://open.bigmodel.cn",
				APIKeyEnv:  "BIGMODEL_API_KEY",
				Model:      "glm-4",
				ChatPath:   "/api/paas/v4/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
			"bigmodel-anthropic": {
				Name:       "bigmodel-anthropic",
				BaseURL:    "https://open.bigmodel.cn/api/anthropic",
				APIKeyEnv:  "BIGMODEL_API_KEY",
				Model:      "glm-4",
				ChatPath:   "/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
			"openai": {
				Name:       "openai",
				BaseURL:    "https://api.openai.com",
				APIKeyEnv:  "OPENAI_API_KEY",
				Model:      "gpt-4",
				ChatPath:   "/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer",
			},
		},
	}
}

// LoadConfig loads LLM configuration from file or returns default
func LoadConfig() (*Config, error) {
	// 1. Get config file path
	configPath := os.Getenv("SUMMONER_LLM_CONFIG")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Printf("Warning: cannot get home directory: %v", err)
			defaultCfg := getDefaultConfig()
			return &defaultCfg, nil
		}
		configPath = filepath.Join(home, ".config", "summoner", "llm.yaml")
	}

	// 2. Try to load config file
	if _, err := os.Stat(configPath); err == nil {
		// Check file size to prevent OOM
		info, err := os.Stat(configPath)
		if err != nil {
			return nil, fmt.Errorf("stat config file %s: %w", configPath, err)
		}
		if info.Size() > 1*1024*1024 { // Limit to 1MB
			return nil, fmt.Errorf("config file too large: %d bytes (max 1MB)", info.Size())
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config file %s: %w", configPath, err)
		}

		var config Config
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("parse config file %s: %w", configPath, err)
		}

		logInfo("Loaded config from %s", configPath)
		return &config, nil
	} else if !os.IsNotExist(err) {
		// File exists but cannot access (permission issue, etc.)
		logWarn("Cannot access config file %s: %v", configPath, err)
	}

	// 3. Use default config
	logInfo("Using default LLM configuration")
	defaultCfg := getDefaultConfig()
	return &defaultCfg, nil
}

// validateBaseURL validates the base URL to prevent SSRF attacks
// Addresses S6: Unvalidated URL in LLM Client [MEDIUM]
func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("base URL cannot be empty")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow HTTPS (except localhost for development)
	if parsed.Scheme != "https" {
		hostname := parsed.Hostname()
		if hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1" {
			return fmt.Errorf("only HTTPS URLs allowed (except localhost)")
		}
	}

	// Block private IP ranges to prevent SSRF
	hostname := parsed.Hostname()
	privateRanges := []string{
		"10.", "172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.", "172.24.",
		"172.25.", "172.26.", "172.27.", "172.28.", "172.29.",
		"172.30.", "172.31.", "192.168.", "169.254.",
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(hostname, prefix) {
			return fmt.Errorf("private IP ranges not allowed")
		}
	}

	return nil
}

// GetProvider returns the current provider configuration
func (c *Config) GetProvider() (ProviderConfig, error) {
	// 1. Determine provider name
	providerName := os.Getenv("SUMMONER_LLM_PROVIDER")
	if providerName == "" {
		providerName = c.DefaultProvider
	}

	// 2. Get provider config
	provider, ok := c.Providers[providerName]
	if !ok {
		return ProviderConfig{}, fmt.Errorf("provider %s not found", providerName)
	}

	// 3. Set defaults
	provider.setDefaults(providerName)

	// 4. Environment variable overrides
	if baseURL := os.Getenv("SUMMONER_LLM_BASE_URL"); baseURL != "" {
		provider.BaseURL = baseURL
		logDebug("Overriding base URL: %s", baseURL)
	}
	if model := os.Getenv("SUMMONER_LLM_MODEL"); model != "" {
		provider.Model = model
		logDebug("Overriding model: %s", model)
	}

	return provider, nil
}
