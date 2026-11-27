// Package localexec holds the credential-free local-executor contract that the
// fork still needs after pruning internal/home and internal/homeplugins.
//
// Upstream keeps these types next to the Home control-plane client. This fork
// removes the Home client but keeps the plugin-executor plumbing, so the small
// data types are copied here verbatim to keep the call sites and the wire
// contract identical. They are a leaf package on purpose: everything else may
// import it without dragging in a network client.
package localexec

import "context"

// ACPModelPrefix marks a model request as an agent-client-protocol dispatch.
const ACPModelPrefix = "acp/"

// LocalExecutorDispatchKind is the Home reply kind for a credential-free plugin route.
const LocalExecutorDispatchKind = "local-plugin"

// LocalExecutorDecision is the Home reply for a credential-free plugin route.
type LocalExecutorDecision struct {
	Kind         string `json:"kind"`
	Plugin       string `json:"plugin"`
	Version      string `json:"version"`
	ConfigDigest string `json:"config_digest"`
	Model        string `json:"model"`
	Principal    string `json:"principal"`
	Access       string `json:"access_provider"`
	Error        *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// LocalExecutorGrant is the authorization handed to an executor adapter.
type LocalExecutorGrant struct {
	Plugin       string
	Version      string
	ConfigDigest string
	Model        string
	Valid        func() bool
}

type localExecutorGrantKey struct{}

// WithLocalExecutorGrant attaches an authorization grant to the request context.
func WithLocalExecutorGrant(ctx context.Context, grant LocalExecutorGrant) context.Context {
	return context.WithValue(ctx, localExecutorGrantKey{}, grant)
}

// ExecutorGrant reports the authorization grant attached to the request context.
func ExecutorGrant(ctx context.Context) (LocalExecutorGrant, bool) {
	if ctx == nil {
		return LocalExecutorGrant{}, false
	}
	grant, ok := ctx.Value(localExecutorGrantKey{}).(LocalExecutorGrant)
	return grant, ok
}
