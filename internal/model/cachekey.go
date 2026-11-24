package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type promptCacheKeyCtx struct{}

// WithPromptCacheKey makes OpenAI-compatible requests under ctx carry
// prompt_cache_key "fe:<session id>:<toolset kind>". It is a routing hint
// that keeps one session's requests on the same cache, not prefix content,
// so the session id may appear in it.
func WithPromptCacheKey(ctx context.Context, sessionID int64, toolsetKind string) context.Context {
	return context.WithValue(ctx, promptCacheKeyCtx{}, fmt.Sprintf("fe:%d:%s", sessionID, toolsetKind))
}

func promptCacheKey(ctx context.Context) string {
	k, _ := ctx.Value(promptCacheKeyCtx{}).(string)
	return k
}

// withPromptCacheKey splices the key in as the first member of the JSON body,
// leaving every other byte as the client wrote it.
func withPromptCacheKey(req *http.Request) (*http.Request, error) {
	key := promptCacheKey(req.Context())
	if key == "" || req.Body == nil || req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		return req, nil
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) > 1 && trimmed[0] == '{' && !bytes.Contains(trimmed, []byte(`"prompt_cache_key"`)) {
		member, _ := json.Marshal(key)
		sep := []byte(",")
		if rest := bytes.TrimLeft(trimmed[1:], " \t\r\n"); len(rest) > 0 && rest[0] == '}' {
			sep = nil
		}
		body = bytes.Join([][]byte{[]byte(`{"prompt_cache_key":`), member, sep, trimmed[1:]}, nil)
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return out, nil
}

type longCacheTTLCtx struct{}

// WithLongCacheTTL makes Claude models created under ctx cache the tools,
// system prompt and shared context for one hour. Use it for human-paced runs
// whose gaps exceed the default five minutes.
func WithLongCacheTTL(ctx context.Context) context.Context {
	return context.WithValue(ctx, longCacheTTLCtx{}, true)
}

func longCacheTTL(ctx context.Context) bool {
	v, _ := ctx.Value(longCacheTTLCtx{}).(bool)
	return v
}
