package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// MemberRole is the organization role of a member.
type MemberRole string

const (
	MemberRoleMember  MemberRole = "member"
	MemberRoleAdmin   MemberRole = "admin"
	MemberRoleManager MemberRole = "manager"
	MemberRoleOwner   MemberRole = "owner"
)

func (MemberRole) Values() []infer.EnumValue[MemberRole] {
	return []infer.EnumValue[MemberRole]{
		{Name: "Member", Value: MemberRoleMember, Description: "Sees the projects of their teams."},
		{Name: "Admin", Value: MemberRoleAdmin, Description: "Manages the projects and teams of the organization."},
		{Name: "Manager", Value: MemberRoleManager, Description: "Admin who also invites and removes members."},
		{Name: "Owner", Value: MemberRoleOwner, Description: "Full control including deleting the organization."},
	}
}

// OrganizationMember manages a member of an organization: the invitation, the
// organization role and the team memberships.
type OrganizationMember struct{}

type OrganizationMemberArgs struct {
	OrganizationSlug string      `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	Email            string      `pulumi:"email" provider:"replaceOnChanges"`
	Role             *MemberRole `pulumi:"role,optional"`
	Teams            []string    `pulumi:"teams,optional"`
}

type OrganizationMemberState struct {
	OrganizationMemberArgs
	MemberID string `pulumi:"memberId"`
	Pending  bool   `pulumi:"pending"`
}

func (r *OrganizationMember) Annotate(a infer.Annotator) {
	a.SetToken("index", "OrganizationMember")
	a.Describe(&r, "A member of a GlitchTip organization. Creating the resource invites the email address with the role and team memberships and sends the invitation mail; an existing member or pending invitation with that email is adopted and brought to the declared role and teams. With ENABLE_USER_REGISTRATION off, GlitchTip only invites emails of users that already exist on the instance. The resource ID is <organizationSlug>/<memberId>. Deleting the resource removes the member from the organization; GlitchTip refuses to remove the organization owner and the last member.")
}

func (args *OrganizationMemberArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization.")
	a.Describe(&args.Email, "Email address of the member. Changing it replaces the resource.")
	a.Describe(&args.Role, "Organization role: member, admin, manager or owner. Defaults to member. GlitchTip keeps at least one owner.")
	a.Describe(&args.Teams, "Slugs of the teams the member belongs to. Teams not listed are left, so the list is authoritative.")
}

func (state *OrganizationMemberState) Annotate(a infer.Annotator) {
	a.Describe(&state.MemberID, "Numeric ID of the membership.")
	a.Describe(&state.Pending, "True while the invitation has not been accepted.")
}

func (OrganizationMember) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[OrganizationMemberArgs], error) {
	args, failures, err := infer.DefaultCheck[OrganizationMemberArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[OrganizationMemberArgs]{}, err
	}
	if args.Role == nil || *args.Role == "" {
		role := MemberRoleMember
		args.Role = &role
	}
	args.Email = strings.ToLower(strings.TrimSpace(args.Email))
	args.Teams = normalizeScopes(args.Teams)
	return infer.CheckResponse[OrganizationMemberArgs]{Inputs: args, Failures: failures}, nil
}

func (OrganizationMember) Create(ctx context.Context, req infer.CreateRequest[OrganizationMemberArgs]) (infer.CreateResponse[OrganizationMemberState], error) {
	state := OrganizationMemberState{OrganizationMemberArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[OrganizationMemberState]{Output: state}, nil
	}
	c := client(ctx)
	teamRoles := make([]glitchtip.TeamRole, 0, len(req.Inputs.Teams))
	for _, team := range req.Inputs.Teams {
		teamRoles = append(teamRoles, glitchtip.TeamRole{TeamSlug: team})
	}
	member, err := c.InviteMember(ctx, req.Inputs.OrganizationSlug, glitchtip.MemberInvite{
		Email: req.Inputs.Email, OrgRole: string(*req.Inputs.Role), TeamRoles: teamRoles, Reinvite: true,
	})
	switch {
	case glitchtip.IsConflict(err):
		// Already a member: adopt and reconcile role and teams below.
		member, err = findMember(ctx, c, req.Inputs.OrganizationSlug, req.Inputs.Email)
		if err != nil {
			return infer.CreateResponse[OrganizationMemberState]{}, err
		}
	case err != nil:
		return infer.CreateResponse[OrganizationMemberState]{}, err
	}
	member, err = c.GetMember(ctx, req.Inputs.OrganizationSlug, member.ID)
	if err != nil {
		return infer.CreateResponse[OrganizationMemberState]{}, err
	}
	if err := reconcileMember(ctx, c, req.Inputs, member); err != nil {
		return infer.CreateResponse[OrganizationMemberState]{}, err
	}
	state.MemberID, state.Pending = member.ID, member.Pending
	return infer.CreateResponse[OrganizationMemberState]{ID: req.Inputs.OrganizationSlug + "/" + member.ID, Output: state}, nil
}

func (OrganizationMember) Read(ctx context.Context, req infer.ReadRequest[OrganizationMemberArgs, OrganizationMemberState]) (infer.ReadResponse[OrganizationMemberArgs, OrganizationMemberState], error) {
	parts, err := splitID(req.ID, 2, "<organizationSlug>/<memberId>")
	if err != nil {
		return infer.ReadResponse[OrganizationMemberArgs, OrganizationMemberState]{}, err
	}
	member, err := client(ctx).GetMember(ctx, parts[0], parts[1])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[OrganizationMemberArgs, OrganizationMemberState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[OrganizationMemberArgs, OrganizationMemberState]{}, err
	}
	role := MemberRole(member.Role)
	inputs := OrganizationMemberArgs{
		OrganizationSlug: parts[0],
		Email:            strings.ToLower(member.Email),
		Role:             &role,
		Teams:            normalizeScopes(member.Teams),
	}
	if len(inputs.Teams) == 0 {
		inputs.Teams = nil
	}
	return infer.ReadResponse[OrganizationMemberArgs, OrganizationMemberState]{
		ID:     req.ID,
		Inputs: inputs,
		State:  OrganizationMemberState{OrganizationMemberArgs: inputs, MemberID: member.ID, Pending: member.Pending},
	}, nil
}

func (OrganizationMember) Diff(_ context.Context, req infer.DiffRequest[OrganizationMemberArgs, OrganizationMemberState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.OrganizationMemberArgs, req.Inputs, "organizationSlug", "email")), nil
}

func (OrganizationMember) Update(ctx context.Context, req infer.UpdateRequest[OrganizationMemberArgs, OrganizationMemberState]) (infer.UpdateResponse[OrganizationMemberState], error) {
	state := OrganizationMemberState{OrganizationMemberArgs: req.Inputs, MemberID: req.State.MemberID, Pending: req.State.Pending}
	if req.DryRun {
		return infer.UpdateResponse[OrganizationMemberState]{Output: state}, nil
	}
	c := client(ctx)
	member, err := c.GetMember(ctx, req.Inputs.OrganizationSlug, req.State.MemberID)
	if err != nil {
		return infer.UpdateResponse[OrganizationMemberState]{}, err
	}
	if err := reconcileMember(ctx, c, req.Inputs, member); err != nil {
		return infer.UpdateResponse[OrganizationMemberState]{}, err
	}
	state.Pending = member.Pending
	return infer.UpdateResponse[OrganizationMemberState]{Output: state}, nil
}

func (OrganizationMember) Delete(ctx context.Context, req infer.DeleteRequest[OrganizationMemberState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteMember(ctx, req.State.OrganizationSlug, req.State.MemberID); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

// reconcileMember brings the role and team memberships to the declared state.
func reconcileMember(ctx context.Context, c *glitchtip.Client, args OrganizationMemberArgs, member glitchtip.Member) error {
	if member.Role != string(*args.Role) {
		if _, err := c.UpdateMemberRole(ctx, args.OrganizationSlug, member.ID, string(*args.Role)); err != nil {
			return fmt.Errorf("glitchtip: setting role of %s: %w", args.Email, err)
		}
	}
	for _, team := range args.Teams {
		if !slices.Contains(member.Teams, team) {
			if err := c.AddMemberTeam(ctx, args.OrganizationSlug, member.ID, team); err != nil {
				return fmt.Errorf("glitchtip: adding %s to team %q: %w", args.Email, team, err)
			}
		}
	}
	for _, team := range member.Teams {
		if !slices.Contains(args.Teams, team) {
			if err := c.RemoveMemberTeam(ctx, args.OrganizationSlug, member.ID, team); err != nil && !glitchtip.IsNotFound(err) {
				return fmt.Errorf("glitchtip: removing %s from team %q: %w", args.Email, team, err)
			}
		}
	}
	return nil
}

// findMember looks a member up by email in the organization's list.
func findMember(ctx context.Context, c *glitchtip.Client, organization, email string) (glitchtip.Member, error) {
	members, err := c.ListMembers(ctx, organization)
	if err != nil {
		return glitchtip.Member{}, err
	}
	for _, member := range members {
		if strings.EqualFold(member.Email, email) {
			return member, nil
		}
	}
	return glitchtip.Member{}, fmt.Errorf("glitchtip: %s is reported as a member of %s but is not listed", email, organization)
}
