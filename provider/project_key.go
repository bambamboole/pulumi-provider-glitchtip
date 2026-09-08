package provider

import (
	"context"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// ProjectKey manages an additional DSN key of a project.
type ProjectKey struct{}

// KeyRateLimit throttles a key to count events per window seconds.
type KeyRateLimit struct {
	Window int `pulumi:"window"`
	Count  int `pulumi:"count"`
}

func (limit *KeyRateLimit) Annotate(a infer.Annotator) {
	a.Describe(&limit.Window, "Length of the rate limit window in seconds.")
	a.Describe(&limit.Count, "Maximum number of events accepted per window.")
}

type ProjectKeyArgs struct {
	OrganizationSlug string        `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	ProjectSlug      string        `pulumi:"projectSlug" provider:"replaceOnChanges"`
	Name             string        `pulumi:"name,optional"`
	RateLimit        *KeyRateLimit `pulumi:"rateLimit,optional"`
}

type ProjectKeyState struct {
	ProjectKeyArgs
	KeyID        string `pulumi:"keyId"`
	Public       string `pulumi:"public"`
	Dsn          string `pulumi:"dsn" provider:"secret"`
	CspReportURI string `pulumi:"cspReportUri" provider:"secret"`
}

func (r *ProjectKey) Annotate(a infer.Annotator) {
	a.SetToken("index", "ProjectKey")
	a.Describe(&r, "A DSN key of a GlitchTip project, in addition to the key GlitchTip creates with the project (see Project.dsn). The resource ID is <organizationSlug>/<projectSlug>/<keyId>. Deleting the resource revokes the key, so clients using its DSN stop reporting.")
}

func (args *ProjectKeyArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization the project belongs to.")
	a.Describe(&args.ProjectSlug, "Slug of the project the key belongs to.")
	a.Describe(&args.Name, "Display name of the key.")
	a.Describe(&args.RateLimit, "Rate limit of the key. Unset accepts every event.")
}

func (state *ProjectKeyState) Annotate(a infer.Annotator) {
	a.Describe(&state.KeyID, "UUID of the key.")
	a.Describe(&state.Public, "Public key part of the DSN.")
	a.Describe(&state.Dsn, "Public DSN clients report to.")
	a.Describe(&state.CspReportURI, "Security endpoint of the key for Content-Security-Policy report-uri and Expect-CT reports.")
}

func (ProjectKey) Create(ctx context.Context, req infer.CreateRequest[ProjectKeyArgs]) (infer.CreateResponse[ProjectKeyState], error) {
	state := ProjectKeyState{ProjectKeyArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[ProjectKeyState]{Output: state}, nil
	}
	key, err := client(ctx).CreateProjectKey(ctx, req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, keyInput(req.Inputs))
	if err != nil {
		return infer.CreateResponse[ProjectKeyState]{}, err
	}
	state.KeyID, state.Public, state.Dsn, state.CspReportURI = key.ID, key.Public, key.DSN["public"], key.DSN["security"]
	return infer.CreateResponse[ProjectKeyState]{ID: keyID(req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, key.ID), Output: state}, nil
}

func (ProjectKey) Read(ctx context.Context, req infer.ReadRequest[ProjectKeyArgs, ProjectKeyState]) (infer.ReadResponse[ProjectKeyArgs, ProjectKeyState], error) {
	parts, err := splitID(req.ID, 3, "<organizationSlug>/<projectSlug>/<keyId>")
	if err != nil {
		return infer.ReadResponse[ProjectKeyArgs, ProjectKeyState]{}, err
	}
	key, err := client(ctx).GetProjectKey(ctx, parts[0], parts[1], parts[2])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[ProjectKeyArgs, ProjectKeyState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[ProjectKeyArgs, ProjectKeyState]{}, err
	}
	inputs := ProjectKeyArgs{OrganizationSlug: parts[0], ProjectSlug: parts[1], Name: deref(key.Name)}
	if key.RateLimit != nil {
		inputs.RateLimit = &KeyRateLimit{Window: key.RateLimit.Window, Count: key.RateLimit.Count}
	}
	return infer.ReadResponse[ProjectKeyArgs, ProjectKeyState]{
		ID:     req.ID,
		Inputs: inputs,
		State:  ProjectKeyState{ProjectKeyArgs: inputs, KeyID: key.ID, Public: key.Public, Dsn: key.DSN["public"], CspReportURI: key.DSN["security"]},
	}, nil
}

func (ProjectKey) Diff(_ context.Context, req infer.DiffRequest[ProjectKeyArgs, ProjectKeyState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.ProjectKeyArgs, req.Inputs, "organizationSlug", "projectSlug")), nil
}

func (ProjectKey) Update(ctx context.Context, req infer.UpdateRequest[ProjectKeyArgs, ProjectKeyState]) (infer.UpdateResponse[ProjectKeyState], error) {
	state := ProjectKeyState{ProjectKeyArgs: req.Inputs, KeyID: req.State.KeyID, Public: req.State.Public, Dsn: req.State.Dsn, CspReportURI: req.State.CspReportURI}
	if req.DryRun {
		return infer.UpdateResponse[ProjectKeyState]{Output: state}, nil
	}
	key, err := client(ctx).UpdateProjectKey(ctx, req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, req.State.KeyID, keyInput(req.Inputs))
	if err != nil {
		return infer.UpdateResponse[ProjectKeyState]{}, err
	}
	state.Public, state.Dsn, state.CspReportURI = key.Public, key.DSN["public"], key.DSN["security"]
	return infer.UpdateResponse[ProjectKeyState]{Output: state}, nil
}

func (ProjectKey) Delete(ctx context.Context, req infer.DeleteRequest[ProjectKeyState]) (infer.DeleteResponse, error) {
	err := client(ctx).DeleteProjectKey(ctx, req.State.OrganizationSlug, req.State.ProjectSlug, req.State.KeyID)
	if err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func keyID(organization, project, key string) string {
	return organization + "/" + project + "/" + key
}

func keyInput(args ProjectKeyArgs) glitchtip.ProjectKeyInput {
	input := glitchtip.ProjectKeyInput{Name: optional(args.Name)}
	if args.RateLimit != nil {
		input.RateLimit = &glitchtip.RateLimit{Window: args.RateLimit.Window, Count: args.RateLimit.Count}
	}
	return input
}
