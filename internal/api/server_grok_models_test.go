package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func TestModelsDispatchByGrokShellUserAgent(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-grok-shell-model-list"
	modelRegistry.RegisterClient(clientID, "openai", []*registry.ModelInfo{
		{ID: "grok-shell-openai-model", DisplayName: "Grok Shell Model", ContextLength: 256000, Thinking: &registry.ThinkingSupport{Levels: []string{"high"}}},
	})
	modelRegistry.RegisterClient(clientID+"-claude", "claude", []*registry.ModelInfo{
		{ID: "grok-shell-claude-model", DisplayName: "Claude Catalog Model", ContextLength: 200000},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
		modelRegistry.UnregisterClient(clientID + "-claude")
	})

	server := newTestServer(t)
	for _, userAgent := range []string{
		"grok-shell/0.2.119 (macos; aarch64)",
		"grok-pager/0.2.119 grok-shell/0.2.119 (macos; aarch64)",
	} {
		t.Run(userAgent, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://proxy.example.test/v1/models?client_version", nil)
			req.Header.Set("Authorization", "Bearer test-key")
			req.Header.Set("User-Agent", userAgent)
			recorder := httptest.NewRecorder()
			server.engine.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Object string `json:"object"`
				Data   []struct {
					ID               string `json:"id"`
					Model            string `json:"model"`
					Name             string `json:"name"`
					ContextWindow    int    `json:"context_window"`
					APIBackend       string `json:"api_backend"`
					SupportedInAPI   bool   `json:"supported_in_api"`
					ReasoningEfforts []struct {
						Value string `json:"value"`
					} `json:"reasoning_efforts"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
			}
			if response.Object != "list" {
				t.Fatalf("object = %q, want list", response.Object)
			}
			var foundOpenAI, foundClaude bool
			for _, model := range response.Data {
				switch model.ID {
				case "grok-shell-openai-model":
					foundOpenAI = true
					if model.Model != model.ID || model.Name != "Grok Shell Model" || model.ContextWindow != 256000 {
						t.Fatalf("OpenAI model mapping = %#v", model)
					}
					if model.APIBackend != "responses" || !model.SupportedInAPI {
						t.Fatalf("OpenAI model routing fields = %#v", model)
					}
					if len(model.ReasoningEfforts) != 1 || model.ReasoningEfforts[0].Value != "high" {
						t.Fatalf("OpenAI reasoning efforts = %#v", model.ReasoningEfforts)
					}
				case "grok-shell-claude-model":
					foundClaude = true
					if model.Model != model.ID || model.Name != "Claude Catalog Model" || model.ContextWindow != 200000 {
						t.Fatalf("Claude model mapping = %#v", model)
					}
					if len(model.ReasoningEfforts) != 0 {
						t.Fatalf("Claude reasoning efforts = %#v, want none", model.ReasoningEfforts)
					}
				}
			}
			if !foundOpenAI {
				t.Fatalf("registered OpenAI Grok model missing: %s", recorder.Body.String())
			}
			if !foundClaude {
				t.Fatalf("registered Claude Grok model missing: %s", recorder.Body.String())
			}
		})
	}
}

func TestModelsDispatchKeepsOrdinaryOpenAIResponse(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-ordinary-model-list-after-grok"
	modelRegistry.RegisterClient(clientID, "openai", []*registry.ModelInfo{{ID: "ordinary-model"}})
	t.Cleanup(func() { modelRegistry.UnregisterClient(clientID) })

	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("User-Agent", "curl/8.7.1")
	recorder := httptest.NewRecorder()
	server.engine.ServeHTTP(recorder, req)

	var response struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Object != "list" {
		t.Fatalf("object = %q, want list", response.Object)
	}
	found := false
	for _, model := range response.Data {
		if _, exists := model["api_backend"]; exists {
			t.Fatalf("ordinary response contains Grok field: %#v", model)
		}
		if id, ok := model["id"].(string); ok && id == "ordinary-model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered ordinary model missing: %s", recorder.Body.String())
	}
}
