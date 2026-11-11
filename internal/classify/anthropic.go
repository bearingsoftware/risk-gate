package classify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

const Model = "claude-sonnet-5-5"

const maxTokens = 1024

// Response is the part of an API reply the classifier reads.
type Response struct {
	Model      string
	StopReason string
	Text       string
	Usage      Usage
}

// APIError is a non-success reply from the API after the SDK's own retries.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("anthropic api: status %d", e.Status) }

// API is one structured-output call; tests supply the real implementation pointed at an httptest server.
type API interface {
	Call(ctx context.Context, system, user string, schema []byte) (Response, error)
}

type Anthropic struct{ client anthropic.Client }

func NewAnthropic(key string, opts ...option.RequestOption) *Anthropic {
	return &Anthropic{client: anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(key)}, opts...)...)}
}

func (a *Anthropic) Call(ctx context.Context, system, user string, schema []byte) (Response, error) {
	msg, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     Model,
		MaxTokens: maxTokens,
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		System: []anthropic.BetaTextBlockParam{{
			Text:         system,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: json.RawMessage(schema)},
		},
		Messages: []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user))},
	})
	if err != nil {
		var ae *anthropic.Error
		if errors.As(err, &ae) {
			return Response{}, &APIError{Status: ae.StatusCode, Body: ae.RawJSON()}
		}
		return Response{}, fmt.Errorf("anthropic call: %w", err)
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if b.Type == "text" {
			text.WriteString(b.AsText().Text)
		}
	}
	return Response{
		Model: msg.Model, StopReason: string(msg.StopReason), Text: text.String(),
		Usage: Usage{
			InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens,
			CacheReadInputTokens: msg.Usage.CacheReadInputTokens, CacheCreationInputTokens: msg.Usage.CacheCreationInputTokens,
		},
	}, nil
}
