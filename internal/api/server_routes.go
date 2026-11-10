package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	managementHandlers "github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api/middleware"
	codexlive "github.com/router-for-me/CLIProxyAPI/v8/internal/client/codex/live"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/client/grokbuild"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clienterror"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/gemini"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	log "github.com/sirupsen/logrus"
)

const oauthCallbackSuccessHTML = `<html><head><meta charset="utf-8"><title>Authentication successful</title><script>setTimeout(function(){window.close();},5000);</script></head><body><h1>Authentication successful!</h1><p>You can close this window.</p><p>This window will close automatically in 5 seconds.</p></body></html>`

const codexAlphaSearchSourceFormat = "codex-alpha-search"

// setupRoutes configures the API routes for the server.
// It defines the endpoints and associates them with their respective handlers.
func (s *Server) setupRoutes() {
	healthzHandler := func(c *gin.Context) {
		if c.Request.Method == http.MethodHead {
			c.Status(http.StatusOK)
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	s.engine.GET("/healthz", healthzHandler)
	s.engine.HEAD("/healthz", healthzHandler)

	s.engine.GET("/management.html", s.serveManagementControlPanel)
	openaiHandlers := openai.NewOpenAIAPIHandler(s.handlers)
	geminiHandlers := gemini.NewGeminiAPIHandler(s.handlers)
	claudeCodeHandlers := claude.NewClaudeCodeAPIHandler(s.handlers)
	openaiResponsesHandlers := openai.NewOpenAIResponsesAPIHandler(s.handlers)
	openaiResponsesHandlers.AccessManager = s.accessManager
	s.codexLiveHandler = codexlive.NewHandler(s.handlers.AuthManager, s.cfg)

	// OpenAI compatible API routes
	v1 := s.engine.Group("/v1")
	v1.Use(AuthMiddleware(s.accessManager))
	{
		v1.GET("/models", s.unifiedModelsHandler(openaiHandlers, claudeCodeHandlers))
		v1.GET("/models/*model", func(c *gin.Context) {
			c.Set(handlers.ModelDetailIDContextKey, strings.TrimPrefix(c.Param("model"), "/"))
			s.unifiedModelsHandler(openaiHandlers, claudeCodeHandlers)(c)
		})
		v1.POST("/chat/completions", openaiHandlers.ChatCompletions)
		v1.POST("/completions", openaiHandlers.Completions)
		v1.POST("/images/generations", openaiHandlers.ImagesGenerations)
		v1.POST("/images/edits", openaiHandlers.ImagesEdits)
		v1.POST("/videos", openaiHandlers.XAIVideosGenerations)
		v1.POST("/videos/generations", openaiHandlers.XAIVideosGenerations)
		v1.POST("/videos/edits", openaiHandlers.XAIVideosEdits)
		v1.POST("/videos/extensions", openaiHandlers.XAIVideosExtensions)
		v1.GET("/videos/:request_id", openaiHandlers.XAIVideosRetrieve)
		v1.POST("/audio/speech", openaiHandlers.AudioSpeech)
		v1.POST("/tts", openaiHandlers.XAITTS)
		v1.POST("/messages", claudeCodeHandlers.ClaudeMessages)
		v1.POST("/messages/count_tokens", claudeCodeHandlers.ClaudeCountTokens)
		v1.GET("/responses", openaiResponsesHandlers.ResponsesWebsocket)
		v1.POST("/responses", openaiResponsesHandlers.Responses)
		v1.POST("/responses/compact", openaiResponsesHandlers.Compact)
		v1.POST("/alpha/search", s.codexAlphaSearch)
		v1.POST("/live", s.codexLiveHandler.Handle)
		v1.GET("/live/:call_id", s.codexLiveHandler.HandleSideband)
	}

	realtimeAuth := realtimeAuthMiddleware(s.accessManager, s.codexLiveHandler)
	standardAuth := realtimeStandardAuthMiddleware(s.accessManager)
	s.engine.GET("/v1/realtime", realtimeAuth, s.codexLiveHandler.HandleRealtimeWebsocket)
	s.engine.POST("/v1/realtime", realtimeAuth, s.codexLiveHandler.Handle)
	s.engine.POST("/v1/realtime/calls", realtimeAuth, s.codexLiveHandler.Handle)
	s.engine.GET("/v1/realtime/calls/:call_id", realtimeAuth, s.codexLiveHandler.HandleSideband)
	s.engine.POST("/v1/realtime/client_secrets", standardAuth, s.codexLiveHandler.CreateClientSecret)
	s.engine.POST("/v1/realtime/sessions", standardAuth, s.codexLiveHandler.CreateLegacySession)
	s.engine.POST("/v1/realtime/transcription_sessions", standardAuth, s.codexLiveHandler.HandleTranscriptionSession)
	s.engine.GET("/v1/realtime/translations", realtimeAuth, s.codexLiveHandler.HandleTranslation)
	s.engine.POST("/v1/realtime/translations", realtimeAuth, s.codexLiveHandler.HandleTranslation)
	s.engine.POST("/v1/realtime/translations/client_secrets", standardAuth, s.codexLiveHandler.HandleTranslation)
	s.engine.POST("/v1/realtime/calls/:call_id/hangup", standardAuth, s.codexLiveHandler.HandleHangup)
	s.engine.POST("/v1/realtime/calls/:call_id/accept", standardAuth, s.codexLiveHandler.HandleSIPControl)
	s.engine.POST("/v1/realtime/calls/:call_id/reject", standardAuth, s.codexLiveHandler.HandleSIPControl)
	s.engine.POST("/v1/realtime/calls/:call_id/refer", standardAuth, s.codexLiveHandler.HandleSIPControl)

	openaiV1 := s.engine.Group("/openai/v1")
	openaiV1.Use(AuthMiddleware(s.accessManager))
	{
		openaiV1.POST("/videos", openaiHandlers.VideosCreate)
		openaiV1.GET("/videos/:video_id/content", openaiHandlers.VideosContent)
		openaiV1.GET("/videos/:video_id", openaiHandlers.VideosRetrieve)
	}

	// Codex CLI direct route aliases (chatgpt_base_url compatible)
	codexDirect := s.engine.Group("/backend-api/codex")
	codexDirect.Use(AuthMiddleware(s.accessManager))
	{
		codexDirect.GET("/responses", openaiResponsesHandlers.ResponsesWebsocket)
		codexDirect.POST("/responses", openaiResponsesHandlers.Responses)
		codexDirect.POST("/responses/compact", openaiResponsesHandlers.Compact)
		codexDirect.POST("/alpha/search", s.codexAlphaSearch)
	}

	// Gemini compatible API routes
	v1beta := s.engine.Group("/v1beta")
	v1beta.Use(AuthMiddleware(s.accessManager))
	{
		v1beta.GET("/models", geminiHandlers.GeminiModels)
		v1beta.POST("/interactions", geminiHandlers.Interactions)
		v1beta.POST("/models/*action", geminiHandlers.GeminiHandler)
		v1beta.GET("/models/*action", geminiHandlers.GeminiGetHandler)
	}

	// Root endpoint
	s.engine.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "CLI Proxy API Server",
			"endpoints": []string{
				"POST /v1/chat/completions",
				"POST /v1/completions",
				"GET /v1/models",
			},
		})
	})

	// OAuth callback endpoints (reuse main server port)
	// These endpoints receive provider redirects and persist
	// the short-lived code/state for the waiting goroutine.
	s.engine.GET("/anthropic/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		errStr := c.Query("error")
		if errStr == "" {
			errStr = c.Query("error_description")
		}
		if state != "" {
			if cfg := s.getConfig(); cfg != nil {
				_, _ = managementHandlers.WriteOAuthCallbackFileForPendingSession(cfg.AuthDir, "anthropic", state, code, errStr)
			}
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	})

	s.engine.GET("/codex/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		errStr := c.Query("error")
		if errStr == "" {
			errStr = c.Query("error_description")
		}
		if state != "" {
			if cfg := s.getConfig(); cfg != nil {
				if _, errWrite := managementHandlers.WriteOAuthCallbackFileForPendingSession(cfg.AuthDir, "codex", state, code, errStr); errors.Is(errWrite, managementHandlers.ErrSIWCCallbackRequiresV8) {
					c.JSON(http.StatusConflict, gin.H{"error": "SIWC callbacks require /v8/management/oauth/callback"})
					return
				}
			}
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	})

	s.engine.GET("/antigravity/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		errStr := c.Query("error")
		if errStr == "" {
			errStr = c.Query("error_description")
		}
		if state != "" {
			if cfg := s.getConfig(); cfg != nil {
				_, _ = managementHandlers.WriteOAuthCallbackFileForPendingSession(cfg.AuthDir, "antigravity", state, code, errStr)
			}
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	})

	devinCallbackHandler := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		code := strings.TrimSpace(c.Query("code"))
		state := strings.TrimSpace(c.Query("state"))
		errStr := strings.TrimSpace(c.Query("error"))
		if errStr == "" {
			errStr = strings.TrimSpace(c.Query("error_description"))
		}
		if code == "" && errStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code or error is required"})
			return
		}
		cfg := s.getConfig()
		authDir := ""
		if cfg != nil {
			authDir = cfg.AuthDir
		}
		if _, errWrite := managementHandlers.WriteOAuthCallbackFileForPendingSession(authDir, "devin", state, code, errStr); errWrite != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired OAuth callback"})
			return
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	}

	s.engine.GET("/callback", devinCallbackHandler)
	s.engine.GET("/devin/callback", devinCallbackHandler)

	// Management routes are registered lazily by registerManagementRoutes when a secret is configured.
}

func (s *Server) codexAlphaSearchModelRouterHost() handlers.PluginModelRouterHost {
	if s == nil {
		return nil
	}
	if s.pluginHost != nil {
		return s.pluginHost
	}
	if s.handlers != nil {
		if host := s.handlers.ModelRouterHostSnapshot(); host != nil {
			return host
		}
	}
	return nil
}

func (s *Server) codexAlphaSearchSelectionModel(ctx context.Context, c *gin.Context, body []byte, model string) (string, error) {
	host := s.codexAlphaSearchModelRouterHost()
	if host == nil {
		return model, nil
	}

	var headers http.Header
	queryValues := make(map[string][]string)
	requestPath := ""
	if c != nil && c.Request != nil {
		headers = c.Request.Header.Clone()
		if c.Request.URL != nil {
			queryValues = c.Request.URL.Query()
			requestPath = c.Request.URL.Path
		}
	}
	metadata := map[string]any{
		coreexecutor.RequestedModelMetadataKey: model,
	}
	if requestPath != "" {
		metadata[coreexecutor.RequestPathMetadataKey] = requestPath
	}
	resp, handled := host.RouteModel(ctx, pluginapi.ModelRouteRequest{
		SourceFormat:   codexAlphaSearchSourceFormat,
		RequestedModel: model,
		Headers:        headers,
		Query:          queryValues,
		Body:           body,
		Metadata:       metadata,
	})
	if !handled || !resp.Handled {
		return model, nil
	}
	if resp.TargetKind != pluginapi.ModelRouteTargetProvider || !strings.EqualFold(strings.TrimSpace(resp.Target), "codex") {
		return "", fmt.Errorf("unsupported Codex Alpha Search model route target %q (%q)", resp.TargetKind, resp.Target)
	}
	if targetModel := strings.TrimSpace(resp.TargetModel); targetModel != "" {
		return targetModel, nil
	}
	return model, nil
}

func sanitizeCodexAlphaSearchBody(body []byte) []byte {
	var payload map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil || payload == nil {
		return body
	}

	removed := false
	for _, field := range []string{"prompt_cache_key", "prompt_cache_retention"} {
		if _, exists := payload[field]; exists {
			delete(payload, field)
			removed = true
		}
	}
	if !removed {
		return body
	}

	sanitizedBody, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return body
	}
	return sanitizedBody
}

// rewriteCodexAlphaSearchModel replaces the top-level model field with the
// credential-resolved upstream model before the request is forwarded.
func rewriteCodexAlphaSearchModel(body []byte, upstreamModel string) []byte {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return body
	}

	var payload map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil || payload == nil {
		return body
	}
	if _, exists := payload["model"]; !exists {
		return body
	}

	modelJSON, errMarshalModel := json.Marshal(upstreamModel)
	if errMarshalModel != nil {
		return body
	}
	if string(payload["model"]) == string(modelJSON) {
		return body
	}

	payload["model"] = modelJSON
	rewrittenBody, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return body
	}
	return rewrittenBody
}

func homeSelectionAttemptContext(ctx context.Context, selection *auth.HomeDispatchSelection) (context.Context, func(), error) {
	if selection == nil {
		return nil, func() {}, errors.New("Home dispatch selection is nil")
	}
	return selection.AttemptContext(ctx)
}

// codexAlphaSearch forwards the standalone search endpoint used by current
// Codex clients. Unlike /responses, this payload is already in Codex search
// format and must not pass through a protocol translator.
func (s *Server) codexAlphaSearch(c *gin.Context) {
	if s == nil || s.handlers == nil || s.handlers.AuthManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Codex auth manager unavailable"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 16<<20))
	if err != nil {
		c.JSON(clienterror.HTTPStatusFromErrorOr(err, http.StatusBadRequest), gin.H{"error": "Failed to read search request"})
		return
	}

	var routing struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &routing)
	upstreamRequestBody := sanitizeCodexAlphaSearchBody(body)

	selectionHeaders := c.Request.Header.Clone()
	if sessionID := strings.TrimSpace(routing.ID); sessionID != "" {
		selectionHeaders.Set("X-Session-ID", sessionID)
	}
	ctx := context.WithValue(c.Request.Context(), "gin", c)
	ctx = handlers.EnrichContextWithSessionHierarchy(ctx, selectionHeaders, body, nil)
	selectionModel, errRoute := s.codexAlphaSearchSelectionModel(ctx, c, body, strings.TrimSpace(routing.Model))
	if errRoute != nil {
		log.WithError(errRoute).Warn("codex alpha search: model router returned an unsupported target")
		c.JSON(clienterror.HTTPStatusFromErrorOr(errRoute, http.StatusServiceUnavailable), gin.H{"error": errRoute.Error()})
		return
	}
	selectionOpts := coreexecutor.Options{Headers: selectionHeaders, OriginalRequest: body}
	var selection *auth.HomeDispatchSelection
	var selected *auth.Auth
	if s.handlers.AuthManager.HomeEnabled() {
		selection, err = s.handlers.AuthManager.SelectHomeAuthWithCredentialPolicy(ctx, "codex", selectionModel, auth.CredentialPolicyCodexAlphaSearchV1, selectionOpts)
		if selection != nil {
			selected = selection.CloneAuth()
		}
	} else {
		selected, err = s.handlers.AuthManager.SelectAuthWithCredentialPolicy(ctx, "codex", selectionModel, auth.CredentialPolicyCodexAlphaSearchV1, selectionOpts)
	}
	if err != nil {
		status := clienterror.HTTPStatusFromErrorOr(err, http.StatusServiceUnavailable)
		for _, value := range auth.SafeResponseHeaders(err).Values("Retry-After") {
			c.Writer.Header().Add("Retry-After", value)
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if selected == nil {
		if selection != nil {
			selection.End("missing_auth")
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Codex auth unavailable"})
		return
	}
	if selection != nil && selection.CanonicalSessionID != "" {
		meta := logging.GetClientRequestMetadata(ctx)
		meta.SessionID = selection.CanonicalSessionID
		if selection.ParentSessionID != "" {
			meta.ParentSessionID = selection.ParentSessionID
		} else {
			meta.ParentSessionID = ""
		}
		if meta.SessionID == meta.ParentSessionID {
			meta.ParentSessionID = ""
		}
		ctx = logging.WithClientRequestMetadata(ctx, meta)
	}
	var releaseAttempt func()
	if selection != nil {
		attemptCtx, release, errBind := homeSelectionAttemptContext(ctx, selection)
		if errBind != nil {
			selection.End("attempt_bind_failed")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errBind.Error()})
			return
		}
		ctx = attemptCtx
		releaseAttempt = release
		defer releaseAttempt()
	}
	logging.SetGinCPATraceID(c, selected.EnsureIndex())

	baseHeaders := make(http.Header)
	baseHeaders.Set("Content-Type", "application/json")
	baseHeaders.Set("Accept", "application/json")
	baseHeaders.Set("Originator", "codex_cli_rs")
	for _, name := range []string{"Version", "User-Agent", "Session_id", "X-Client-Request-Id"} {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			baseHeaders.Set(name, value)
		}
	}

	errMissingBaseURL := errors.New("Codex Alpha Search API key base URL unavailable")
	routeModel := strings.TrimSpace(selectionModel)
	if routeModel == "" {
		routeModel = strings.TrimSpace(routing.Model)
	}
	performRequest := func(current *auth.Auth) (*http.Response, error) {
		headers := baseHeaders.Clone()
		if accountID, ok := current.Metadata["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			headers.Set("Chatgpt-Account-Id", accountID)
		}
		upstreamURL := "https://chatgpt.com/backend-api/codex/alpha/search"
		requestBody := upstreamRequestBody
		// API-key Alpha Search reuses normal credential-aware model resolution so
		// CPA routing prefixes and model aliases are not forwarded upstream.
		if current.AuthKind() == auth.AuthKindAPIKey {
			baseURL := ""
			if current.Attributes != nil {
				baseURL = strings.TrimSpace(current.Attributes["base_url"])
			}
			if baseURL == "" {
				return nil, errMissingBaseURL
			}
			upstreamURL = strings.TrimRight(baseURL, "/") + "/alpha/search"
			if upstreamModel := s.handlers.AuthManager.ResolveExecutionModel(current, routeModel); upstreamModel != "" {
				requestBody = rewriteCodexAlphaSearchModel(upstreamRequestBody, upstreamModel)
			}
		}
		req, errRequest := s.handlers.AuthManager.NewHttpRequest(ctx, current, http.MethodPost, upstreamURL, requestBody, headers)
		if errRequest != nil {
			return nil, errRequest
		}
		authType, authValue := current.AccountInfo()
		helps.RecordAPIRequest(ctx, s.getConfig(), helps.UpstreamRequestLog{
			URL:       upstreamURL,
			Method:    http.MethodPost,
			Headers:   req.Header.Clone(),
			Body:      requestBody,
			Provider:  "codex",
			AuthID:    current.ID,
			AuthLabel: current.Label,
			AuthType:  authType,
			AuthValue: authValue,
		})
		return s.handlers.AuthManager.HttpRequest(ctx, current, req)
	}

	if errCtx := ctx.Err(); errCtx != nil {
		if selection != nil {
			selection.End("attempt_canceled")
		}
		c.JSON(clienterror.HTTPStatusFromErrorOr(errCtx, http.StatusRequestTimeout), gin.H{"error": errCtx.Error()})
		return
	}
	cfg := s.getConfig()
	resp, err := performRequest(selected)
	if err != nil {
		if errors.Is(err, errMissingBaseURL) {
			if selection != nil {
				selection.End("missing_base_url")
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		if selection != nil {
			selection.End("request_failed")
		}
		helps.RecordAPIResponseError(ctx, cfg, err)
		c.JSON(clienterror.HTTPStatusFromErrorOr(err, http.StatusBadGateway), gin.H{"error": err.Error()})
		return
	}
	closeResponseBody := func() error {
		errClose := resp.Body.Close()
		if errClose != nil {
			log.Errorf("codex alpha search: close response body error: %v", errClose)
		}
		return errClose
	}
	if selection != nil {
		if errBind := selection.Bind(closeResponseBody); errBind != nil {
			if resp.StatusCode == http.StatusUnauthorized {
				s.handlers.AuthManager.ReportHomeUnauthorized(ctx, selected, "codex", selectionModel)
			}
			selection.End("response_bind_failed")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errBind.Error()})
			return
		}
		defer selection.End("response_closed")
	} else {
		defer func() { _ = closeResponseBody() }()
	}
	helps.RecordAPIResponseMetadata(ctx, cfg, resp.StatusCode, resp.Header.Clone())
	upstreamBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		helps.AppendAPIResponseChunk(ctx, cfg, upstreamBody)
		if selection != nil && resp.StatusCode == http.StatusUnauthorized {
			s.handlers.AuthManager.ReportHomeUnauthorized(ctx, selected, "codex", selectionModel, upstreamBody)
		}
		helps.RecordAPIResponseError(ctx, cfg, err)
		c.JSON(clienterror.HTTPStatusFromErrorOr(err, http.StatusBadGateway), gin.H{"error": "Failed to read Codex search response"})
		return
	}
	helps.AppendAPIResponseChunk(ctx, cfg, upstreamBody)
	if selection != nil && resp.StatusCode == http.StatusUnauthorized {
		s.handlers.AuthManager.ReportHomeUnauthorized(ctx, selected, "codex", selectionModel, upstreamBody)
		log.WithField("status", resp.StatusCode).Warnf("codex alpha search upstream request failed: %s", logging.SafeDiagnosticForLog(string(upstreamBody)))
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Status(resp.StatusCode)
	_, _ = c.Writer.Write(upstreamBody)
}

// AttachWebsocketRoute registers a websocket upgrade handler on the primary Gin engine.
// Home mode does not support AI Studio and must not expose its websocket gateway.
func (s *Server) AttachWebsocketRoute(path string, handler http.Handler) {
	if s == nil || s.engine == nil || handler == nil {
		return
	}
	if cfg := s.getConfig(); cfg != nil && cfg.Home.Enabled {
		return
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		trimmed = "/v1/ws"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	s.wsRouteMu.Lock()
	if _, exists := s.wsRoutes[trimmed]; exists {
		s.wsRouteMu.Unlock()
		return
	}
	s.wsRoutes[trimmed] = struct{}{}
	s.wsRouteMu.Unlock()

	authMiddleware := AuthMiddleware(s.accessManager)
	conditionalAuth := func(c *gin.Context) {
		if !s.wsAuthEnabled.Load() {
			middleware.CaptureAuthenticatedRequestBody(c)
			if c.IsAborted() {
				return
			}
			c.Next()
			return
		}
		authMiddleware(c)
	}
	finalHandler := func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}

	s.engine.GET(trimmed, conditionalAuth, finalHandler)
}

// isAnthropicModelsRequest reports whether a /v1/models request should be served in
// Anthropic format. Anthropic API clients send the Anthropic-Version header; Claude
// Code additionally uses a claude-cli User-Agent.
func isAnthropicModelsRequest(c *gin.Context) bool {
	if c.GetHeader("Anthropic-Version") != "" {
		return true
	}
	return strings.HasPrefix(c.GetHeader("User-Agent"), "claude-cli")
}

// unifiedModelsHandler creates a unified handler for the /v1/models endpoint
// that routes to different handlers based on the request.
// Anthropic API requests (Anthropic-Version header, or a claude-cli User-Agent)
// route to the Claude handler, otherwise they route to the OpenAI handler.
func (s *Server) unifiedModelsHandler(openaiHandler *openai.OpenAIAPIHandler, claudeHandler *claude.ClaudeCodeAPIHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if grokbuild.IsGrokShellUserAgent(c.GetHeader("User-Agent")) {
			s.handleGrokModels(c)
			return
		}

		if _, ok := c.Request.URL.Query()["client_version"]; ok {
			openaiHandler.OpenAIModels(c)
			return
		}

		// Route to Claude handler for Anthropic API requests.
		if isAnthropicModelsRequest(c) {
			claudeHandler.ClaudeModels(c)
		} else {
			openaiHandler.OpenAIModels(c)
		}
	}
}

func grokModelsFromRegistryInfos(infos []*registry.ModelInfo) []grokbuild.ModelInfo {
	models := make([]grokbuild.ModelInfo, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		model := grokbuild.ModelInfo{
			ID:            info.ID,
			DisplayName:   info.DisplayName,
			ContextLength: info.ContextLength,
		}
		if info.Thinking != nil {
			model.ReasoningLevels = append([]string(nil), info.Thinking.Levels...)
		}
		models = append(models, model)
	}
	return models
}

func (s *Server) handleGrokModels(c *gin.Context) {
	models := grokModelsFromRegistryInfos(registry.GetGlobalRegistry().GetAvailableModelInfos())
	s.writeModelListResponse(c, "openai", grokbuild.BuildResponse(models))
}

func (s *Server) writeModelListResponse(c *gin.Context, sourceFormat string, payload any) {
	if s != nil && s.handlers != nil {
		s.handlers.WriteModelListResponse(c, sourceFormat, payload)
		return
	}
	c.JSON(http.StatusOK, payload)
}
