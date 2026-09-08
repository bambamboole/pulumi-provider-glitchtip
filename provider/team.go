package provider

import (
	"context"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// Team manages a team of a GlitchTip organization.
type Team struct{}

type TeamArgs struct {
	OrganizationSlug string `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	Slug             string `pulumi:"slug" provider:"replaceOnChanges"`
}

type TeamState struct {
	TeamArgs
	TeamID string `pulumi:"teamId"`
}

func (r *Team) Annotate(a infer.Annotator) {
	a.SetToken("index", "Team")
	a.Describe(&r, "A team of a GlitchTip organization. Teams grant their members access to projects. The resource ID is <organizationSlug>/<slug>; changing either replaces the team, which drops its memberships and project assignments.")
}

func (args *TeamArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization the team belongs to.")
	a.Describe(&args.Slug, "Slug of the team: letters, digits, hyphens and underscores.")
}

func (state *TeamState) Annotate(a infer.Annotator) {
	a.Describe(&state.TeamID, "Numeric ID of the team.")
}

func (Team) Create(ctx context.Context, req infer.CreateRequest[TeamArgs]) (infer.CreateResponse[TeamState], error) {
	state := TeamState{TeamArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[TeamState]{Output: state}, nil
	}
	team, err := client(ctx).CreateTeam(ctx, req.Inputs.OrganizationSlug, req.Inputs.Slug)
	if err != nil {
		return infer.CreateResponse[TeamState]{}, err
	}
	state.TeamID = team.ID
	return infer.CreateResponse[TeamState]{ID: req.Inputs.OrganizationSlug + "/" + team.Slug, Output: state}, nil
}

func (Team) Read(ctx context.Context, req infer.ReadRequest[TeamArgs, TeamState]) (infer.ReadResponse[TeamArgs, TeamState], error) {
	parts, err := splitID(req.ID, 2, "<organizationSlug>/<teamSlug>")
	if err != nil {
		return infer.ReadResponse[TeamArgs, TeamState]{}, err
	}
	team, err := client(ctx).GetTeam(ctx, parts[0], parts[1])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[TeamArgs, TeamState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[TeamArgs, TeamState]{}, err
	}
	inputs := TeamArgs{OrganizationSlug: parts[0], Slug: team.Slug}
	return infer.ReadResponse[TeamArgs, TeamState]{
		ID:     req.ID,
		Inputs: inputs,
		State:  TeamState{TeamArgs: inputs, TeamID: team.ID},
	}, nil
}

func (Team) Delete(ctx context.Context, req infer.DeleteRequest[TeamState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteTeam(ctx, req.State.OrganizationSlug, req.State.Slug); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}
