// Package providers implements various LLM providers and their utility functions.
// This file contains the Ollama provider implementation.
package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// OllamaResponse represents the response structure from the Ollama API.
type OllamaResponse struct {
	ID      string                          `json:"id"`
	Object  string                          `json:"object"`
	Choices []schemas.BifrostResponseChoice `json:"choices"`
	Model   string                          `json:"model"`
	Created int                             `json:"created"`
	Usage   schemas.LLMUsage                `json:"usage"`
}

// ollamaResponsePool provides a pool for Ollama response objects.
var ollamaResponsePool = sync.Pool{
	New: func() interface{} {
		return &OllamaResponse{}
	},
}

// acquireOllamaResponse gets a Ollama response from the pool and resets it.
func acquireOllamaResponse() *OllamaResponse {
	resp := ollamaResponsePool.Get().(*OllamaResponse)
	*resp = OllamaResponse{} // Reset the struct
	return resp
}

// releaseOllamaResponse returns a Ollama response to the pool.
func releaseOllamaResponse(resp *OllamaResponse) {
	if resp != nil {
		ollamaResponsePool.Put(resp)
	}
}

// OllamaProvider implements the Provider interface for Ollama's API.
type OllamaProvider struct {
	logger        schemas.Logger        // Logger for provider operations
	client        *fasthttp.Client      // HTTP client for API requests
	networkConfig schemas.NetworkConfig // Network configuration including extra headers
}

// NewOllamaProvider creates a new Ollama provider instance.
// It initializes the HTTP client with the provided configuration and sets up response pools.
// The client is configured with timeouts, concurrency limits, and optional proxy settings.
func NewOllamaProvider(config *schemas.ProviderConfig, logger schemas.Logger) (*OllamaProvider, error) {
	config.CheckAndSetDefaults()

	client := &fasthttp.Client{
		ReadTimeout:     time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds),
		WriteTimeout:    time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds),
		MaxConnsPerHost: config.ConcurrencyAndBufferSize.BufferSize,
	}

	// Pre-warm response pools
	for range config.ConcurrencyAndBufferSize.Concurrency {
		ollamaResponsePool.Put(&OllamaResponse{})
	}

	// Configure proxy if provided
	client = configureProxy(client, config.ProxyConfig, logger)

	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")

	// BaseURL is required for Ollama
	if config.NetworkConfig.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required for ollama provider")
	}

	return &OllamaProvider{
		logger:        logger,
		client:        client,
		networkConfig: config.NetworkConfig,
	}, nil
}

// GetProviderKey returns the provider identifier for Ollama.
func (provider *OllamaProvider) GetProviderKey() schemas.ModelProvider {
	return schemas.Ollama
}

// TextCompletion is not supported by the Ollama provider.
func (provider *OllamaProvider) TextCompletion(ctx context.Context, model, key, text string, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	return nil, newUnsupportedOperationError("text completion", "ollama")
}

// ChatCompletion performs a chat completion request to the Ollama API.
func (provider *OllamaProvider) ChatCompletion(ctx context.Context, model, key string, messages []schemas.BifrostMessage, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	formattedMessages, preparedParams := prepareOpenAIChatRequest(messages, params)

	requestBody := mergeConfig(map[string]interface{}{
		"model":    model,
		"messages": formattedMessages,
	}, preparedParams)

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: schemas.ErrorField{
				Message: schemas.ErrProviderJSONMarshaling,
				Error:   err,
			},
		}
	}

	// Create request
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	// Set any extra headers from network config
	setExtraHeaders(req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/chat/completions")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	req.SetBody(jsonBody)

	// Make request
	bifrostErr := makeRequestWithContext(ctx, provider.client, req, resp)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	// Handle error response
	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug(fmt.Sprintf("error from ollama provider: %s", string(resp.Body())))

		var errorResp map[string]interface{}
		bifrostErr := handleProviderAPIError(resp, &errorResp)
		bifrostErr.Error.Message = fmt.Sprintf("Ollama error: %v", errorResp)
		return nil, bifrostErr
	}

	responseBody := resp.Body()

	// Pre-allocate response structs from pools
	response := acquireOllamaResponse()
	defer releaseOllamaResponse(response)

	// Use enhanced response handler with pre-allocated response
	rawResponse, bifrostErr := handleProviderResponse(responseBody, response)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	// Create final response
	bifrostResponse := &schemas.BifrostResponse{
		ID:      response.ID,
		Object:  response.Object,
		Choices: response.Choices,
		Model:   response.Model,
		Created: response.Created,
		Usage:   response.Usage,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Provider:    schemas.Ollama,
			RawResponse: rawResponse,
		},
	}

	if params != nil {
		bifrostResponse.ExtraFields.Params = *params
	}

	return bifrostResponse, nil
}

// Embedding is not supported by the Ollama provider.
func (provider *OllamaProvider) Embedding(ctx context.Context, model string, key string, input *schemas.EmbeddingInput, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	return nil, newUnsupportedOperationError("embedding", "ollama")
}
