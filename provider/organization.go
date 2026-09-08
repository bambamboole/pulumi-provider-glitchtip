package provider

import (
	"context"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// Organization manages a GlitchTip organization.
type Organization struct{}

type OrganizationArgs struct {
	Name string `pulumi:"name"`
}

type OrganizationState struct {
	OrganizationArgs
	OrganizationID string `pulumi:"organizationId"`
	Slug           string `pulumi:"slug"`
}

func (r *Organization) Annotate(a infer.Annotator) {
	a.SetToken("index", "Organization")
	a.Describe(&r, "A GlitchTip organization. GlitchTip derives the slug from the name on creation and cannot change it afterwards; renaming keeps the slug. The resource ID is the slug, so an existing organization is imported by its slug. Deleting the resource deletes the organization with every team and project in it.")
}

func (args *OrganizationArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.Name, "Display name of the organization.")
}

func (state *OrganizationState) Annotate(a infer.Annotator) {
	a.Describe(&state.OrganizationID, "Numeric ID of the organization.")
	a.Describe(&state.Slug, "URL slug of the organization, derived from the name on creation.")
}

func (Organization) Create(ctx context.Context, req infer.CreateRequest[OrganizationArgs]) (infer.CreateResponse[OrganizationState], error) {
	state := OrganizationState{OrganizationArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[OrganizationState]{Output: state}, nil
	}
	organization, err := client(ctx).CreateOrganization(ctx, req.Inputs.Name)
	if err != nil {
		return infer.CreateResponse[OrganizationState]{}, err
	}
	state.OrganizationID, state.Slug = organization.ID, organization.Slug
	return infer.CreateResponse[OrganizationState]{ID: organization.Slug, Output: state}, nil
}

func (Organization) Read(ctx context.Context, req infer.ReadRequest[OrganizationArgs, OrganizationState]) (infer.ReadResponse[OrganizationArgs, OrganizationState], error) {
	organization, err := client(ctx).GetOrganization(ctx, req.ID)
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[OrganizationArgs, OrganizationState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[OrganizationArgs, OrganizationState]{}, err
	}
	inputs := OrganizationArgs{Name: organization.Name}
	return infer.ReadResponse[OrganizationArgs, OrganizationState]{
		ID:     req.ID,
		Inputs: inputs,
		State:  OrganizationState{OrganizationArgs: inputs, OrganizationID: organization.ID, Slug: organization.Slug},
	}, nil
}

func (Organization) Diff(_ context.Context, req infer.DiffRequest[OrganizationArgs, OrganizationState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.OrganizationArgs, req.Inputs)), nil
}

func (Organization) Update(ctx context.Context, req infer.UpdateRequest[OrganizationArgs, OrganizationState]) (infer.UpdateResponse[OrganizationState], error) {
	state := OrganizationState{OrganizationArgs: req.Inputs, OrganizationID: req.State.OrganizationID, Slug: req.State.Slug}
	if req.DryRun {
		return infer.UpdateResponse[OrganizationState]{Output: state}, nil
	}
	organization, err := client(ctx).UpdateOrganization(ctx, req.ID, req.Inputs.Name)
	if err != nil {
		return infer.UpdateResponse[OrganizationState]{}, err
	}
	state.OrganizationID, state.Slug = organization.ID, organization.Slug
	return infer.UpdateResponse[OrganizationState]{Output: state}, nil
}

func (Organization) Delete(ctx context.Context, req infer.DeleteRequest[OrganizationState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteOrganization(ctx, req.ID); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}
