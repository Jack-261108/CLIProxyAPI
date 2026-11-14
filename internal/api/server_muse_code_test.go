package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoeForge/moego/ojson"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func parseAPIMuseCodeEntriesByID(t *testing.T, raw []byte) map[string]ojson.Result {
	t.Helper()
	root := ojson.ParseBytes(raw)
	data := root.Get("data")
	if !data.IsArray() {
		t.Fatalf("expected data array in response, got %s", string(raw))
	}
	entries := make(map[string]ojson.Result)
	data.ForEach(func(_, item ojson.Result) bool {
		entries[item.Get("id").String()] = item
		return true
	})
	return entries
}

func TestMuseCodeClientEndpointsRelayToMetaOrigin(t *testing.T) {
	var seenMethod, seenPath, seenQuery, seenAuth, seenBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	server := newTestServer(t)
	server.handlers.AuthManager.RegisterExecutor(executor.NewMetaExecutor(server.getConfig()))
	cred := &auth.Auth{
		ID:       "meta-muse-code-relay",
		Provider: "meta",
		Status:   auth.StatusActive,
		Attributes: map[string]string{
			"api_key":  "meta-upstream-secret",
			"base_url": upstream.URL + "/v1",
		},
	}
	registry.GetGlobalRegistry().RegisterClient(cred.ID, "meta", []*registry.ModelInfo{{ID: "muse-wonder-full-1.0"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(cred.ID)
	})
	if _, errRegister := server.handlers.AuthManager.Register(t.Context(), cred); errRegister != nil {
		t.Fatalf("register Meta credential: %v", errRegister)
	}

	for _, tc := range []struct {
		name      string
		method    string
		target    string
		body      string
		wantPath  string
		wantQuery string
	}{
		{
			name:      "config",
			method:    http.MethodGet,
			target:    "/muse-code/config?channel=public",
			wantPath:  "/muse-code/config",
			wantQuery: "channel=public",
		},
		{
			name:     "search",
			method:   http.MethodPost,
			target:   "/muse-code/search",
			body:     `{"query":"golang"}`,
			wantPath: "/muse-code/search",
		},
		{
			name:     "browser_open",
			method:   http.MethodPost,
			target:   "/muse-code/browser_open",
			body:     `{"url":"https://example.com"}`,
			wantPath: "/muse-code/browser_open",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seenMethod, seenPath, seenQuery, seenAuth, seenBody = "", "", "", "", ""
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-key")
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
			}
			if seenMethod != tc.method || seenPath != tc.wantPath || seenQuery != tc.wantQuery {
				t.Fatalf("upstream got %s %s?%s, want %s %s?%s", seenMethod, seenPath, seenQuery, tc.method, tc.wantPath, tc.wantQuery)
			}
			if seenAuth != "Bearer meta-upstream-secret" {
				t.Fatalf("upstream Authorization = %q, want Bearer meta-upstream-secret", seenAuth)
			}
			if tc.body != "" && seenBody != tc.body {
				t.Fatalf("upstream body = %q, want %q", seenBody, tc.body)
			}
		})
	}
}

func TestMuseCodeModelsUsesFullCPACatalogWithoutUpstreamRelay(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	server := newTestServer(t)
	server.handlers.AuthManager.RegisterExecutor(executor.NewMetaExecutor(server.getConfig()))
	cred := &auth.Auth{
		ID:       "meta-muse-code-models-local",
		Provider: "meta",
		Status:   auth.StatusActive,
		Attributes: map[string]string{
			"api_key":  "meta-upstream-secret",
			"base_url": upstream.URL + "/v1",
		},
	}
	registry.GetGlobalRegistry().RegisterClient(cred.ID, "meta", []*registry.ModelInfo{
		{ID: "muse-wonder-1.0", DisplayName: "Muse Wonder 1.0", OwnedBy: "meta", Type: "meta", ContextLength: 1007997, MaxCompletionTokens: 128000, Thinking: &registry.ThinkingSupport{Levels: []string{"minimal", "low", "medium", "high", "xhigh", "max"}}, SupportedInputModalities: []string{"text", "image", "video"}, SupportedOutputModalities: []string{"text"}},
		{ID: "muse-spark-1.3", DisplayName: "Muse Spark 1.3", OwnedBy: "meta", Type: "meta", ContextLength: 1007997, MaxCompletionTokens: 128000, Thinking: &registry.ThinkingSupport{Levels: []string{"minimal", "low", "medium", "high", "xhigh", "max"}}, SupportedInputModalities: []string{"text", "image", "video"}, SupportedOutputModalities: []string{"text"}},
		{ID: "muse-image-1.0", DisplayName: "Muse Image 1.0", OwnedBy: "meta", Type: "meta", SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"image"}},
	})
	registry.GetGlobalRegistry().RegisterClient("codex-muse-code-models-local", "codex", []*registry.ModelInfo{
		{ID: "gpt-6.1-sol", DisplayName: "GPT 6.1 Sol", OwnedBy: "openai", Type: "codex", ContextLength: 500000, MaxCompletionTokens: 128000, Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high", "xhigh"}}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text"}},
	})
	registry.GetGlobalRegistry().RegisterClient("claude-muse-code-models-local", "claude", []*registry.ModelInfo{
		{ID: "claude-sonnet-4-5-20250929", DisplayName: "Claude 4.5 Sonnet", OwnedBy: "anthropic", Type: "claude", ContextLength: 200000, MaxCompletionTokens: 64000, Thinking: &registry.ThinkingSupport{Min: 1024, Max: 128000, ZeroAllowed: true}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text"}},
	})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(cred.ID)
		registry.GetGlobalRegistry().UnregisterClient("codex-muse-code-models-local")
		registry.GetGlobalRegistry().UnregisterClient("claude-muse-code-models-local")
	})
	if _, errRegister := server.handlers.AuthManager.Register(t.Context(), cred); errRegister != nil {
		t.Fatalf("register Meta credential: %v", errRegister)
	}

	req := httptest.NewRequest(http.MethodGet, "/muse-code/models?channel=public", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	server.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("/muse-code/models must not call Meta upstream; calls=%d", upstreamCalls.Load())
	}
	if ojson.GetBytesNoCopy(rec.Body.Bytes(), "object").String() != "list" {
		t.Fatalf("object = %q, want list", ojson.GetBytesNoCopy(rec.Body.Bytes(), "object").String())
	}

	entries := parseAPIMuseCodeEntriesByID(t, rec.Body.Bytes())
	for _, wantID := range []string{"muse-wonder-1.0", "muse-spark-1.3", "muse-image-1.0", "gpt-6.1-sol", "claude-sonnet-4-5-20250929"} {
		if _, ok := entries[wantID]; !ok {
			t.Fatalf("missing expected model %q in /muse-code/models response: %s", wantID, rec.Body.String())
		}
	}
	if got := entries["muse-wonder-1.0"].Get("metadata.muse-code.background_agent_model").String(); got != "muse-spark-1.3" {
		t.Fatalf("muse-wonder-1.0 background_agent_model = %q, want muse-spark-1.3", got)
	}
	museImg := entries["muse-image-1.0"].Get("metadata.muse-code")
	if !museImg.Get("is_hidden").Bool() || museImg.Get("tool_call").Bool() {
		t.Fatalf("muse-image-1.0 expected hidden with tool_call=false: %s", museImg.Raw)
	}
	if len(entries["claude-sonnet-4-5-20250929"].Get("metadata.muse-code.variants").Map()) == 0 {
		t.Fatalf("claude-sonnet-4-5-20250929 should advertise derived reasoning variants")
	}
}

func TestMuseCodeRoutesEnforceAuthSafeModeAndDisallowedPaths(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// 1. Unknown client key returns 401; disallowed /muse-code/key returns 404.
	server := newTestServer(t)
	server.handlers.AuthManager.RegisterExecutor(executor.NewMetaExecutor(server.getConfig()))
	cred := &auth.Auth{
		ID:       "meta-server-auth-test",
		Provider: "meta",
		Status:   auth.StatusActive,
		Attributes: map[string]string{
			"api_key":  "meta-secret",
			"base_url": upstream.URL + "/v1",
		},
	}
	registry.GetGlobalRegistry().RegisterClient(cred.ID, "meta", []*registry.ModelInfo{{ID: "muse-wonder-1.0"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(cred.ID)
	})
	if _, errRegister := server.handlers.AuthManager.Register(t.Context(), cred); errRegister != nil {
		t.Fatalf("register Meta credential: %v", errRegister)
	}

	unauthReq := httptest.NewRequest(http.MethodGet, "/muse-code/models", nil)
	unauthReq.Header.Set("Authorization", "Bearer wrong-key")
	unauthRec := httptest.NewRecorder()
	server.engine.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown client key status = %d, want 401", unauthRec.Code)
	}

	keyReq := httptest.NewRequest(http.MethodPost, "/muse-code/key", nil)
	keyReq.Header.Set("Authorization", "Bearer test-key")
	keyRec := httptest.NewRecorder()
	server.engine.ServeHTTP(keyRec, keyReq)
	if keyRec.Code != http.StatusNotFound {
		t.Fatalf("disallowed /muse-code/key status = %d, want 404", keyRec.Code)
	}

	// 2. Example API key safe mode blocks /muse-code/models with 403.
	safeCfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"your-api-key-1"},
		},
	}
	safeServer := newTestServerWithConfig(t, safeCfg, WithExampleAPIKeySafeMode())
	safeReq := httptest.NewRequest(http.MethodGet, "/muse-code/models", nil)
	safeReq.Header.Set("Authorization", "Bearer your-api-key-1")
	safeRec := httptest.NewRecorder()
	safeServer.engine.ServeHTTP(safeRec, safeReq)
	if safeRec.Code != http.StatusForbidden {
		t.Fatalf("safe mode status = %d, want 403; body=%s", safeRec.Code, safeRec.Body.String())
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("upstream called %d times, want 0", upstreamCalls.Load())
	}
}
