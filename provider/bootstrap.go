package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

const (
	defaultBootstrapTokenLabel   = "pulumi"
	defaultBootstrapReadyTimeout = 180
)

// Variables so tests can shorten the waits.
var (
	// bootstrapRefreshTimeout bounds the readiness wait of a refresh. A refresh
	// asks the host recorded in state, which is gone after a hostname change;
	// it must not wait out the whole deployment timeout.
	bootstrapRefreshTimeout = 15 * time.Second
	bootstrapPollInterval   = 3 * time.Second
)

// defaultBootstrapScopes are the scope flags of GlitchTip's apps/api_tokens
// model, which grant an owner everything the other resources need.
var defaultBootstrapScopes = []string{
	"project:read", "project:write", "project:admin", "project:releases",
	"team:read", "team:write", "team:admin",
	"event:read", "event:write", "event:admin",
	"org:read", "org:write", "org:admin",
	"member:read", "member:write", "member:admin",
}

// Bootstrap issues an API token by logging in with the email and password of
// a GlitchTip user, typically the owner the container created from
// INITIAL_USER_EMAIL and INITIAL_USER_PASSWORD. The token feeds a second
// provider instance that manages everything else.
type Bootstrap struct{}

type BootstrapArgs struct {
	BaseURL             string   `pulumi:"baseUrl,optional"`
	Email               string   `pulumi:"email" provider:"replaceOnChanges"`
	Password            string   `pulumi:"password" provider:"secret"`
	TokenLabel          string   `pulumi:"tokenLabel,optional"`
	Scopes              []string `pulumi:"scopes,optional"`
	ReadyTimeoutSeconds int      `pulumi:"readyTimeoutSeconds,optional"`
}

type BootstrapState struct {
	BootstrapArgs
	TokenID        string `pulumi:"tokenId"`
	Token          string `pulumi:"token" provider:"secret"`
	RepairRequired bool   `pulumi:"repairRequired,optional"`
}

func (r *Bootstrap) Annotate(a infer.Annotator) {
	a.SetToken("index", "Bootstrap")
	a.Describe(&r, "Issues an API token of a GlitchTip user without user interaction, for use as the apiToken of a second provider instance. The resource logs in with the user's email and password through the same endpoints as the web UI, waits for a freshly deployed instance to answer, and adopts an existing token with the same label and scopes before issuing a new one. A refresh that finds the token revoked, or the recorded host unreachable, marks the resource for repair, and the next update issues a replacement token against the declared host. Deleting the resource revokes the token and keeps the user. The resource ID is the email.")
}

func (args *BootstrapArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.BaseURL, "Origin of the GlitchTip instance. Defaults to the provider's base URL.")
	a.Describe(&args.Email, "Email of the user whose token is issued. Changing it replaces the resource.")
	a.Describe(&args.Password, "Password of the user. The resource never changes it; keep it in sync with the deployed INITIAL_USER_PASSWORD.")
	a.Describe(&args.TokenLabel, "Label of the API token. Changing it rotates the token. Defaults to \"pulumi\".")
	a.Describe(&args.Scopes, "Scopes of the API token, e.g. project:read. Changing them rotates the token. Defaults to every scope GlitchTip offers.")
	a.Describe(&args.ReadyTimeoutSeconds, "Seconds to wait for the instance to answer before giving up on create and update. Defaults to 180.")
	a.SetDefault(&args.TokenLabel, defaultBootstrapTokenLabel)
	a.SetDefault(&args.ReadyTimeoutSeconds, defaultBootstrapReadyTimeout)
}

func (state *BootstrapState) Annotate(a infer.Annotator) {
	a.Describe(&state.TokenID, "ID of the issued API token.")
	a.Describe(&state.Token, "The issued API token. Use it as the apiToken of a second provider instance.")
	a.Describe(&state.RepairRequired, "True after a refresh found the token revoked or the recorded host unreachable. The next update issues a replacement token.")
}

func (Bootstrap) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[BootstrapArgs], error) {
	args, failures, err := infer.DefaultCheck[BootstrapArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[BootstrapArgs]{}, err
	}
	if args.TokenLabel == "" {
		args.TokenLabel = defaultBootstrapTokenLabel
	}
	if args.ReadyTimeoutSeconds <= 0 {
		args.ReadyTimeoutSeconds = defaultBootstrapReadyTimeout
	}
	if len(args.Scopes) == 0 {
		args.Scopes = slices.Clone(defaultBootstrapScopes)
	}
	args.Scopes = normalizeScopes(args.Scopes)
	return infer.CheckResponse[BootstrapArgs]{Inputs: args, Failures: failures}, nil
}

func (Bootstrap) Create(ctx context.Context, req infer.CreateRequest[BootstrapArgs]) (infer.CreateResponse[BootstrapState], error) {
	state := BootstrapState{BootstrapArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[BootstrapState]{Output: state}, nil
	}
	session, err := bootstrapLogin(ctx, req.Inputs, time.Duration(req.Inputs.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.CreateResponse[BootstrapState]{}, err
	}
	tokens, err := session.ListAPITokens(ctx)
	if err != nil {
		return infer.CreateResponse[BootstrapState]{}, err
	}
	token, adopted := adoptToken(tokens, req.Inputs)
	if !adopted {
		token, err = session.CreateAPIToken(ctx, req.Inputs.TokenLabel, req.Inputs.Scopes)
		if err != nil {
			return infer.CreateResponse[BootstrapState]{}, fmt.Errorf("glitchtip: issuing the API token: %w", err)
		}
	}
	state.TokenID, state.Token = fmt.Sprint(token.ID), token.Token
	return infer.CreateResponse[BootstrapState]{ID: req.Inputs.Email, Output: state}, nil
}

// Read verifies the token still exists by logging in with the password. An
// unreachable host or a revoked token marks the resource for repair; a rejected
// login fails the refresh so a changed password never erases the state.
func (Bootstrap) Read(ctx context.Context, req infer.ReadRequest[BootstrapArgs, BootstrapState]) (infer.ReadResponse[BootstrapArgs, BootstrapState], error) {
	state := req.State
	if state.Token == "" {
		return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, nil
	}
	inputs := req.Inputs
	if inputs.Email == "" {
		inputs = state.BootstrapArgs
	}
	session, err := bootstrapLogin(ctx, state.BootstrapArgs, bootstrapRefreshTimeout)
	switch {
	case errors.Is(err, glitchtip.ErrNotReady):
		state.RepairRequired = true
	case err != nil:
		return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, err
	default:
		tokens, err := session.ListAPITokens(ctx)
		if err != nil {
			return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, err
		}
		state.RepairRequired = true
		for _, token := range tokens {
			if fmt.Sprint(token.ID) == state.TokenID && token.Token == state.Token {
				state.RepairRequired = false
			}
		}
	}
	state.BootstrapArgs = inputs
	return infer.ReadResponse[BootstrapArgs, BootstrapState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

// Diff compares the inputs and also schedules an update when a refresh marked
// the token for repair.
func (Bootstrap) Diff(_ context.Context, req infer.DiffRequest[BootstrapArgs, BootstrapState]) (infer.DiffResponse, error) {
	diff := diffResponse(diffArgs(req.State.BootstrapArgs, req.Inputs, "email"))
	diff.HasChanges = diff.HasChanges || req.State.RepairRequired
	return diff, nil
}

func (Bootstrap) Update(ctx context.Context, req infer.UpdateRequest[BootstrapArgs, BootstrapState]) (infer.UpdateResponse[BootstrapState], error) {
	state := BootstrapState{BootstrapArgs: req.Inputs, TokenID: req.State.TokenID, Token: req.State.Token}
	if req.DryRun {
		return infer.UpdateResponse[BootstrapState]{Output: state}, nil
	}
	session, err := bootstrapLogin(ctx, req.Inputs, time.Duration(req.Inputs.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.UpdateResponse[BootstrapState]{}, err
	}
	rotate := req.State.RepairRequired || req.State.TokenLabel != req.Inputs.TokenLabel || !slices.Equal(req.State.Scopes, req.Inputs.Scopes)
	if !rotate {
		return infer.UpdateResponse[BootstrapState]{Output: state}, nil
	}
	tokens, err := session.ListAPITokens(ctx)
	if err != nil {
		return infer.UpdateResponse[BootstrapState]{}, err
	}
	// A repair keeps the token when the instance still knows it, e.g. after
	// a refresh that could not reach the old host.
	if req.State.RepairRequired && req.State.TokenLabel == req.Inputs.TokenLabel && slices.Equal(req.State.Scopes, req.Inputs.Scopes) {
		for _, token := range tokens {
			if fmt.Sprint(token.ID) == req.State.TokenID && token.Token == req.State.Token {
				return infer.UpdateResponse[BootstrapState]{Output: state}, nil
			}
		}
	}
	token, err := session.CreateAPIToken(ctx, req.Inputs.TokenLabel, req.Inputs.Scopes)
	if err != nil {
		return infer.UpdateResponse[BootstrapState]{}, fmt.Errorf("glitchtip: reissuing the API token: %w", err)
	}
	state.TokenID, state.Token = fmt.Sprint(token.ID), token.Token
	if err := revokeToken(ctx, session, tokens, req.State.TokenID); err != nil {
		return infer.UpdateResponse[BootstrapState]{}, err
	}
	return infer.UpdateResponse[BootstrapState]{Output: state}, nil
}

// Delete revokes the token. The user is kept; it is the instance owner.
func (Bootstrap) Delete(ctx context.Context, req infer.DeleteRequest[BootstrapState]) (infer.DeleteResponse, error) {
	if req.State.TokenID == "" {
		return infer.DeleteResponse{}, nil
	}
	session, err := bootstrapLogin(ctx, req.State.BootstrapArgs, time.Duration(req.State.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	tokens, err := session.ListAPITokens(ctx)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, revokeToken(ctx, session, tokens, req.State.TokenID)
}

// bootstrapLogin waits for the instance and opens a session for the user.
func bootstrapLogin(ctx context.Context, args BootstrapArgs, timeout time.Duration) (*glitchtip.Client, error) {
	baseURL := args.BaseURL
	if baseURL == "" {
		baseURL = client(ctx).BaseURL()
	}
	session, err := glitchtip.NewAnonymous(baseURL)
	if err != nil {
		return nil, err
	}
	if err := session.WaitReady(ctx, timeout, bootstrapPollInterval); err != nil {
		return nil, err
	}
	if err := session.Login(ctx, args.Email, args.Password); err != nil {
		return nil, fmt.Errorf("glitchtip: login as %q failed: %w", args.Email, err)
	}
	return session, nil
}

// adoptToken picks the one existing token matching label and scopes, so a
// recreated resource reuses the token an earlier run issued.
func adoptToken(tokens []glitchtip.APIToken, args BootstrapArgs) (glitchtip.APIToken, bool) {
	var matches []glitchtip.APIToken
	for _, token := range tokens {
		if token.Label == args.TokenLabel && slices.Equal(normalizeScopes(token.Scopes), args.Scopes) {
			matches = append(matches, token)
		}
	}
	if len(matches) != 1 {
		return glitchtip.APIToken{}, false
	}
	return matches[0], true
}

// revokeToken deletes the token with the given ID when the instance still
// lists it.
func revokeToken(ctx context.Context, session *glitchtip.Client, tokens []glitchtip.APIToken, id string) error {
	for _, token := range tokens {
		if fmt.Sprint(token.ID) != id {
			continue
		}
		if err := session.DeleteAPIToken(ctx, token.ID); err != nil && !glitchtip.IsNotFound(err) {
			return fmt.Errorf("glitchtip: revoking the API token: %w", err)
		}
	}
	return nil
}

func normalizeScopes(scopes []string) []string {
	normalized := slices.Clone(scopes)
	slices.Sort(normalized)
	return slices.Compact(normalized)
}
