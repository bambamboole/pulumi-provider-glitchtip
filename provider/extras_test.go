package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

func alertArgs() ProjectAlertArgs {
	return ProjectAlertArgs{
		OrganizationSlug: "acme", ProjectSlug: "api", Name: "errors", TimespanMinutes: 5, Quantity: 10,
		Recipients: []AlertRecipient{
			{RecipientType: "webhook", Url: "https://chat.example.com/hooks/abc", TagsToAdd: []string{"release"}},
			{RecipientType: "email"},
		},
	}
}

func TestProjectAlertCheckValidatesRecipients(t *testing.T) {
	response, err := (ProjectAlert{}).Check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(map[string]property.Value{
		"organizationSlug": property.New("acme"),
		"projectSlug":      property.New("api"),
		"recipients": property.New([]property.Value{
			property.New(map[string]property.Value{"recipientType": property.New("discord")}),
			property.New(map[string]property.Value{"recipientType": property.New("email")}),
			property.New(map[string]property.Value{"recipientType": property.New("email")}),
		}),
	})})
	if err != nil {
		t.Fatal(err)
	}
	if response.Inputs.TimespanMinutes != 1 || response.Inputs.Quantity != 1 {
		t.Fatalf("defaults not applied: %+v", response.Inputs)
	}
	if len(response.Failures) != 2 || !strings.Contains(response.Failures[0].Reason, "url is required") || !strings.Contains(response.Failures[1].Reason, "one recipient per type") {
		t.Fatalf("expected a missing URL and a duplicate failure, got %+v", response.Failures)
	}
}

func TestProjectAlertLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addProject(org, "API")
	ctx := fake.ctx()

	created, err := (ProjectAlert{}).Create(ctx, infer.CreateRequest[ProjectAlertArgs]{Inputs: alertArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "acme/api/") || created.Output.AlertID == "" {
		t.Fatalf("unexpected output %+v", created)
	}

	read, err := (ProjectAlert{}).Read(ctx, infer.ReadRequest[ProjectAlertArgs, ProjectAlertState]{ID: created.ID, Inputs: alertArgs(), State: created.Output})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(read.Inputs, alertArgs()) {
		t.Fatalf("refresh must read back the declared inputs, got %+v", read.Inputs)
	}
	diff, err := (ProjectAlert{}).Diff(ctx, infer.DiffRequest[ProjectAlertArgs, ProjectAlertState]{Inputs: alertArgs(), State: read.State})
	if err != nil || diff.HasChanges {
		t.Fatalf("refresh must not diff: %+v %v", diff, err)
	}

	inputs := alertArgs()
	inputs.Uptime = true
	inputs.Recipients = []AlertRecipient{{RecipientType: "webhook", Url: "https://chat.example.com/hooks/abc"}}
	diff, err = (ProjectAlert{}).Diff(ctx, infer.DiffRequest[ProjectAlertArgs, ProjectAlertState]{Inputs: inputs, State: read.State})
	if err != nil || diff.DetailedDiff["uptime"].Kind != p.Update || diff.DetailedDiff["recipients"].Kind != p.Update {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	if _, err := (ProjectAlert{}).Update(ctx, infer.UpdateRequest[ProjectAlertArgs, ProjectAlertState]{ID: created.ID, Inputs: inputs, State: read.State}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	fake.mu.Lock()
	alerts := org.projects["api"].alerts
	fake.mu.Unlock()
	if len(alerts) != 1 || !alerts[0].Uptime || len(alerts[0].AlertRecipients) != 1 || alerts[0].AlertRecipients[0].TagsToAdd != nil {
		t.Fatalf("alert not updated: %+v", alerts)
	}

	// Import: the list is the only source and the second page holds the alert.
	fake.mu.Lock()
	for i := 0; i < 3; i++ {
		fake.nextID++
		org.projects["api"].alerts = append([]*glitchtip.ProjectAlert{{ID: int64(fake.nextID), AlertRecipients: []glitchtip.AlertRecipient{}}}, org.projects["api"].alerts...)
	}
	fake.mu.Unlock()
	imported, err := (ProjectAlert{}).Read(ctx, infer.ReadRequest[ProjectAlertArgs, ProjectAlertState]{ID: created.ID})
	if err != nil || imported.ID != created.ID || !imported.Inputs.Uptime || len(imported.Inputs.Recipients) != 1 {
		t.Fatalf("import must find the alert across pages: %+v %v", imported, err)
	}

	if _, err := (ProjectAlert{}).Delete(ctx, infer.DeleteRequest[ProjectAlertState]{ID: created.ID, State: read.State}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (ProjectAlert{}).Read(ctx, infer.ReadRequest[ProjectAlertArgs, ProjectAlertState]{ID: created.ID, Inputs: inputs, State: read.State})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted alert must read as missing: %+v %v", gone, err)
	}
}

func TestProjectAlertReadKeepsZulipKeyAndOrder(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addProject(org, "API")
	ctx := fake.ctx()
	args := alertArgs()
	args.Recipients = []AlertRecipient{
		{RecipientType: "zulip", Url: "https://zulip.example.com", BotEmail: "bot@example.com", ApiKey: "zulip-secret", Channel: "alerts", Topic: "GlitchTip Alerts"},
		{RecipientType: "email"},
	}
	created, err := (ProjectAlert{}).Create(ctx, infer.CreateRequest[ProjectAlertArgs]{Inputs: args})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// GlitchTip stores the key in config; simulate a release that hides it.
	fake.mu.Lock()
	delete(org.projects["api"].alerts[0].AlertRecipients[0].Config, "apiKey")
	// Reverse the order the API returns to check the declared order wins.
	recipients := org.projects["api"].alerts[0].AlertRecipients
	recipients[0], recipients[1] = recipients[1], recipients[0]
	fake.mu.Unlock()

	read, err := (ProjectAlert{}).Read(ctx, infer.ReadRequest[ProjectAlertArgs, ProjectAlertState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(read.Inputs.Recipients, args.Recipients) {
		t.Fatalf("expected declared recipients incl. the hidden key, got %+v", read.Inputs.Recipients)
	}
}

func monitorArgs() UptimeMonitorArgs {
	status := 200
	return UptimeMonitorArgs{OrganizationSlug: "acme", Name: "API", MonitorType: MonitorTypeGet, Url: "https://api.example.com/health", ExpectedStatus: &status, IntervalSeconds: 60, ConfirmationThreshold: 1}
}

func TestUptimeMonitorCheckDefaults(t *testing.T) {
	response, err := (UptimeMonitor{}).Check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(map[string]property.Value{
		"organizationSlug": property.New("acme"), "name": property.New("API"), "monitorType": property.New("GET"),
	})})
	if err != nil {
		t.Fatal(err)
	}
	if response.Inputs.IntervalSeconds != 60 || response.Inputs.ConfirmationThreshold != 1 || response.Inputs.ExpectedStatus == nil || *response.Inputs.ExpectedStatus != 200 {
		t.Fatalf("defaults not applied: %+v", response.Inputs)
	}
	if len(response.Failures) != 1 || response.Failures[0].Property != "url" {
		t.Fatalf("a GET monitor without URL must fail check: %+v", response.Failures)
	}
	heartbeat, err := (UptimeMonitor{}).Check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(map[string]property.Value{
		"organizationSlug": property.New("acme"), "name": property.New("cron"), "monitorType": property.New("Heartbeat"),
	})})
	if err != nil || len(heartbeat.Failures) != 0 || heartbeat.Inputs.ExpectedStatus != nil {
		t.Fatalf("a heartbeat monitor needs no URL or status: %+v %v", heartbeat, err)
	}
}

func TestUptimeMonitorLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addProject(org, "API")
	ctx := fake.ctx()
	fake.mu.Lock()
	projectID := org.projects["api"].id
	fake.mu.Unlock()

	args := monitorArgs()
	args.ProjectID = projectID
	created, err := (UptimeMonitor{}).Create(ctx, infer.CreateRequest[UptimeMonitorArgs]{Inputs: args})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "acme/") || created.Output.MonitorID == "" || created.Output.HeartbeatEndpoint != "" {
		t.Fatalf("unexpected output %+v", created)
	}

	read, err := (UptimeMonitor{}).Read(ctx, infer.ReadRequest[UptimeMonitorArgs, UptimeMonitorState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil || !reflect.DeepEqual(read.Inputs, args) {
		t.Fatalf("refresh must read back the declared inputs: %+v %v", read.Inputs, err)
	}
	diff, err := (UptimeMonitor{}).Diff(ctx, infer.DiffRequest[UptimeMonitorArgs, UptimeMonitorState]{Inputs: args, State: read.State})
	if err != nil || diff.HasChanges {
		t.Fatalf("refresh must not diff: %+v %v", diff, err)
	}

	inputs := args
	inputs.MonitorType, inputs.Url, inputs.ExpectedStatus, inputs.ProjectID = MonitorTypeHeartbeat, "", nil, ""
	inputs.IntervalSeconds = 300
	diff, err = (UptimeMonitor{}).Diff(ctx, infer.DiffRequest[UptimeMonitorArgs, UptimeMonitorState]{Inputs: inputs, State: read.State})
	if err != nil || len(diff.DetailedDiff) != 5 || diff.DetailedDiff["monitorType"].Kind != p.Update {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	updated, err := (UptimeMonitor{}).Update(ctx, infer.UpdateRequest[UptimeMonitorArgs, UptimeMonitorState]{ID: created.ID, Inputs: inputs, State: read.State})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !strings.Contains(updated.Output.HeartbeatEndpoint, "/api/0/organizations/acme/heartbeat_check/") {
		t.Fatalf("heartbeat monitors must expose their endpoint: %+v", updated.Output)
	}
	fake.mu.Lock()
	monitor := org.monitors[0]
	fake.mu.Unlock()
	if monitor.MonitorType != "Heartbeat" || monitor.ProjectID != nil || monitor.Interval != 300 {
		t.Fatalf("monitor not updated: %+v", monitor)
	}

	if _, err := (UptimeMonitor{}).Delete(ctx, infer.DeleteRequest[UptimeMonitorState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (UptimeMonitor{}).Read(ctx, infer.ReadRequest[UptimeMonitorArgs, UptimeMonitorState]{ID: created.ID, Inputs: inputs, State: updated.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("deleted monitor must read as missing: %+v %v", gone, err)
	}
}

func TestUptimeMonitorExplainsDisabledFeature(t *testing.T) {
	fake := newFakeGlitchTip(t)
	fake.addOrg()
	fake.uptime = false
	_, err := (UptimeMonitor{}).Create(fake.ctx(), infer.CreateRequest[UptimeMonitorArgs]{Inputs: monitorArgs()})
	if err == nil || !strings.Contains(err.Error(), "GLITCHTIP_ENABLE_UPTIME") {
		t.Fatalf("expected a hint about the disabled feature, got %v", err)
	}
}

func memberArgs() OrganizationMemberArgs {
	return OrganizationMemberArgs{OrganizationSlug: "acme", Email: "dev@example.com", Role: ptr(MemberRoleAdmin), Teams: []string{"backend"}}
}

func TestOrganizationMemberLifecycle(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addTeam(org, "ops")
	ctx := fake.ctx()

	created, err := (OrganizationMember{}).Create(ctx, infer.CreateRequest[OrganizationMemberArgs]{Inputs: memberArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "acme/") || created.Output.MemberID == "" || !created.Output.Pending {
		t.Fatalf("unexpected output %+v", created)
	}
	if fake.countRequests("POST", "/api/0/organizations/acme/members/") != 1 || fake.countRequests("PUT", "/api/0/organizations/acme/members/"+created.Output.MemberID+"/") != 0 {
		t.Fatalf("invitation must carry role and teams without extra calls: %v", fake.requests)
	}

	read, err := (OrganizationMember{}).Read(ctx, infer.ReadRequest[OrganizationMemberArgs, OrganizationMemberState]{ID: created.ID, Inputs: memberArgs(), State: created.Output})
	if err != nil || !reflect.DeepEqual(read.Inputs, memberArgs()) {
		t.Fatalf("refresh must read back the declared inputs: %+v %v", read.Inputs, err)
	}

	inputs := memberArgs()
	inputs.Role, inputs.Teams = ptr(MemberRoleMember), []string{"ops"}
	diff, err := (OrganizationMember{}).Diff(ctx, infer.DiffRequest[OrganizationMemberArgs, OrganizationMemberState]{Inputs: inputs, State: read.State})
	if err != nil || diff.DetailedDiff["role"].Kind != p.Update || diff.DetailedDiff["teams"].Kind != p.Update {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	if _, err := (OrganizationMember{}).Update(ctx, infer.UpdateRequest[OrganizationMemberArgs, OrganizationMemberState]{ID: created.ID, Inputs: inputs, State: read.State}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	fake.mu.Lock()
	member := org.members[0]
	fake.mu.Unlock()
	if member.role != "member" || strings.Join(member.teams, ",") != "ops" {
		t.Fatalf("member not reconciled: %+v", member)
	}

	if _, err := (OrganizationMember{}).Delete(ctx, infer.DeleteRequest[OrganizationMemberState]{ID: created.ID, State: read.State}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (OrganizationMember{}).Read(ctx, infer.ReadRequest[OrganizationMemberArgs, OrganizationMemberState]{ID: created.ID, Inputs: inputs, State: read.State})
	if err != nil || gone.ID != "" {
		t.Fatalf("removed member must read as missing: %+v %v", gone, err)
	}
}

func TestOrganizationMemberCreateAdoptsExistingMember(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	fake.addTeam(org, "backend")
	fake.addTeam(org, "ops")
	fake.addMember(org, "owner@example.com", "owner", false)
	existing := fake.addMember(org, "dev@example.com", "member", false, "ops")
	ctx := fake.ctx()

	created, err := (OrganizationMember{}).Create(ctx, infer.CreateRequest[OrganizationMemberArgs]{Inputs: memberArgs()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Output.MemberID != existing.id || created.Output.Pending {
		t.Fatalf("existing member must be adopted: %+v", created.Output)
	}
	if existing.role != "admin" || strings.Join(existing.teams, ",") != "backend" {
		t.Fatalf("adopted member must be reconciled: %+v", existing)
	}
}

func TestOrganizationMemberDeleteSurfacesOwnerRefusal(t *testing.T) {
	fake := newFakeGlitchTip(t)
	org := fake.addOrg()
	owner := fake.addMember(org, "owner@example.com", "owner", false)
	state := OrganizationMemberState{OrganizationMemberArgs: OrganizationMemberArgs{OrganizationSlug: "acme", Email: "owner@example.com", Role: ptr(MemberRoleOwner)}, MemberID: owner.id}
	_, err := (OrganizationMember{}).Delete(fake.ctx(), infer.DeleteRequest[OrganizationMemberState]{ID: "acme/" + owner.id, State: state})
	if err == nil || !strings.Contains(err.Error(), "Transfer ownership") {
		t.Fatalf("expected GlitchTip's refusal to surface, got %v", err)
	}
}
