package base

import (
	"cmp"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/httpclient"
)

// OpenCodeSessionHeader carries the stable per-conversation ID OpenCode asks
// clients to send; it keys their routing and prompt caching.
const OpenCodeSessionHeader = "x-opencode-session"

const opencodeHost = "opencode.ai"

// opencodeSessionNamespace salts the hash so the header cannot be mapped back
// to the local session ID or the gateway's X-Cagent-Session-Id.
var opencodeSessionNamespace = uuid.MustParse("6f0c2a1e-8d4b-4c7f-9a3e-2b5d7e9f1c03")

// isOpenCodeProvider reports whether requests target OpenCode, either through
// the built-in aliases or a custom provider pointed at opencode.ai. A custom
// provider reaches the OpenAI, Anthropic or Google client with its alias
// already replaced by the client type, so on that path the base URL is the
// only signal.
func isOpenCodeProvider(cfg *latest.ModelConfig) bool {
	if cfg == nil {
		return false
	}
	switch cfg.Provider {
	case "opencode-go", "opencode-zen":
		return true
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == opencodeHost || strings.HasSuffix(host, "."+opencodeHost)
}

// WrapOpenCodeSession installs the session-header transport on client when
// cfg targets OpenCode. Provider clients call it on the direct path after
// applying the options transport wrapper, so the header is already set on
// every request a registered wrapper sees, as it was when the header was an
// SDK middleware. No-op for other providers and for a nil client (the Gemini
// Vertex AI backend hands the SDK an ADC-managed client instead). Gateway
// traffic is left alone: it already carries X-Cagent-Session-Id.
func WrapOpenCodeSession(cfg *latest.ModelConfig, client *http.Client) {
	if client == nil || !isOpenCodeProvider(cfg) {
		return
	}
	client.Transport = &opencodeSessionTransport{
		// A nil Transport means http.DefaultTransport, as in net/http.
		base:     cmp.Or(client.Transport, http.DefaultTransport),
		fallback: uuid.NewString(),
	}
}

// opencodeSessionID derives the header from the agent session, so multiplexed
// deployments get one ID per conversation rather than per client, and a
// resumed session keeps its ID across processes.
func opencodeSessionID(sessionID string) string {
	return uuid.NewSHA1(opencodeSessionNamespace, []byte(sessionID)).String()
}

// opencodeSessionTransport sets the header per request, leaving a value the
// user pinned through the SDK alone. The session comes off the request
// context, so this cannot be a static header fixed at construction time;
// requests with no session on the context share a per-client ID.
type opencodeSessionTransport struct {
	base     http.RoundTripper
	fallback string
}

func (t *opencodeSessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(OpenCodeSessionHeader) != "" {
		return t.base.RoundTrip(req)
	}
	id := t.fallback
	if sid := httpclient.SessionIDFromContext(req.Context()); sid != "" {
		id = opencodeSessionID(sid)
	}
	// A RoundTripper must not modify the caller's request.
	req = req.Clone(req.Context())
	req.Header.Set(OpenCodeSessionHeader, id)
	return t.base.RoundTrip(req)
}
