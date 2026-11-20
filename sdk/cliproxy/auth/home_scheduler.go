package auth

import (
	"context"
	"errors"
	"net/http"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// HomeSchedulerContinuityError preserves the structured Home error envelope so the
// execution layer can apply the normal dispatch error classification.
type HomeSchedulerContinuityError struct {
	Payload []byte
}

func (e *HomeSchedulerContinuityError) Error() string {
	return "home rejected scheduler continuity observation"
}

// HomeSchedulerContinuityQuery carries query parameters for observing Home candidates.
type HomeSchedulerContinuityQuery struct {
	Request                   pluginapi.SchedulerContinuityRequest `json:"request"`
	CredentialPolicy          string                               `json:"credential_policy,omitempty"`
	RetryRound                int                                  `json:"retry_round"`
	ApplyRetryRoundExclusions bool                                 `json:"apply_retry_round_exclusions"`
	ExcludedAuthIDs           []string                             `json:"excluded_auth_ids,omitempty"`
	PinnedAuthID              string                               `json:"pinned_auth_id,omitempty"`
}

type homeContinuityClient interface {
	SchedulerProtocol() int
	EvaluateSchedulerContinuity(context.Context, HomeSchedulerContinuityQuery) (pluginapi.SchedulerContinuityResponse, bool, error)
}

// homeSchedulerContinuity delegates observation to Home while CPA retains the
// request-wide deadline, poll clock, cancellation and pre-output lifecycle.
type homeSchedulerContinuity struct {
	client  homeContinuityClient
	manager *Manager
}

func (h *homeSchedulerContinuity) HasSchedulerContinuity() bool {
	return h.client.SchedulerProtocol() >= 1
}

func (h *homeSchedulerContinuity) EvaluateSchedulerContinuity(ctx context.Context, req pluginapi.SchedulerContinuityRequest) (pluginapi.SchedulerContinuityResponse, bool, error) {
	metadata := req.Options.Metadata
	if req.CanonicalSessionID == "" {
		req.CanonicalSessionID, _ = h.manager.homeDispatchSessionIDs(cliproxyexecutor.Options{Headers: http.Header(req.Options.Headers), Metadata: metadata})
	}
	applyRound, _ := metadata["apply_retry_round_exclusions"].(bool)
	query := HomeSchedulerContinuityQuery{
		Request: req, CredentialPolicy: credentialPolicyFromContext(ctx),
		RetryRound: homeRetryRoundFromMetadata(metadata), ApplyRetryRoundExclusions: applyRound,
		ExcludedAuthIDs: homeExcludedAuthIDsFromMetadata(metadata), PinnedAuthID: pinnedAuthIDFromMetadata(metadata),
	}
	query.Request.Options.Headers = homeDispatchHeaders(ctx, http.Header(req.Options.Headers))
	query.Request.Options.Metadata = nil
	query.Request.Candidates = nil
	response, handled, errEvaluate := h.client.EvaluateSchedulerContinuity(ctx, query)
	var rejected *HomeSchedulerContinuityError
	if errors.As(errEvaluate, &rejected) && rejected != nil {
		if errDecode := decodeHomeDispatchError(rejected.Payload); errDecode != nil {
			return pluginapi.SchedulerContinuityResponse{}, false, errDecode
		}
		return pluginapi.SchedulerContinuityResponse{}, false, &Error{Code: "invalid_auth", Message: "Home returned a malformed continuity error", HTTPStatus: http.StatusBadGateway}
	}
	return response, handled, errEvaluate
}
