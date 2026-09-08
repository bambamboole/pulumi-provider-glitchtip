package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// fastBootstrap shortens the readiness waits for the duration of a test.
func fastBootstrap(t *testing.T) {
	t.Helper()
	refresh, poll := bootstrapRefreshTimeout, bootstrapPollInterval
	bootstrapRefreshTimeout, bootstrapPollInterval = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { bootstrapRefreshTimeout, bootstrapPollInterval = refresh, poll })
}

func bootstrapArgs(baseURL string) BootstrapArgs {
	return BootstrapArgs{
		BaseURL:             baseURL,
		Email:               "owner@example.com",
		Password:            "owner-secret",
		TokenLabel:          "infrastructure",
		Scopes:              normalizeScopes(defaultBootstrapScopes),
		ReadyTimeoutSeconds: 1,
	}
}

func TestBootstrapCheckAppliesDefaults(t *testing.T) {
	response, err := (Bootstrap{}).Check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(map[string]property.Value{
		"email":    property.New("owner@example.com"),
		"password": property.New("owner-secret"),
		"scopes":   property.New([]property.Value{property.New("project:write"), property.New("project:read"), property.New("project:read")}),
	})})
	if err != nil {
		t.Fatal(err)
	}
	if response.Inputs.TokenLabel != "pulumi" || response.Inputs.ReadyTimeoutSeconds != 180 {
		t.Fatalf("defaults not applied: %+v", response.Inputs)
	}
	if got := strings.Join(response.Inputs.Scopes, ","); got != "project:read,project:write" {
		t.Fatalf("scopes not normalized: %q", got)
	}
}

func TestBootstrapCreateIssuesTokenAfterReadiness(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.ready = false
	go func() {
		time.Sleep(200 * time.Millisecond)
		fake.mu.Lock()
		fake.ready = true
		fake.mu.Unlock()
	}()
	args := bootstrapArgs(fake.server.URL)
	args.ReadyTimeoutSeconds = 5

	response, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if response.ID != "owner@example.com" || response.Output.TokenID == "" || !strings.HasPrefix(response.Output.Token, "tok-") {
		t.Fatalf("unexpected output %+v", response.Output)
	}
	if fake.countRequests("POST", "/api/0/api-tokens/") != 1 {
		t.Fatalf("expected one token creation, got %v", fake.requests)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tokens) != 1 || fake.tokens[0].Label != "infrastructure" || len(fake.tokens[0].Scopes) != len(defaultBootstrapScopes) {
		t.Fatalf("token not issued with label and scopes: %+v", fake.tokens)
	}
}

func TestBootstrapCreateAdoptsMatchingToken(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.addToken("other", []string{"org:read"})
	fake.addToken("infrastructure", []string{"project:read"}) // same label, other scopes: not adopted
	existing := fake.addToken("infrastructure", normalizeScopes(defaultBootstrapScopes))

	response, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if response.Output.Token != existing.Token {
		t.Fatalf("expected the existing token to be adopted, got %+v", response.Output)
	}
	if fake.countRequests("POST", "/api/0/api-tokens/") != 0 {
		t.Fatalf("no token must be issued, got %v", fake.requests)
	}
	// The listing spans two pages of the fake's pagination.
	if fake.countRequests("GET", "/api/0/api-tokens/") != 2 {
		t.Fatalf("expected two list pages, got %v", fake.requests)
	}
}

func TestBootstrapCreateRejectsWrongPassword(t *testing.T) {
	fake := newFakeGlitchTip(t)
	args := bootstrapArgs(fake.server.URL)
	args.Password = "wrong"
	_, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err == nil || !glitchtip.IsUnauthorized(err) && !strings.Contains(err.Error(), "login rejected") {
		t.Fatalf("expected a login error, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error must not echo the password: %v", err)
	}
}

func TestBootstrapCreateFailsWhenNeverReady(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.ready = false
	_, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if !errors.Is(err, glitchtip.ErrNotReady) {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
}

func createdBootstrap(t *testing.T, fake *fakeGlitchTip) BootstrapState {
	t.Helper()
	response, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return response.Output
}

func TestBootstrapReadKeepsStateAndDetectsRevocation(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	request := infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: state.BootstrapArgs, State: state}

	read, err := (Bootstrap{}).Read(context.Background(), request)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.ID == "" || read.State.RepairRequired || read.State.Token != state.Token {
		t.Fatalf("intact token must read back unchanged: %+v", read.State)
	}

	fake.mu.Lock()
	fake.tokens = nil
	fake.mu.Unlock()
	read, err = (Bootstrap{}).Read(context.Background(), request)
	if err != nil {
		t.Fatalf("Read after revocation: %v", err)
	}
	if read.ID == "" || !read.State.RepairRequired || read.State.Token != state.Token {
		t.Fatalf("revoked token must mark repair and keep the state: %+v", read.State)
	}
	diff, err := (Bootstrap{}).Diff(context.Background(), infer.DiffRequest[BootstrapArgs, BootstrapState]{Inputs: state.BootstrapArgs, State: read.State})
	if err != nil || !diff.HasChanges {
		t.Fatalf("repair must schedule an update: %+v %v", diff, err)
	}
}

func TestBootstrapReadMarksUnreachableHostForRepair(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	fake.ready = false

	start := time.Now()
	read, err := (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: state.BootstrapArgs, State: state})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.ID == "" || !read.State.RepairRequired {
		t.Fatalf("unreachable host must mark repair: %+v", read.State)
	}
	if time.Since(start) > 2*bootstrapRefreshTimeout {
		t.Fatalf("refresh waited too long: %v", time.Since(start))
	}
}

func TestBootstrapReadFailsOnRejectedLogin(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	fake.mu.Lock()
	fake.users["owner@example.com"] = "changed"
	fake.mu.Unlock()

	_, err := (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: state.BootstrapArgs, State: state})
	if err == nil {
		t.Fatal("a rejected login must fail the refresh instead of dropping the state")
	}
}

func TestBootstrapReadWithoutTokenReportsMissing(t *testing.T) {
	fake := newFakeGlitchTip(t)
	read, err := (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com"})
	if err != nil || read.ID != "" {
		t.Fatalf("expected a missing resource, got %+v %v", read, err)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("no request expected, got %v", fake.requests)
	}
}

func TestBootstrapUpdateRepairsRevokedToken(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	fake.mu.Lock()
	fake.tokens = nil
	fake.mu.Unlock()
	state.RepairRequired = true

	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: state.BootstrapArgs, State: state})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Output.Token == state.Token || updated.Output.TokenID == state.TokenID || updated.Output.RepairRequired {
		t.Fatalf("expected a new token, got %+v", updated.Output)
	}
}

func TestBootstrapUpdateKeepsTokenWhenRepairFindsIt(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	state.RepairRequired = true // e.g. the previous refresh could not reach the host

	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: state.BootstrapArgs, State: state})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Output.Token != state.Token || fake.countRequests("POST", "/api/0/api-tokens/") != 1 {
		t.Fatalf("existing token must be kept, got %+v %v", updated.Output, fake.requests)
	}
}

func TestBootstrapUpdateRotatesOnScopeChange(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	inputs := state.BootstrapArgs
	inputs.Scopes = []string{"project:read"}

	diff, err := (Bootstrap{}).Diff(context.Background(), infer.DiffRequest[BootstrapArgs, BootstrapState]{Inputs: inputs, State: state})
	if err != nil || !diff.HasChanges || diff.DetailedDiff["scopes"].Kind != p.Update {
		t.Fatalf("scope change must be an in-place update: %+v %v", diff, err)
	}
	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: "owner@example.com", Inputs: inputs, State: state})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tokens) != 1 || fake.tokens[0].Token != updated.Output.Token || strings.Join(fake.tokens[0].Scopes, ",") != "project:read" {
		t.Fatalf("expected the old token revoked and a new one issued, got %+v", fake.tokens)
	}
}

func TestBootstrapDiffReplacesOnEmail(t *testing.T) {
	state := BootstrapState{BootstrapArgs: bootstrapArgs("https://glitchtip.example.com"), TokenID: "1", Token: "tok-1"}
	inputs := state.BootstrapArgs
	inputs.Email = "other@example.com"
	diff, err := (Bootstrap{}).Diff(context.Background(), infer.DiffRequest[BootstrapArgs, BootstrapState]{Inputs: inputs, State: state})
	if err != nil || diff.DetailedDiff["email"].Kind != p.UpdateReplace {
		t.Fatalf("email change must replace: %+v %v", diff, err)
	}
	same, err := (Bootstrap{}).Diff(context.Background(), infer.DiffRequest[BootstrapArgs, BootstrapState]{Inputs: state.BootstrapArgs, State: state})
	if err != nil || same.HasChanges {
		t.Fatalf("unchanged inputs must not diff: %+v %v", same, err)
	}
}

func TestBootstrapDeleteRevokesToken(t *testing.T) {
	fake := newFakeGlitchTip(t)
	state := createdBootstrap(t, fake)
	if _, err := (Bootstrap{}).Delete(context.Background(), infer.DeleteRequest[BootstrapState]{ID: "owner@example.com", State: state}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tokens) != 0 {
		t.Fatalf("token must be revoked, got %+v", fake.tokens)
	}
}
