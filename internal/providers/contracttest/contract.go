// Package contracttest contains reusable conformance checks for provider adapters.
//
// It is intended for tests only. Adapters remain normal, statically compiled Go
// packages; this package is not a runtime plugin mechanism.
package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"controlpanel/internal/providers"
)

const (
	defaultMaxPages   = 100
	defaultMaxServers = 10_000
)

// TestingT is the subset of testing.T used by Run. Keeping the interface small
// also lets this package test its own failure reporting without printing fixture
// payloads or secrets.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
}

// Limits bounds inventory traversal. Zero values select conservative defaults.
type Limits struct {
	MaxPages   int
	MaxServers int
}

// ErrorCase verifies that a deliberately failing adapter call exposes only the
// application's safe error interfaces. Forbidden values are inspected but are
// never copied into a failure message.
type ErrorCase struct {
	Name          string
	Call          func(context.Context) error
	WantRetryable bool
	Forbidden     []string
}

// Fixture describes one provider instance and optional safe-error probes.
type Fixture struct {
	Provider   providers.Provider
	Context    context.Context
	Limits     Limits
	ErrorCases []ErrorCase
}

// Run checks the provider's validation, bounded pagination, stable identities,
// normalized inventory, detail lookup, capabilities, and safe error contract.
// Diagnostics intentionally identify only the failed invariant, never a full
// server, upstream payload, URL, credential, or raw error.
func Run(testing TestingT, fixture Fixture) {
	testing.Helper()
	if fixture.Provider == nil {
		testing.Errorf("contract: provider is required")
		return
	}
	ctx := fixture.Context
	if ctx == nil {
		ctx = context.Background()
	}
	limits := normalizeLimits(fixture.Limits)

	info, err := fixture.Provider.ValidateConnection(ctx)
	if err != nil {
		testing.Errorf("contract: connection validation must succeed")
	} else if strings.TrimSpace(info.DisplayName) == "" || strings.TrimSpace(info.Version) == "" {
		testing.Errorf("contract: connection information must be non-empty")
	}

	first, firstOK := collectInventory(testing, ctx, fixture.Provider, limits)
	if firstOK {
		for _, item := range first {
			validateServer(testing, item)
			detail, detailErr := fixture.Provider.GetServer(ctx, providers.ServerRef{ExternalID: item.ExternalID, Scope: item.Scope})
			if detailErr != nil {
				testing.Errorf("contract: GetServer must succeed for a listed server")
				continue
			}
			if identity(detail) != identity(item) {
				testing.Errorf("contract: GetServer identity must match list identity")
			}
			validateServer(testing, detail)
		}
	}

	second, secondOK := collectInventory(testing, ctx, fixture.Provider, limits)
	if firstOK && secondOK && !sameIdentities(first, second) {
		testing.Errorf("contract: repeated inventory traversal must return stable identities")
	}

	for _, errorCase := range fixture.ErrorCases {
		validateErrorCase(testing, ctx, errorCase)
	}
}

func normalizeLimits(limits Limits) Limits {
	if limits.MaxPages <= 0 {
		limits.MaxPages = defaultMaxPages
	}
	if limits.MaxServers <= 0 {
		limits.MaxServers = defaultMaxServers
	}
	return limits
}

func collectInventory(testing TestingT, ctx context.Context, provider providers.Provider, limits Limits) ([]providers.RemoteServer, bool) {
	var cursor *providers.Cursor
	seenCursors := make(map[string]struct{})
	seenServers := make(map[string]struct{})
	servers := make([]providers.RemoteServer, 0)
	for pageNumber := 1; pageNumber <= limits.MaxPages; pageNumber++ {
		page, err := provider.ListServers(ctx, cursor)
		if err != nil {
			testing.Errorf("contract: inventory page must load successfully")
			return servers, false
		}
		if len(page.Servers) > limits.MaxServers-len(servers) {
			testing.Errorf("contract: inventory exceeds server count limit")
			return servers, false
		}
		for _, item := range page.Servers {
			key := identity(item)
			if _, exists := seenServers[key]; exists {
				testing.Errorf("contract: duplicate server identity in inventory")
			} else {
				seenServers[key] = struct{}{}
			}
			servers = append(servers, item)
		}
		if page.Next == nil {
			return servers, true
		}
		next := strings.TrimSpace(page.Next.Value)
		if next == "" {
			testing.Errorf("contract: next cursor must be non-empty")
			return servers, false
		}
		if _, exists := seenCursors[next]; exists {
			testing.Errorf("contract: pagination cursor must advance without repetition")
			return servers, false
		}
		seenCursors[next] = struct{}{}
		cursor = &providers.Cursor{Value: next}
	}
	testing.Errorf("contract: inventory exceeds page count limit")
	return servers, false
}

func validateServer(testing TestingT, server providers.RemoteServer) {
	if strings.TrimSpace(server.ExternalID) == "" {
		testing.Errorf("contract: server external ID must be non-empty")
	}
	if strings.TrimSpace(server.Scope) == "" {
		testing.Errorf("contract: server scope must be non-empty")
	}
	if strings.TrimSpace(server.Name) == "" {
		testing.Errorf("contract: server name must be non-empty")
	}
	if strings.TrimSpace(server.RemoteState) == "" {
		testing.Errorf("contract: remote state must be non-empty")
	}
	if !canonicalState(server.State) {
		testing.Errorf("contract: server must use a canonical state")
	}
	if len(server.Spec) == 0 || !json.Valid(server.Spec) {
		testing.Errorf("contract: server spec must be valid JSON")
	}
	if len(server.Addresses) == 0 || !json.Valid(server.Addresses) {
		testing.Errorf("contract: server addresses must be valid JSON")
	}
	validateCapabilities(testing, server.Capabilities)
}

func canonicalState(state providers.ServerState) bool {
	switch state {
	case providers.StatePending, providers.StateRunning, providers.StateStopping,
		providers.StateStopped, providers.StateRebooting, providers.StateSuspended,
		providers.StateError, providers.StateUnknown:
		return true
	default:
		return false
	}
}

func validateCapabilities(testing TestingT, capabilities providers.Capabilities) {
	checks := []providers.Capability{
		capabilities.CanStart,
		capabilities.CanStop,
		capabilities.CanReboot,
		capabilities.CanEmbedConsole,
		capabilities.CanOpenConsoleWindow,
		capabilities.HasProviderPortal,
	}
	for _, capability := range checks {
		if capability.Available && strings.TrimSpace(capability.Reason) != "" {
			testing.Errorf("contract: available capability must not include a reason")
		}
		if !capability.Available && strings.TrimSpace(capability.Reason) == "" {
			testing.Errorf("contract: unavailable capability requires a reason")
		}
	}
}

func validateErrorCase(testing TestingT, ctx context.Context, errorCase ErrorCase) {
	if strings.TrimSpace(errorCase.Name) == "" || errorCase.Call == nil {
		testing.Errorf("contract: safe error case requires a name and call")
		return
	}
	err := errorCase.Call(ctx)
	if err == nil {
		testing.Errorf("contract: safe error case must return an error")
		return
	}
	var safe interface{ SafeMessage() string }
	var retryable interface{ RetryableError() bool }
	safeOK := errors.As(err, &safe)
	retryableOK := errors.As(err, &retryable)
	if !safeOK || !retryableOK {
		testing.Errorf("contract: safe error must implement SafeMessage and RetryableError")
		return
	}
	message := strings.TrimSpace(safe.SafeMessage())
	if message == "" {
		testing.Errorf("contract: safe error message must be non-empty")
	}
	for _, forbidden := range errorCase.Forbidden {
		if forbidden != "" && strings.Contains(message, forbidden) {
			testing.Errorf("contract: safe error message contains forbidden data")
		}
	}
	if retryable.RetryableError() != errorCase.WantRetryable {
		testing.Errorf("contract: safe error retryable classification is incorrect")
	}
}

func identity(server providers.RemoteServer) string {
	return server.Scope + "\x00" + server.ExternalID
}

func sameIdentities(left, right []providers.RemoteServer) bool {
	leftIDs := identities(left)
	rightIDs := identities(right)
	if len(leftIDs) != len(rightIDs) {
		return false
	}
	for index := range leftIDs {
		if leftIDs[index] != rightIDs[index] {
			return false
		}
	}
	return true
}

func identities(servers []providers.RemoteServer) []string {
	result := make([]string, 0, len(servers))
	for _, server := range servers {
		result = append(result, identity(server))
	}
	sort.Strings(result)
	return result
}
