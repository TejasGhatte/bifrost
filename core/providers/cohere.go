// Package providers implements various LLM providers and their utility functions.
// This file contains the Cohere provider implementation.
package providers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// cohereResponsePool provides a pool for Cohere response objects.
var cohereResponsePool = sync.Pool{
	New: func() interface{} {
		return &CohereChatResponse{}
	},
}

// acquireCohereResponse gets a Cohere response from the pool and resets it.
func acquireCohereResponse() *CohereChatResponse {
	resp := cohereResponsePool.Get().(*CohereChatResponse)
	*resp = CohereChatResponse{} // Reset the struct
	return resp
}

// releaseCohereResponse returns a Cohere response to the pool.
func releaseCohereResponse(resp *CohereChatResponse) {
	if resp != nil {
		cohereResponsePool.Put(resp)
	}
}

// CohereParameterDefinition represents a parameter definition for a Cohere tool.
// It defines the type, description, and whether the parameter is required.
type CohereParameterDefinition struct {
	Type        string  `json:"type"`                  // Type of the parameter
	Description *string `json:"description,omitempty"` // Optional description of the parameter
	Required    bool    `json:"required"`              // Whether the parameter is required
}

// CohereTool represents a tool definition for the Cohere API.
// It includes the tool's name, description, and parameter definitions.
type CohereTool struct {
	Name                 string                               `json:"name"`                  // Name of the tool
	Description          string                               `json:"description"`           // Description of the tool
	ParameterDefinitions map[string]CohereParameterDefinition `json:"parameter_definitions"` // Definitions of the tool's parameters
}

// CohereToolCall represents a tool call made by the Cohere API.
// It includes the name of the tool and its parameters.
type CohereToolCall struct {
	Name       string      `json:"name"`       // Name of the tool being called
	Parameters interface{} `json:"parameters"` // Parameters passed to the tool
}

// CohereChatResponse represents the response from Cohere's chat API.
// It includes the response ID, generated text, chat history, and usage statistics.
type CohereChatResponse struct {
	ResponseID   string `json:"response_id"`   // Unique identifier for the response
	Text         string `json:"text"`          // Generated text response
	GenerationID string `json:"generation_id"` // ID of the generation
	ChatHistory  []struct {
		Role      schemas.ModelChatMessageRole `json:"role"`       // Role of the message sender
		Message   string                       `json:"message"`    // Content of the message
		ToolCalls []CohereToolCall             `json:"tool_calls"` // Tool calls made in the message
	} `json:"chat_history"` // History of the chat conversation
	FinishReason string `json:"finish_reason"` // Reason for completion termination
	Meta         struct {
		APIVersion struct {
			Version string `json:"version"` // Version of the API used
		} `json:"api_version"` // API version information
		BilledUnits struct {
			InputTokens  float64 `json:"input_tokens"`  // Number of input tokens billed
			OutputTokens float64 `json:"output_tokens"` // Number of output tokens billed
		} `json:"billed_units"` // Token usage billing information
		Tokens struct {
			InputTokens  float64 `json:"input_tokens"`  // Number of input tokens used
			OutputTokens float64 `json:"output_tokens"` // Number of output tokens generated
		} `json:"tokens"` // Token usage statistics
	} `json:"meta"` // Metadata about the response
	ToolCalls []CohereToolCall `json:"tool_calls"` // Tool calls made in the response
}

// CohereError represents an error response from the Cohere API.
type CohereError struct {
	Message string `json:"message"` // Error message
}

// CohereEmbeddingResponse represents the response from Cohere's embedding API.
type CohereEmbeddingResponse struct {
	ID         string `json:"id"` // Unique identifier for the embedding request
	Embeddings struct {
		Float [][]float32 `json:"float"` // Array of float embeddings, one for each input text
	} `json:"embeddings"` // Embeddings in the response
}

// CohereProvider implements the Provider interface for Cohere.
type CohereProvider struct {
	logger        schemas.Logger        // Logger for provider operations
	client        *fasthttp.Client      // HTTP client for API requests
	networkConfig schemas.NetworkConfig // Network configuration including extra headers
}

// NewCohereProvider creates a new Cohere provider instance.
// It initializes the HTTP client with the provided configuration and sets up response pools.
// The client is configured with timeouts and connection limits.
func NewCohereProvider(config *schemas.ProviderConfig, logger schemas.Logger) *CohereProvider {
	config.CheckAndSetDefaults()

	client := &fasthttp.Client{
		ReadTimeout:     time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds),
		WriteTimeout:    time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds),
		MaxConnsPerHost: config.ConcurrencyAndBufferSize.Concurrency,
	}

	// Pre-warm response pools
	for range config.ConcurrencyAndBufferSize.Concurrency {
		cohereResponsePool.Put(&CohereChatResponse{})

	}

	// Set default BaseURL if not provided
	if config.NetworkConfig.BaseURL == "" {
		config.NetworkConfig.BaseURL = "https://api.cohere.ai"
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")

	return &CohereProvider{
		logger:        logger,
		client:        client,
		networkConfig: config.NetworkConfig,
	}
}

// GetProviderKey returns the provider identifier for Cohere.
func (provider *CohereProvider) GetProviderKey() schemas.ModelProvider {
	return schemas.Cohere
}

// TextCompletion is not supported by the Cohere provider.
// Returns an error indicating that text completion is not supported.
func (provider *CohereProvider) TextCompletion(ctx context.Context, model, key, text string, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	return nil, newUnsupportedOperationError("text completion", "cohere")
}

// ChatCompletion performs a chat completion request to the Cohere API.
// It formats the request, sends it to Cohere, and processes the response.
// Returns a BifrostResponse containing the completion results or an error if the request fails.
func (provider *CohereProvider) ChatCompletion(ctx context.Context, model, key string, messages []schemas.BifrostMessage, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	// Get the last message and chat history
	lastMessage := messages[len(messages)-1]
	chatHistory := messages[:len(messages)-1]

	// Transform chat history
	var cohereHistory []map[string]interface{}
	for _, msg := range chatHistory {
		historyMsg := map[string]interface{}{
			"role": msg.Role,
		}

		if msg.Role == schemas.ModelChatMessageRoleAssistant {
			if msg.AssistantMessage != nil && msg.AssistantMessage.ToolCalls != nil {
				var toolCalls []map[string]interface{}
				for _, toolCall := range *msg.AssistantMessage.ToolCalls {
					var arguments map[string]interface{}
					var parsedJSON interface{}
					err := json.Unmarshal([]byte(toolCall.Function.Arguments), &parsedJSON)
					if err == nil {
						if arr, ok := parsedJSON.(map[string]interface{}); ok {
							arguments = arr
						} else {
							arguments = map[string]interface{}{"content": parsedJSON}
						}
					} else {
						arguments = map[string]interface{}{"content": toolCall.Function.Arguments}
					}

					toolCalls = append(toolCalls, map[string]interface{}{
						"name":       toolCall.Function.Name,
						"parameters": arguments,
					})
				}
				historyMsg["tool_calls"] = toolCalls
			}
		} else if msg.Role == schemas.ModelChatMessageRoleTool {
			// Find the original tool call parameters from conversation history
			var toolCallParameters map[string]interface{}

			// Look back through the chat history to find the assistant message with the matching tool call
			for i := len(chatHistory) - 1; i >= 0; i-- {
				prevMsg := chatHistory[i]
				if prevMsg.Role == schemas.ModelChatMessageRoleAssistant &&
					prevMsg.AssistantMessage != nil &&
					prevMsg.AssistantMessage.ToolCalls != nil {

					// Search through tool calls in this assistant message
					for _, toolCall := range *prevMsg.AssistantMessage.ToolCalls {
						if toolCall.ID != nil && msg.ToolMessage != nil && msg.ToolMessage.ToolCallID != nil &&
							*toolCall.ID == *msg.ToolMessage.ToolCallID {

							// Found the matching tool call, extract its parameters
							var parsedJSON interface{}
							err := json.Unmarshal([]byte(toolCall.Function.Arguments), &parsedJSON)
							if err == nil {
								if arr, ok := parsedJSON.(map[string]interface{}); ok {
									toolCallParameters = arr
								} else {
									toolCallParameters = map[string]interface{}{"content": parsedJSON}
								}
							} else {
								toolCallParameters = map[string]interface{}{"content": toolCall.Function.Arguments}
							}
							break
						}
					}

					// If we found the parameters, stop searching
					if toolCallParameters != nil {
						break
					}
				}
			}

			// If no parameters found, use empty map as fallback
			if toolCallParameters == nil {
				toolCallParameters = map[string]interface{}{}
			}

			toolResults := []map[string]interface{}{
				{
					"call": map[string]interface{}{
						"name":       *msg.ToolMessage.ToolCallID,
						"parameters": toolCallParameters,
					},
					"outputs": *msg.Content.ContentStr,
				},
			}

			historyMsg["tool_results"] = toolResults
		}

		if msg.Content.ContentStr != nil {
			historyMsg["message"] = *msg.Content.ContentStr
		} else if msg.Content.ContentBlocks != nil {
			// Create content array with text and image
			contentArray := []map[string]interface{}{}

			// Iterate over ContentBlocks to build the content array
			for _, block := range *msg.Content.ContentBlocks {
				if block.Text != nil {
					contentArray = append(contentArray, map[string]interface{}{
						"type": "text",
						"text": *block.Text,
					})
				}
				// Add image content using our helper function
				// NOTE: Cohere v1 does not support image content
				// if processedImageContent := processImageContent(block.ImageContent); processedImageContent != nil {
				// 	contentArray = append(contentArray, processedImageContent)
				// }
			}

			historyMsg["content"] = contentArray
		}

		cohereHistory = append(cohereHistory, historyMsg)
	}

	preparedParams := prepareParams(params)

	// Prepare request body
	requestBody := mergeConfig(map[string]interface{}{
		"chat_history": cohereHistory,
		"model":        model,
	}, preparedParams)

	// Handle the last message content based on whether it supports vision
	if lastMessage.Content.ContentStr != nil {
		requestBody["message"] = *lastMessage.Content.ContentStr
	} else if lastMessage.Content.ContentBlocks != nil {
		message := ""
		for _, block := range *lastMessage.Content.ContentBlocks {
			if block.Text != nil {
				message += *block.Text + "\n"
			}
		}
		requestBody["message"] = strings.TrimSuffix(message, "\n")
	}

	// Add tools if present
	if params != nil && params.Tools != nil && len(*params.Tools) > 0 {
		var tools []CohereTool
		for _, tool := range *params.Tools {
			parameterDefinitions := make(map[string]CohereParameterDefinition)
			params := tool.Function.Parameters
			for name, prop := range tool.Function.Parameters.Properties {
				propMap, ok := prop.(map[string]interface{})
				if ok {
					paramDef := CohereParameterDefinition{
						Required: slices.Contains(params.Required, name),
					}

					if typeStr, ok := propMap["type"].(string); ok {
						paramDef.Type = typeStr
					}

					if desc, ok := propMap["description"].(string); ok {
						paramDef.Description = &desc
					}

					parameterDefinitions[name] = paramDef
				}
			}

			tools = append(tools, CohereTool{
				Name:                 tool.Function.Name,
				Description:          tool.Function.Description,
				ParameterDefinitions: parameterDefinitions,
			})
		}
		requestBody["tools"] = tools
	}
	// Add tool choice if present
	if params != nil && params.ToolChoice != nil {
		if params.ToolChoice.ToolChoiceStr != nil {
			requestBody["tool_choice"] = *params.ToolChoice.ToolChoiceStr
		} else if params.ToolChoice.ToolChoiceStruct != nil {
			requestBody["tool_choice"] = map[string]interface{}{
				"type": strings.ToUpper(string(params.ToolChoice.ToolChoiceStruct.Type)),
			}
		}
	}

	// Marshal request body
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

	req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/chat")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	req.SetBody(jsonBody)

	// Make request
	bifrostErr := makeRequestWithContext(ctx, provider.client, req, resp)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	// Handle error response
	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug(fmt.Sprintf("error from cohere provider: %s", string(resp.Body())))

		var errorResp CohereError

		bifrostErr := handleProviderAPIError(resp, &errorResp)
		bifrostErr.Error.Message = errorResp.Message

		return nil, bifrostErr
	}

	// Read response body
	responseBody := resp.Body()

	// Create response object from pool
	response := acquireCohereResponse()
	defer releaseCohereResponse(response)

	rawResponse, bifrostErr := handleProviderResponse(responseBody, response)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	// Transform tool calls if present
	var toolCalls []schemas.ToolCall
	if response.ToolCalls != nil {
		for _, tool := range response.ToolCalls {
			function := schemas.FunctionCall{
				Name: &tool.Name,
			}

			args, err := json.Marshal(tool.Parameters)
			if err != nil {
				function.Arguments = fmt.Sprintf("%v", tool.Parameters)
			} else {
				function.Arguments = string(args)
			}

			toolCalls = append(toolCalls, schemas.ToolCall{
				Function: function,
			})
		}
	}

	// Get role and content from the last message in chat history
	var role schemas.ModelChatMessageRole
	var content string
	if len(response.ChatHistory) > 0 {
		lastMsg := response.ChatHistory[len(response.ChatHistory)-1]
		role = lastMsg.Role
		content = lastMsg.Message
	} else {
		role = schemas.ModelChatMessageRoleChatbot
		content = response.Text
	}

	// Create final response
	bifrostResponse := &schemas.BifrostResponse{
		ID: response.ResponseID,
		Choices: []schemas.BifrostResponseChoice{
			{
				Index: 0,
				Message: schemas.BifrostMessage{
					Role: role,
					Content: schemas.MessageContent{
						ContentStr: &content,
					},
					AssistantMessage: &schemas.AssistantMessage{
						ToolCalls: &toolCalls,
					},
				},
				FinishReason: &response.FinishReason,
			},
		},
		Usage: schemas.LLMUsage{
			PromptTokens:     int(response.Meta.Tokens.InputTokens),
			CompletionTokens: int(response.Meta.Tokens.OutputTokens),
			TotalTokens:      int(response.Meta.Tokens.InputTokens + response.Meta.Tokens.OutputTokens),
		},
		Model: model,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Provider: schemas.Cohere,
			BilledUsage: &schemas.BilledLLMUsage{
				PromptTokens:     float64Ptr(response.Meta.BilledUnits.InputTokens),
				CompletionTokens: float64Ptr(response.Meta.BilledUnits.OutputTokens),
			},
			ChatHistory: convertChatHistory(response.ChatHistory),
			RawResponse: rawResponse,
		},
	}

	if params != nil {
		bifrostResponse.ExtraFields.Params = *params
	}

	return bifrostResponse, nil
}

// processImageContent processes image content for Cohere API format.
// It creates a copy of the image content, normalizes and formats it, then returns the properly formatted map.
// This prevents unintended mutations to the original image content.
func processImageContent(imageContent *schemas.ImageURLStruct) map[string]interface{} {
	if imageContent == nil {
		return nil
	}

	sanitizedURL, _ := SanitizeImageURL(imageContent.URL)
	urlTypeInfo := ExtractURLTypeInfo(sanitizedURL)

	formattedImgContent := AnthropicImageContent{
		Type: urlTypeInfo.Type,
		URL:  sanitizedURL,
	}

	if urlTypeInfo.MediaType != nil {
		formattedImgContent.MediaType = *urlTypeInfo.MediaType
	}

	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url": formattedImgContent.URL,
		},
	}
}

// convertChatHistory converts Cohere's chat history format to Bifrost's format for standardization.
// It transforms the chat history messages and their tool calls.
func convertChatHistory(history []struct {
	Role      schemas.ModelChatMessageRole `json:"role"`
	Message   string                       `json:"message"`
	ToolCalls []CohereToolCall             `json:"tool_calls"`
}) *[]schemas.BifrostMessage {
	converted := make([]schemas.BifrostMessage, len(history))
	for i, msg := range history {
		var toolCalls []schemas.ToolCall
		if msg.ToolCalls != nil {
			for _, tool := range msg.ToolCalls {
				function := schemas.FunctionCall{
					Name: &tool.Name,
				}

				args, err := json.Marshal(tool.Parameters)
				if err != nil {
					function.Arguments = fmt.Sprintf("%v", tool.Parameters)
				} else {
					function.Arguments = string(args)
				}

				toolCalls = append(toolCalls, schemas.ToolCall{
					Function: function,
				})
			}
		}

		converted[i] = schemas.BifrostMessage{
			Role: msg.Role,
			Content: schemas.MessageContent{
				ContentStr: &msg.Message,
			},
			AssistantMessage: &schemas.AssistantMessage{
				ToolCalls: &toolCalls,
			},
		}
	}
	return &converted
}

// Embedding generates embeddings for the given input text(s) using the Cohere API.
// Supports Cohere's embedding models and returns a BifrostResponse containing the embedding(s).
func (provider *CohereProvider) Embedding(ctx context.Context, model string, key string, input *schemas.EmbeddingInput, params *schemas.ModelParameters) (*schemas.BifrostResponse, *schemas.BifrostError) {
	if len(input.Texts) == 0 {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error:          schemas.ErrorField{Message: "no input text provided for embedding"},
		}
	}

	// Prepare request body with default values
	requestBody := map[string]interface{}{
		"texts":            input.Texts,
		"model":            model,
		"input_type":       "search_document", // Default input type - can be overridden via ExtraParams
		"embedding_types":  []string{"float"}, // Default to float embeddings
	}

	// Apply additional parameters if provided
	if params != nil {
		// Validate encoding format - Cohere API supports float, int8, uint8, binary, ubinary, but our provider only implements float
		if params.EncodingFormat != nil {
			if *params.EncodingFormat != "float" {
				return nil, &schemas.BifrostError{
					IsBifrostError: false,
					Error: schemas.ErrorField{
						Message: fmt.Sprintf("Cohere provider currently only supports 'float' encoding format, received: %s", *params.EncodingFormat),
					},
				}
			}
			// Override default with the specified format
			requestBody["embedding_types"] = []string{*params.EncodingFormat}
		}

		// Merge extra parameters - this allows overriding input_type and other parameters
		if params.ExtraParams != nil {
			for k, v := range params.ExtraParams {
				requestBody[k] = v
			}
		}
	}

	// Marshal request body
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

	req.SetRequestURI(provider.networkConfig.BaseURL + "/v2/embed")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	req.SetBody(jsonBody)

	// Make request
	bifrostErr := makeRequestWithContext(ctx, provider.client, req, resp)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	// Handle error response
	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug(fmt.Sprintf("error from cohere embedding provider: %s", string(resp.Body())))

		var errorResp CohereError
		bifrostErr := handleProviderAPIError(resp, &errorResp)
		bifrostErr.Error.Message = errorResp.Message

		return nil, bifrostErr
	}

	// Parse response
	var cohereResp CohereEmbeddingResponse
	if err := json.Unmarshal(resp.Body(), &cohereResp); err != nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: schemas.ErrorField{
				Message: "error parsing Cohere embedding response",
				Error:   err,
			},
		}
	}

	// Parse raw response for consistent format
	var rawResponse interface{}
	if err := json.Unmarshal(resp.Body(), &rawResponse); err != nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: schemas.ErrorField{
				Message: "error parsing raw response for Cohere embedding",
				Error:   err,
			},
		}
	}

	// Calculate token usage approximation (since Cohere doesn't provide this for embeddings)
	totalInputTokens := approximateTokenCount(input.Texts)

	// Create BifrostResponse
	bifrostResponse := &schemas.BifrostResponse{
		ID:        cohereResp.ID,
		Embedding: cohereResp.Embeddings.Float,
		Model:     model,
		Usage: schemas.LLMUsage{
			PromptTokens: totalInputTokens,
			TotalTokens:  totalInputTokens,
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			Provider:    schemas.Cohere,
			RawResponse: rawResponse,
		},
	}

	if params != nil {
		bifrostResponse.ExtraFields.Params = *params
	}

	return bifrostResponse, nil
}
