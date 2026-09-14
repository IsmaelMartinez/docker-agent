// Package contracts defines the interfaces implemented by model providers.
// It has no dependency on provider construction, allowing provider
// implementations and orchestration packages to share one canonical contract.
package contracts

import (
	"context"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/model/provider/base"
	"github.com/docker/docker-agent/pkg/modelsdev"
	"github.com/docker/docker-agent/pkg/rag/types"
	"github.com/docker/docker-agent/pkg/tools"
)

// Provider defines the common interface implemented by model providers.
type Provider interface {
	// ID returns a provider-qualified identity to preserve the model namespace
	// when it crosses API boundaries.
	ID() modelsdev.ID
	// CreateChatCompletionStream creates a streaming chat completion request.
	CreateChatCompletionStream(
		ctx context.Context,
		messages []chat.Message,
		tools []tools.Tool,
	) (chat.MessageStream, error)
	// BaseConfig returns the provider's base configuration.
	BaseConfig() base.Config
}

// EmbeddingProvider is a provider that supports embeddings.
type EmbeddingProvider interface {
	Provider
	CreateEmbedding(ctx context.Context, text string) (*base.EmbeddingResult, error)
}

// BatchEmbeddingProvider is an embedding provider that supports batches.
type BatchEmbeddingProvider interface {
	EmbeddingProvider
	// CreateBatchEmbedding returns embeddings in the same order as the inputs.
	CreateBatchEmbedding(ctx context.Context, texts []string) (*base.BatchEmbeddingResult, error)
}

// RerankingProvider is a provider that can score documents by relevance.
type RerankingProvider interface {
	Provider
	// Rerank returns one relevance score per document, in input order.
	Rerank(ctx context.Context, query string, documents []types.Document, criteria string) ([]float64, error)
}
