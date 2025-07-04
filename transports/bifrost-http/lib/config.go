// Package lib provides core functionality for the Bifrost HTTP service,
// including context propagation, header management, and integration with monitoring systems.
package lib

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/core/schemas/meta"
)

// ProviderConfig represents the configuration for a specific AI model provider.
// It includes API keys, network settings, provider-specific metadata, and concurrency settings.
type ProviderConfig struct {
	Keys                     []schemas.Key                     `json:"keys"`                                  // API keys for the provider
	NetworkConfig            *schemas.NetworkConfig            `json:"network_config,omitempty"`              // Network-related settings
	MetaConfig               *schemas.MetaConfig               `json:"-"`                                     // Provider-specific metadata
	ConcurrencyAndBufferSize *schemas.ConcurrencyAndBufferSize `json:"concurrency_and_buffer_size,omitempty"` // Concurrency settings
}

// ConfigMap maps provider names to their configurations.
type ConfigMap map[schemas.ModelProvider]ProviderConfig

// BifrostHTTPConfig represents the complete configuration structure for Bifrost HTTP transport.
// It includes both provider configurations and MCP configuration.
type BifrostHTTPConfig struct {
	ProviderConfig ConfigMap          `json:"providers"` // Provider configurations
	MCPConfig      *schemas.MCPConfig `json:"mcp"`       // MCP configuration (optional)
}

// ReadMCPKeys reads environment variables from the environment and updates the MCP configurations.
// It replaces values starting with "env." in the connection_string field with actual values from the environment.
// Returns an error if any required environment variable is missing.
func (config *BifrostHTTPConfig) ReadMCPKeys() error {
	if config.MCPConfig == nil {
		return nil // No MCP config to process
	}

	// Helper function to check and replace env values
	replaceEnvValue := func(value string) (string, error) {
		if strings.HasPrefix(value, "env.") {
			envKey := strings.TrimPrefix(value, "env.")
			if envValue := os.Getenv(envKey); envValue != "" {
				return envValue, nil
			}
			return "", fmt.Errorf("environment variable %s not found in the environment", envKey)
		}
		return value, nil
	}

	// Process each client config
	for i, clientConfig := range config.MCPConfig.ClientConfigs {
		// Process ConnectionString if present
		if clientConfig.ConnectionString != nil {
			newValue, err := replaceEnvValue(*clientConfig.ConnectionString)
			if err != nil {
				return fmt.Errorf("MCP client %s: %w", clientConfig.Name, err)
			}
			config.MCPConfig.ClientConfigs[i].ConnectionString = &newValue
		}
	}

	return nil
}

// readConfig reads and parses the configuration file.
// It handles case conversion for provider names and sets up provider-specific metadata.
// Returns a BifrostHTTPConfig containing both provider and MCP configurations.
// Panics if the config file cannot be read or parsed.
//
// In the config file, use placeholder keys (e.g., env.OPENAI_API_KEY) instead of hardcoding actual values.
// These placeholders will be replaced with the corresponding values from the environment variables.
// Example:
//
//	"providers": {
//		"openAI": {
//			"keys":[{
//				 "value": "env.OPENAI_API_KEY"
//			     "models": ["gpt-4o-mini", "gpt-4-turbo"],
//			     "weight": 1.0
//			}]
//		}
//	},
//	"mcp": {
//		"client_configs": [...]
//	}
//
// In this example, OPENAI_API_KEY refers to a key in the environment variables. At runtime, its value will be used to replace the placeholder.
// Same setup applies to keys in meta configs of all the providers.
// Example:
//
//	"meta_config": {
//		"secret_access_key": "env.AWS_SECRET_ACCESS_KEY"
//		"region": "env.AWS_REGION"
//	}
//
// In this example, AWS_SECRET_ACCESS_KEY and AWS_REGION refer to keys in environment variables.
func ReadConfig(configLocation string) *BifrostHTTPConfig {
	data, err := os.ReadFile(configLocation)
	if err != nil {
		log.Fatalf("failed to read config JSON file: %v", err)
	}

	// First unmarshal into the new structure
	var fullConfig BifrostHTTPConfig
	if err := json.Unmarshal(data, &fullConfig); err != nil {
		log.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if fullConfig.ProviderConfig == nil {
		log.Fatalf("providers section is required in config")
	}

	// Process provider configurations - convert string keys to lowercase provider names and handle meta configs
	processedProviders := make(ConfigMap)

	// First unmarshal providers into a map with string keys to handle case conversion
	var rawProviders map[string]ProviderConfig
	if providersBytes, err := json.Marshal(fullConfig.ProviderConfig); err != nil {
		log.Fatalf("failed to marshal providers: %v", err)
	} else if err := json.Unmarshal(providersBytes, &rawProviders); err != nil {
		log.Fatalf("failed to unmarshal providers: %v", err)
	}

	// Create a temporary structure to unmarshal the full JSON with proper meta configs
	var tempConfig struct {
		Providers map[string]struct {
			MetaConfig json.RawMessage `json:"meta_config"`
		} `json:"providers"`
	}

	if err := json.Unmarshal(data, &tempConfig); err != nil {
		log.Fatalf("failed to unmarshal configuration file: %v\n\n Please check your configuration file for proper JSON formatting and meta_config structure", err)
	} else {
		for rawProvider, cfg := range rawProviders {
			provider := schemas.ModelProvider(strings.ToLower(rawProvider))

			// Get the raw meta config for this provider
			if tempProvider, exists := tempConfig.Providers[rawProvider]; exists && len(tempProvider.MetaConfig) > 0 {
				switch provider {
				case schemas.Azure:
					var azureMetaConfig meta.AzureMetaConfig
					if err := json.Unmarshal(tempProvider.MetaConfig, &azureMetaConfig); err != nil {
						log.Printf("warning: failed to unmarshal Azure meta config: %v", err)
					} else {
						var metaConfig schemas.MetaConfig = &azureMetaConfig
						cfg.MetaConfig = &metaConfig
					}
				case schemas.Bedrock:
					var bedrockMetaConfig meta.BedrockMetaConfig
					if err := json.Unmarshal(tempProvider.MetaConfig, &bedrockMetaConfig); err != nil {
						log.Printf("warning: failed to unmarshal Bedrock meta config: %v", err)
					} else {
						var metaConfig schemas.MetaConfig = &bedrockMetaConfig
						cfg.MetaConfig = &metaConfig
					}
				case schemas.Vertex:
					var vertexMetaConfig meta.VertexMetaConfig
					if err := json.Unmarshal(tempProvider.MetaConfig, &vertexMetaConfig); err != nil {
						log.Printf("warning: failed to unmarshal Vertex meta config: %v", err)
					} else {
						var metaConfig schemas.MetaConfig = &vertexMetaConfig
						cfg.MetaConfig = &metaConfig
					}
				}
			}

			processedProviders[provider] = cfg
		}

	}

	fullConfig.ProviderConfig = processedProviders
	return &fullConfig
}
