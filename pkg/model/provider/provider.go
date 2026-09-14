// Package provider defines the provider contracts and builds providers from
// explicit factory registries.
//
// The package deliberately does not import concrete SDK-backed providers.
// Applications that need Docker Agent's built-in provider set should import
// pkg/model/provider/providers and use its NewDefaultRegistry. Embedders can
// instead build a smaller registry with [NewRegistry]. [EmptyRegistry] is
// available for components that support running without model providers.
package provider

import (
	"context"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/model/provider/base"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	"github.com/docker/docker-agent/pkg/modelsdev"
	"github.com/docker/docker-agent/pkg/rag/types"
	"github.com/docker/docker-agent/pkg/tools"
)

// Provider defines the interface for model providers.
type Provider interface {
	// ID returns the provider-qualified model identity. Returning a
	// [modelsdev.ID] (rather than a bare string) prevents callers from
	// silently forgetting to namespace the model when it crosses an API
	// boundary; use [modelsdev.ID.String] when a textual representation
	// is required.
	ID() modelsdev.ID
	// CreateChatCompletionStream creates a streaming chat completion request.
	// It returns a stream that can be iterated over to get completion chunks.
	CreateChatCompletionStream(
		ctx context.Context,
		messages []chat.Message,
		tools []tools.Tool,
	) (chat.MessageStream, error)
	// BaseConfig returns the base configuration of this provider.
	BaseConfig() base.Config
}

// EmbeddingProvider defines the interface for providers that support embeddings.
type EmbeddingProvider interface {
	Provider
	// CreateEmbedding generates an embedding vector for the given text with usage tracking.
	CreateEmbedding(ctx context.Context, text string) (*base.EmbeddingResult, error)
}

// BatchEmbeddingProvider defines the interface for providers that support batch embeddings.
type BatchEmbeddingProvider interface {
	EmbeddingProvider
	// CreateBatchEmbedding generates embedding vectors for multiple texts with usage tracking.
	// Returns embeddings in the same order as input texts.
	CreateBatchEmbedding(ctx context.Context, texts []string) (*base.BatchEmbeddingResult, error)
}

// RerankingProvider defines the interface for providers that support reranking.
// Reranking models score query-document pairs to assess relevance.
type RerankingProvider interface {
	Provider
	// Rerank scores documents by relevance to the query.
	// Returns relevance scores in the same order as input documents.
	// Scores are typically in [0, 1] range where higher means more relevant.
	// criteria: Optional domain-specific guidance for relevance scoring (appended to base prompt)
	// documents: Array of types.Document with content and metadata
	Rerank(ctx context.Context, query string, documents []types.Document, criteria string) ([]float64, error)
}

// New creates a provider with an empty registry and therefore returns an
// unknown-provider error for every concrete provider.
//
// Deprecated: construct a Registry explicitly and call Registry.New.
func New(ctx context.Context, cfg *latest.ModelConfig, env environment.Provider, opts ...options.Opt) (Provider, error) {
	return EmptyRegistry().New(ctx, cfg, env, opts...)
}

// NewWithModels creates a provider with an empty registry and therefore
// returns an unknown-provider error for every concrete provider.
//
// Deprecated: construct a Registry explicitly and call Registry.NewWithModels.
func NewWithModels(ctx context.Context, cfg *latest.ModelConfig, models map[string]latest.ModelConfig, env environment.Provider, opts ...options.Opt) (Provider, error) {
	return EmptyRegistry().NewWithModels(ctx, cfg, models, env, opts...)
}
