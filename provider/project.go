package provider

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// Project manages a GlitchTip project, the team that owns it and exposes the
// DSN of the key GlitchTip creates with the project.
type Project struct{}

type ProjectArgs struct {
	OrganizationSlug  string `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	TeamSlug          string `pulumi:"teamSlug"`
	Name              string `pulumi:"name"`
	Slug              string `pulumi:"slug,optional" provider:"replaceOnChanges"`
	Platform          string `pulumi:"platform,optional"`
	EventThrottleRate int    `pulumi:"eventThrottleRate,optional"`
}

type ProjectState struct {
	ProjectArgs
	ProjectID    string `pulumi:"projectId"`
	ProjectSlug  string `pulumi:"projectSlug"`
	DefaultKeyID string `pulumi:"defaultKeyId"`
	Dsn          string `pulumi:"dsn" provider:"secret"`
	CspReportURI string `pulumi:"cspReportUri" provider:"secret"`
}

func (r *Project) Annotate(a infer.Annotator) {
	a.SetToken("index", "Project")
	a.Describe(&r, "A GlitchTip project owned by a team. GlitchTip creates one DSN key with every project; its DSN and CSP report URI are exposed as dsn and cspReportUri, additional keys are ProjectKey resources. The resource ID is <organizationSlug>/<projectSlug>. Changing the team adds the new team and removes the old one in place; changing the organization or the slug replaces the project, which deletes its events. Deleting the resource deletes the project.")
}

func (args *ProjectArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization the project belongs to.")
	a.Describe(&args.TeamSlug, "Slug of the team that owns the project. A refresh reports the first attached team when the declared one was detached.")
	a.Describe(&args.Name, "Display name of the project.")
	a.Describe(&args.Slug, "URL slug of the project. Derived from the name by GlitchTip when unset; see projectSlug for the effective value.")
	a.Describe(&args.Platform, "Platform identifier shown in the UI, e.g. javascript or python. Unset clears it.")
	a.Describe(&args.EventThrottleRate, "Percentage of events to drop for throttling. Defaults to 0.")
}

func (state *ProjectState) Annotate(a infer.Annotator) {
	a.Describe(&state.ProjectID, "Numeric ID of the project.")
	a.Describe(&state.ProjectSlug, "Effective URL slug of the project.")
	a.Describe(&state.DefaultKeyID, "ID of the key GlitchTip created with the project. Empty when that key was deleted and another one could not be adopted unambiguously.")
	a.Describe(&state.Dsn, "Public DSN of the default key. Empty when defaultKeyId is empty.")
	a.Describe(&state.CspReportURI, "Security endpoint of the default key for Content-Security-Policy report-uri and Expect-CT reports. Empty when defaultKeyId is empty.")
}

func (Project) Create(ctx context.Context, req infer.CreateRequest[ProjectArgs]) (infer.CreateResponse[ProjectState], error) {
	state := ProjectState{ProjectArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[ProjectState]{Output: state}, nil
	}
	c := client(ctx)
	project, err := c.CreateProject(ctx, req.Inputs.OrganizationSlug, req.Inputs.TeamSlug, glitchtip.ProjectInput{
		Name:              req.Inputs.Name,
		Slug:              optional(req.Inputs.Slug),
		Platform:          optional(req.Inputs.Platform),
		EventThrottleRate: req.Inputs.EventThrottleRate,
	})
	if err != nil {
		return infer.CreateResponse[ProjectState]{}, err
	}
	state.ProjectID, state.ProjectSlug = project.ID, project.Slug
	keys, err := c.ListProjectKeys(ctx, req.Inputs.OrganizationSlug, project.Slug)
	if err != nil {
		return infer.CreateResponse[ProjectState]{}, err
	}
	state.DefaultKeyID, state.Dsn, state.CspReportURI = defaultKey(keys, "")
	return infer.CreateResponse[ProjectState]{ID: req.Inputs.OrganizationSlug + "/" + project.Slug, Output: state}, nil
}

func (Project) Read(ctx context.Context, req infer.ReadRequest[ProjectArgs, ProjectState]) (infer.ReadResponse[ProjectArgs, ProjectState], error) {
	parts, err := splitID(req.ID, 2, "<organizationSlug>/<projectSlug>")
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	c := client(ctx)
	project, err := c.GetProject(ctx, parts[0], parts[1])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	teams, err := c.ListProjectTeams(ctx, parts[0], project.Slug)
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	keys, err := c.ListProjectKeys(ctx, parts[0], project.Slug)
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	inputs := ProjectArgs{
		OrganizationSlug:  parts[0],
		TeamSlug:          attachedTeam(teams, req.Inputs.TeamSlug),
		Name:              project.Name,
		Slug:              req.Inputs.Slug,
		Platform:          deref(project.Platform),
		EventThrottleRate: project.EventThrottleRate,
	}
	if inputs.Slug != "" || req.Inputs.Name == "" {
		// Keep the slug managed when it was declared, and adopt it on import.
		inputs.Slug = project.Slug
	}
	state := ProjectState{ProjectArgs: inputs, ProjectID: project.ID, ProjectSlug: project.Slug}
	state.DefaultKeyID, state.Dsn, state.CspReportURI = defaultKey(keys, req.State.DefaultKeyID)
	return infer.ReadResponse[ProjectArgs, ProjectState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

func (Project) Diff(_ context.Context, req infer.DiffRequest[ProjectArgs, ProjectState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.ProjectArgs, req.Inputs, "organizationSlug", "slug")), nil
}

func (Project) Update(ctx context.Context, req infer.UpdateRequest[ProjectArgs, ProjectState]) (infer.UpdateResponse[ProjectState], error) {
	state := ProjectState{
		ProjectArgs:  req.Inputs,
		ProjectID:    req.State.ProjectID,
		ProjectSlug:  req.State.ProjectSlug,
		DefaultKeyID: req.State.DefaultKeyID,
		Dsn:          req.State.Dsn,
		CspReportURI: req.State.CspReportURI,
	}
	if req.DryRun {
		return infer.UpdateResponse[ProjectState]{Output: state}, nil
	}
	c := client(ctx)
	organization, slug := req.Inputs.OrganizationSlug, req.State.ProjectSlug
	project, err := c.UpdateProject(ctx, organization, slug, glitchtip.ProjectInput{
		Name:              req.Inputs.Name,
		Slug:              &slug,
		Platform:          optional(req.Inputs.Platform),
		EventThrottleRate: req.Inputs.EventThrottleRate,
	})
	if err != nil {
		return infer.UpdateResponse[ProjectState]{}, err
	}
	state.ProjectID, state.ProjectSlug = project.ID, project.Slug
	if req.Inputs.TeamSlug != req.State.TeamSlug {
		if err := c.AddProjectTeam(ctx, organization, slug, req.Inputs.TeamSlug); err != nil {
			return infer.UpdateResponse[ProjectState]{}, fmt.Errorf("glitchtip: attaching team %q: %w", req.Inputs.TeamSlug, err)
		}
		if req.State.TeamSlug != "" {
			if err := c.RemoveProjectTeam(ctx, organization, slug, req.State.TeamSlug); err != nil && !glitchtip.IsNotFound(err) {
				return infer.UpdateResponse[ProjectState]{}, fmt.Errorf("glitchtip: detaching team %q: %w", req.State.TeamSlug, err)
			}
		}
	}
	return infer.UpdateResponse[ProjectState]{Output: state}, nil
}

func (Project) Delete(ctx context.Context, req infer.DeleteRequest[ProjectState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteProject(ctx, req.State.OrganizationSlug, req.State.ProjectSlug); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

// attachedTeam keeps the declared team while it is attached and otherwise
// reports the first attached team, so the diff re-attaches the declared one.
func attachedTeam(teams []glitchtip.Team, declared string) string {
	for _, team := range teams {
		if team.Slug == declared {
			return declared
		}
	}
	if len(teams) > 0 {
		return teams[0].Slug
	}
	return ""
}

// defaultKey returns the ID, public DSN and security endpoint of the tracked
// key, or of the only key when none is tracked yet or the tracked one is gone.
func defaultKey(keys []glitchtip.ProjectKey, tracked string) (id, dsn, cspReportURI string) {
	for _, key := range keys {
		if key.ID == tracked {
			return key.ID, key.DSN["public"], key.DSN["security"]
		}
	}
	if len(keys) == 1 {
		return keys[0].ID, keys[0].DSN["public"], keys[0].DSN["security"]
	}
	return "", "", ""
}
