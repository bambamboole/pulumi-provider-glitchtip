package provider

import (
	"context"
	"strings"
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

func TestMissingTokenFailsRequests(t *testing.T) {
	fake := newFakeGlitchTip(t)
	c, err := glitchtip.New(fake.server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), clientKey{}, c)
	_, err = (Organization{}).Create(ctx, infer.CreateRequest[OrganizationArgs]{Inputs: OrganizationArgs{Name: "Acme"}})
	if err == nil || !strings.Contains(err.Error(), "missing API token") {
		t.Fatalf("expected ErrMissingToken, got %v", err)
	}
}

func TestOrganizationLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	ctx := fake.ctx()

	created, err := (Organization{}).Create(ctx, infer.CreateRequest[OrganizationArgs]{Inputs: OrganizationArgs{Name: "Artisan OS"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != "artisan-os" || created.Output.Slug != "artisan-os" || created.Output.OrganizationID == "" {
		t.Fatalf("unexpected output %+v", created)
	}

	read, err := (Organization{}).Read(ctx, infer.ReadRequest[OrganizationArgs, OrganizationState]{ID: created.ID})
	if err != nil || read.ID != created.ID || read.Inputs.Name != "Artisan OS" || read.State.OrganizationID != created.Output.OrganizationID {
		t.Fatalf("import must fill the inputs: %+v %v", read, err)
	}

	inputs := OrganizationArgs{Name: "Artisan OS GmbH"}
	diff, err := (Organization{}).Diff(ctx, infer.DiffRequest[OrganizationArgs, OrganizationState]{Inputs: inputs, State: created.Output})
	if err != nil || diff.DetailedDiff["name"].Kind != p.Update {
		t.Fatalf("rename must be an update: %+v %v", diff, err)
	}
	updated, err := (Organization{}).Update(ctx, infer.UpdateRequest[OrganizationArgs, OrganizationState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || updated.Output.Name != "Artisan OS GmbH" || updated.Output.Slug != "artisan-os" {
		t.Fatalf("rename must keep the slug: %+v %v", updated, err)
	}

	if _, err := (Organization{}).Delete(ctx, infer.DeleteRequest[OrganizationState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (Organization{}).Read(ctx, infer.ReadRequest[OrganizationArgs, OrganizationState]{ID: created.ID, Inputs: inputs, State: updated.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted organization must read as missing: %+v %v", gone, err)
	}
	if _, err := (Organization{}).Delete(ctx, infer.DeleteRequest[OrganizationState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("deleting a missing organization must succeed: %v", err)
	}
}

func TestTeamLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.addOrg()
	ctx := fake.ctx()

	created, err := (Team{}).Create(ctx, infer.CreateRequest[TeamArgs]{Inputs: TeamArgs{OrganizationSlug: "acme", Slug: "backend"}})
	if err != nil || created.ID != "acme/backend" || created.Output.TeamID == "" {
		t.Fatalf("Create: %+v %v", created, err)
	}
	read, err := (Team{}).Read(ctx, infer.ReadRequest[TeamArgs, TeamState]{ID: "acme/backend"})
	if err != nil || read.Inputs != (TeamArgs{OrganizationSlug: "acme", Slug: "backend"}) || read.State.TeamID != created.Output.TeamID {
		t.Fatalf("Read: %+v %v", read, err)
	}
	if _, err := (Team{}).Read(ctx, infer.ReadRequest[TeamArgs, TeamState]{ID: "backend"}); err == nil {
		t.Fatal("a malformed ID must be rejected")
	}
	if _, err := (Team{}).Delete(ctx, infer.DeleteRequest[TeamState]{ID: created.ID, State: created.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (Team{}).Read(ctx, infer.ReadRequest[TeamArgs, TeamState]{ID: "acme/backend"})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted team must read as missing: %+v %v", gone, err)
	}
}

func projectArgs() ProjectArgs {
	return ProjectArgs{OrganizationSlug: "acme", TeamSlug: "backend", Name: "API Server", Platform: "python"}
}

func TestProjectCreateExposesDefaultDsn(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.addTeam(fake.addOrg(), "backend")
	ctx := fake.ctx()

	created, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: projectArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	out := created.Output
	if created.ID != "acme/api-server" || out.ProjectSlug != "api-server" || out.Slug != "" || out.ProjectID == "" {
		t.Fatalf("unexpected output %+v", out)
	}
	if out.DefaultKeyID == "" || !strings.HasPrefix(out.Dsn, "http://public-") || !strings.Contains(out.CspReportURI, "/security/?glitchtip_key=public-") {
		t.Fatalf("expected the default key's DSN and CSP report URI, got %+v", out)
	}

	read, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: projectArgs(), State: out})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Inputs != projectArgs() || read.State.Dsn != out.Dsn || read.State.DefaultKeyID != out.DefaultKeyID {
		t.Fatalf("refresh must read back the declared inputs and DSN: %+v", read)
	}
	diff, err := (Project{}).Diff(ctx, infer.DiffRequest[ProjectArgs, ProjectState]{Inputs: projectArgs(), State: read.State})
	if err != nil || diff.HasChanges {
		t.Fatalf("refresh must not produce a diff: %+v %v", diff, err)
	}
}

func TestProjectReadAdoptsSlugOnImport(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addProject(org, "Legacy")
	ctx := fake.ctx()

	read, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: "acme/legacy"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Inputs != (ProjectArgs{OrganizationSlug: "acme", TeamSlug: "backend", Name: "Legacy", Slug: "legacy"}) {
		t.Fatalf("import must adopt every field: %+v", read.Inputs)
	}
	if read.State.DefaultKeyID == "" || read.State.Dsn == "" {
		t.Fatalf("import must adopt the only key: %+v", read.State)
	}
}

func TestProjectReadReportsDetachedTeam(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addTeam(org, "ops")
	ctx := fake.ctx()
	created, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: projectArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake.mu.Lock()
	org.projects["api-server"].teams = []string{"ops"}
	fake.mu.Unlock()

	read, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: projectArgs(), State: created.Output})
	if err != nil || read.Inputs.TeamSlug != "ops" {
		t.Fatalf("refresh must report the attached team: %+v %v", read.Inputs, err)
	}
	diff, err := (Project{}).Diff(ctx, infer.DiffRequest[ProjectArgs, ProjectState]{Inputs: projectArgs(), State: read.State})
	if err != nil || diff.DetailedDiff["teamSlug"].Kind != p.Update {
		t.Fatalf("team drift must be an in-place update: %+v %v", diff, err)
	}
	updated, err := (Project{}).Update(ctx, infer.UpdateRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: projectArgs(), State: read.State})
	if err != nil || updated.Output.TeamSlug != "backend" {
		t.Fatalf("Update: %+v %v", updated, err)
	}
	if fake.countRequests("POST", "/api/0/projects/acme/api-server/teams/backend/") != 1 || fake.countRequests("DELETE", "/api/0/projects/acme/api-server/teams/ops/") != 1 {
		t.Fatalf("expected one attach and one detach, got %v", fake.requests)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if teams := strings.Join(org.projects["api-server"].teams, ","); teams != "backend" {
		t.Fatalf("expected ops detached and backend attached, got %q", teams)
	}
}

func TestProjectUpdateAndDiff(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	ctx := fake.ctx()
	created, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: projectArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	inputs := projectArgs()
	inputs.Name, inputs.Platform, inputs.EventThrottleRate = "API", "", 10
	diff, err := (Project{}).Diff(ctx, infer.DiffRequest[ProjectArgs, ProjectState]{Inputs: inputs, State: created.Output})
	if err != nil || len(diff.DetailedDiff) != 3 || diff.DetailedDiff["name"].Kind != p.Update {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	updated, err := (Project{}).Update(ctx, infer.UpdateRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Output.ProjectSlug != "api-server" || updated.Output.Dsn != created.Output.Dsn {
		t.Fatalf("update must keep slug and DSN: %+v", updated.Output)
	}
	fake.mu.Lock()
	project := org.projects["api-server"]
	fake.mu.Unlock()
	if project == nil || project.name != "API" || project.platform != nil || project.throttle != 10 {
		t.Fatalf("project not updated: %+v", project)
	}

	replace := projectArgs()
	replace.Slug = "api"
	diff, err = (Project{}).Diff(ctx, infer.DiffRequest[ProjectArgs, ProjectState]{Inputs: replace, State: created.Output})
	if err != nil || diff.DetailedDiff["slug"].Kind != p.UpdateReplace {
		t.Fatalf("slug change must replace: %+v %v", diff, err)
	}

	if _, err := (Project{}).Delete(ctx, infer.DeleteRequest[ProjectState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: inputs, State: updated.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted project must read as missing: %+v %v", gone, err)
	}
}

func TestProjectKeyLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addProject(org, "API")
	ctx := fake.ctx()

	args := ProjectKeyArgs{OrganizationSlug: "acme", ProjectSlug: "api", Name: "mobile", RateLimit: &KeyRateLimit{Window: 60, Count: 100}}
	created, err := (ProjectKey{}).Create(ctx, infer.CreateRequest[ProjectKeyArgs]{Inputs: args})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "acme/api/") || created.Output.KeyID == "" || created.Output.Public == "" || !strings.Contains(created.Output.Dsn, created.Output.Public) || !strings.HasSuffix(created.Output.CspReportURI, "/security/?glitchtip_key="+created.Output.Public) {
		t.Fatalf("unexpected output %+v", created)
	}

	read, err := (ProjectKey{}).Read(ctx, infer.ReadRequest[ProjectKeyArgs, ProjectKeyState]{ID: created.ID})
	if err != nil || read.Inputs.Name != "mobile" || read.Inputs.RateLimit == nil || *read.Inputs.RateLimit != (KeyRateLimit{Window: 60, Count: 100}) || read.State.Dsn != created.Output.Dsn {
		t.Fatalf("import must fill the inputs: %+v %v", read, err)
	}

	inputs := ProjectKeyArgs{OrganizationSlug: "acme", ProjectSlug: "api", Name: "mobile-app"}
	diff, err := (ProjectKey{}).Diff(ctx, infer.DiffRequest[ProjectKeyArgs, ProjectKeyState]{Inputs: inputs, State: created.Output})
	if err != nil || diff.DetailedDiff["name"].Kind != p.Update || diff.DetailedDiff["rateLimit"].Kind != p.Update {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	updated, err := (ProjectKey{}).Update(ctx, infer.UpdateRequest[ProjectKeyArgs, ProjectKeyState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || updated.Output.RateLimit != nil || updated.Output.Dsn != created.Output.Dsn {
		t.Fatalf("Update: %+v %v", updated, err)
	}
	fake.mu.Lock()
	keys := org.projects["api"].keys
	fake.mu.Unlock()
	if len(keys) != 2 || keys[1].rateLimit != nil || *keys[1].name != "mobile-app" {
		t.Fatalf("key not updated: %+v", keys)
	}

	if _, err := (ProjectKey{}).Delete(ctx, infer.DeleteRequest[ProjectKeyState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (ProjectKey{}).Read(ctx, infer.ReadRequest[ProjectKeyArgs, ProjectKeyState]{ID: created.ID, Inputs: inputs, State: updated.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted key must read as missing: %+v %v", gone, err)
	}
	if _, err := (ProjectKey{}).Delete(ctx, infer.DeleteRequest[ProjectKeyState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("deleting a missing key must succeed: %v", err)
	}
}

func TestProjectDefaultKeyFollowsRemainingKey(t *testing.T) {
	keys := []glitchtip.ProjectKey{
		{ID: "a", DSN: map[string]string{"public": "dsn-a", "security": "csp-a"}},
		{ID: "b", DSN: map[string]string{"public": "dsn-b", "security": "csp-b"}},
	}
	if id, dsn, csp := defaultKey(keys, "b"); id != "b" || dsn != "dsn-b" || csp != "csp-b" {
		t.Fatalf("tracked key must win: %s %s %s", id, dsn, csp)
	}
	if id, dsn, csp := defaultKey(keys, "gone"); id != "" || dsn != "" || csp != "" {
		t.Fatalf("ambiguous keys must not be adopted: %s %s %s", id, dsn, csp)
	}
	if id, dsn, csp := defaultKey(keys[:1], "gone"); id != "a" || dsn != "dsn-a" || csp != "csp-a" {
		t.Fatalf("the only key must be adopted: %s %s %s", id, dsn, csp)
	}
}
