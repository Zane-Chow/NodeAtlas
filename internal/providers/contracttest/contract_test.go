package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestRunAcceptsConformingFixture(t *testing.T) {
	provider := &fixtureProvider{pages: map[string]providers.ServerPage{
		"": {
			Servers: []providers.RemoteServer{server("one", "zone-a", providers.StateRunning)},
			Next:    &providers.Cursor{Value: "page-2"},
		},
		"page-2": {Servers: []providers.RemoteServer{server("two", "zone-b", providers.StateStopped)}},
	}}

	Run(t, Fixture{
		Provider: provider,
		ErrorCases: []ErrorCase{{
			Name: "missing server",
			Call: func(ctx context.Context) error {
				_, err := provider.GetServer(ctx, providers.ServerRef{ExternalID: "missing"})
				return fmt.Errorf("detail lookup: %w", err)
			},
			WantRetryable: false,
		}},
	})
}

func TestRunBoundsPaginationWithoutLeakingPayload(t *testing.T) {
	recorder := &recordingT{}
	secret := "super-secret-upstream-payload"
	provider := &fixtureProvider{pages: map[string]providers.ServerPage{
		"":       {Next: &providers.Cursor{Value: "repeat"}},
		"repeat": {Next: &providers.Cursor{Value: "repeat"}},
	}, unsafeError: errors.New(secret)}

	Run(recorder, Fixture{
		Provider: provider,
		Limits:   Limits{MaxPages: 2, MaxServers: 2},
		ErrorCases: []ErrorCase{
			{
				Name: "unsafe upstream failure",
				Call: func(context.Context) error { return provider.unsafeError },
			},
			{
				Name:      "unsafe safe-message",
				Call:      func(context.Context) error { return &providers.Error{Code: providers.ErrorProvider, Message: secret} },
				Forbidden: []string{secret},
			},
		},
	})

	require.True(t, recorder.failed)
	require.NotContains(t, recorder.messages(), secret)
	require.Contains(t, recorder.messages(), "cursor")
	require.Contains(t, recorder.messages(), "safe error")
	require.Contains(t, recorder.messages(), "forbidden data")
}

func TestRunRejectsInvalidInventoryWithoutDumpingJSON(t *testing.T) {
	recorder := &recordingT{}
	secret := "credential-from-raw-payload"
	bad := server("duplicate", "zone-a", providers.ServerState("mystery"))
	bad.Spec = json.RawMessage(`{"secret":"` + secret + `"`)
	bad.Addresses = json.RawMessage(`not-json-` + secret)
	bad.Capabilities.CanStart = providers.Capability{Available: false}
	provider := &fixtureProvider{pages: map[string]providers.ServerPage{
		"": {Servers: []providers.RemoteServer{bad, bad}},
	}}

	Run(recorder, Fixture{Provider: provider})

	require.True(t, recorder.failed)
	require.NotContains(t, recorder.messages(), secret)
	require.Contains(t, recorder.messages(), "canonical state")
	require.Contains(t, recorder.messages(), "valid JSON")
	require.Contains(t, recorder.messages(), "duplicate server identity")
	require.Contains(t, recorder.messages(), "unavailable capability requires a reason")
}

func TestRunRejectsCapabilitiesInconsistentWithServerState(t *testing.T) {
	tests := []struct {
		name   string
		state  providers.ServerState
		mutate func(*providers.Capabilities)
		want   string
	}{
		{
			name:  "running server cannot start",
			state: providers.StateRunning,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStart = providers.Capability{Available: true}
			},
			want: "running server must not allow start",
		},
		{
			name:  "stopped server cannot stop",
			state: providers.StateStopped,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStop = providers.Capability{Available: true}
			},
			want: "stopped server must not allow stop or reboot",
		},
		{
			name:  "stopped server cannot reboot",
			state: providers.StateStopped,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanReboot = providers.Capability{Available: true}
			},
			want: "stopped server must not allow stop or reboot",
		},
		{
			name:  "suspended server cannot use power",
			state: providers.StateSuspended,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStart = providers.Capability{Available: true}
			},
			want: "suspended server must not allow power or console operations",
		},
		{
			name:  "suspended server cannot use embedded console",
			state: providers.StateSuspended,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanEmbedConsole = providers.Capability{Available: true}
			},
			want: "suspended server must not allow power or console operations",
		},
		{
			name:  "suspended server cannot use window console",
			state: providers.StateSuspended,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanOpenConsoleWindow = providers.Capability{Available: true}
			},
			want: "suspended server must not allow power or console operations",
		},
		{
			name:  "pending server cannot use power",
			state: providers.StatePending,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStop = providers.Capability{Available: true}
			},
			want: "transitional or unsafe server state must not allow power operations",
		},
		{
			name:  "stopping server cannot use power",
			state: providers.StateStopping,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanReboot = providers.Capability{Available: true}
			},
			want: "transitional or unsafe server state must not allow power operations",
		},
		{
			name:  "rebooting server cannot use power",
			state: providers.StateRebooting,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStart = providers.Capability{Available: true}
			},
			want: "transitional or unsafe server state must not allow power operations",
		},
		{
			name:  "unknown server cannot use power",
			state: providers.StateUnknown,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStart = providers.Capability{Available: true}
			},
			want: "transitional or unsafe server state must not allow power operations",
		},
		{
			name:  "error server cannot use power",
			state: providers.StateError,
			mutate: func(capabilities *providers.Capabilities) {
				capabilities.CanStop = providers.Capability{Available: true}
			},
			want: "transitional or unsafe server state must not allow power operations",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingT{}
			item := server("one", "zone-a", test.state)
			test.mutate(&item.Capabilities)
			provider := &fixtureProvider{pages: map[string]providers.ServerPage{"": {Servers: []providers.RemoteServer{item}}}}

			Run(recorder, Fixture{Provider: provider})

			require.Contains(t, recorder.messages(), test.want)
		})
	}
}

func TestServerIdentityTupleCannotCollideOnNUL(t *testing.T) {
	leftServer := server("\x00b", "a", providers.StateRunning)
	rightServer := server("b", "a\x00", providers.StateStopped)
	left := identity(leftServer)
	right := identity(rightServer)

	require.NotEqual(t, left, right)
	identities := map[serverIdentity]struct{}{left: {}, right: {}}
	require.Len(t, identities, 2)

	recorder := &recordingT{}
	provider := &fixtureProvider{pages: map[string]providers.ServerPage{"": {Servers: []providers.RemoteServer{leftServer, rightServer}}}}
	Run(recorder, Fixture{Provider: provider})
	require.False(t, recorder.failed, recorder.messages())
}

func TestRunAllowsConservativeCapabilitiesForStableStates(t *testing.T) {
	running := server("running", "zone-a", providers.StateRunning)
	stopped := server("stopped", "zone-a", providers.StateStopped)
	for _, item := range []*providers.RemoteServer{&running, &stopped} {
		item.Capabilities = providers.Capabilities{
			CanStart:             providers.Capability{Reason: "provider does not expose start"},
			CanStop:              providers.Capability{Reason: "provider does not expose stop"},
			CanReboot:            providers.Capability{Reason: "provider does not expose reboot"},
			CanEmbedConsole:      providers.Capability{Reason: "provider does not expose a console"},
			CanOpenConsoleWindow: providers.Capability{Reason: "provider does not expose a console"},
			HasProviderPortal:    providers.Capability{Reason: "provider does not expose a portal"},
		}
	}
	recorder := &recordingT{}
	provider := &fixtureProvider{pages: map[string]providers.ServerPage{"": {Servers: []providers.RemoteServer{running, stopped}}}}

	Run(recorder, Fixture{Provider: provider})

	require.False(t, recorder.failed, recorder.messages())
}

func TestRunEnforcesPageAndServerLimits(t *testing.T) {
	t.Run("servers", func(t *testing.T) {
		recorder := &recordingT{}
		provider := &fixtureProvider{pages: map[string]providers.ServerPage{
			"": {Servers: []providers.RemoteServer{
				server("one", "zone-a", providers.StateRunning),
				server("two", "zone-a", providers.StateRunning),
			}},
		}}
		Run(recorder, Fixture{Provider: provider, Limits: Limits{MaxPages: 2, MaxServers: 1}})
		require.Contains(t, recorder.messages(), "server count limit")
	})

	t.Run("pages", func(t *testing.T) {
		recorder := &recordingT{}
		provider := &fixtureProvider{pages: map[string]providers.ServerPage{
			"":       {Next: &providers.Cursor{Value: "second"}},
			"second": {Next: &providers.Cursor{Value: "third"}},
			"third":  {},
		}}
		Run(recorder, Fixture{Provider: provider, Limits: Limits{MaxPages: 2, MaxServers: 1}})
		require.Contains(t, recorder.messages(), "page count limit")
	})
}

func TestRunRejectsUnstableIdentityAndGetMismatch(t *testing.T) {
	recorder := &recordingT{}
	provider := &fixtureProvider{
		pages:      map[string]providers.ServerPage{"": {Servers: []providers.RemoteServer{server("one", "zone-a", providers.StateRunning)}}},
		secondPass: map[string]providers.ServerPage{"": {Servers: []providers.RemoteServer{server("renamed-id", "zone-a", providers.StateRunning)}}},
		getOverride: func(ref providers.ServerRef) providers.RemoteServer {
			return server(ref.ExternalID, "wrong-zone", providers.StateRunning)
		},
	}

	Run(recorder, Fixture{Provider: provider})

	require.Contains(t, recorder.messages(), "stable identities")
	require.Contains(t, recorder.messages(), "GetServer identity")
}

type recordingT struct {
	failed bool
	lines  []string
}

func (recorder *recordingT) Helper() {}

func (recorder *recordingT) Errorf(format string, args ...any) {
	recorder.failed = true
	recorder.lines = append(recorder.lines, strings.TrimSpace(formatMessage(format, args...)))
}

func (recorder *recordingT) messages() string { return strings.Join(recorder.lines, "\n") }

func formatMessage(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

type fixtureProvider struct {
	pages       map[string]providers.ServerPage
	secondPass  map[string]providers.ServerPage
	listCalls   int
	getOverride func(providers.ServerRef) providers.RemoteServer
	unsafeError error
}

func (fixture *fixtureProvider) ValidateConnection(context.Context) (providers.ConnectionInfo, error) {
	return providers.ConnectionInfo{DisplayName: "Fixture Panel", Version: "1"}, nil
}

func (fixture *fixtureProvider) ListServers(_ context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	key := ""
	if cursor != nil {
		key = cursor.Value
	}
	fixture.listCalls++
	pages := fixture.pages
	if fixture.secondPass != nil && fixture.listCalls > len(fixture.pages) {
		pages = fixture.secondPass
	}
	return pages[key], nil
}

func (fixture *fixtureProvider) GetServer(_ context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	if fixture.getOverride != nil {
		return fixture.getOverride(ref), nil
	}
	for _, page := range fixture.pages {
		for _, item := range page.Servers {
			if item.ExternalID == ref.ExternalID && item.Scope == ref.Scope {
				return item, nil
			}
		}
	}
	return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "fixture server not found"}
}

func (*fixtureProvider) StartServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{RequestID: "start"}, nil
}
func (*fixtureProvider) StopServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{RequestID: "stop"}, nil
}
func (*fixtureProvider) RebootServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{RequestID: "reboot"}, nil
}
func (*fixtureProvider) OpenConsole(context.Context, providers.ServerRef, providers.ConsoleMode) (providers.ConsoleTarget, error) {
	return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "fixture console unavailable"}
}
func (*fixtureProvider) ProviderPortalURL(context.Context, providers.ServerRef) (*url.URL, error) {
	return nil, &providers.Error{Code: providers.ErrorUnsupported, Message: "fixture portal unavailable"}
}

func server(id, scope string, state providers.ServerState) providers.RemoteServer {
	capabilities := providers.Capabilities{
		CanStart:             providers.Capability{Reason: "server cannot start in this state"},
		CanStop:              providers.Capability{Reason: "server cannot stop in this state"},
		CanReboot:            providers.Capability{Reason: "server cannot reboot in this state"},
		CanEmbedConsole:      providers.Capability{Reason: "not supported by fixture"},
		CanOpenConsoleWindow: providers.Capability{Reason: "not supported by fixture"},
		HasProviderPortal:    providers.Capability{Reason: "not supported by fixture"},
	}
	if state == providers.StateRunning {
		capabilities.CanStop = providers.Capability{Available: true}
		capabilities.CanReboot = providers.Capability{Available: true}
	}
	if state == providers.StateStopped {
		capabilities.CanStart = providers.Capability{Available: true}
	}
	return providers.RemoteServer{
		ExternalID:   id,
		Scope:        scope,
		Name:         "fixture-" + id,
		State:        state,
		RemoteState:  string(state),
		Spec:         json.RawMessage(`{"cpu":2}`),
		Addresses:    json.RawMessage(`[]`),
		Capabilities: capabilities,
	}
}
