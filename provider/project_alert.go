package provider

import (
	"context"
	"fmt"
	"strconv"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

const (
	defaultAlertTimespan = 1
	defaultAlertQuantity = 1
)

// AlertRecipient is one notification target of a project alert.
type AlertRecipient struct {
	RecipientType string   `pulumi:"recipientType"`
	Url           string   `pulumi:"url,optional"`
	TagsToAdd     []string `pulumi:"tagsToAdd,optional"`
	BotEmail      string   `pulumi:"botEmail,optional"`
	ApiKey        string   `pulumi:"apiKey,optional" provider:"secret"`
	Channel       string   `pulumi:"channel,optional"`
	Topic         string   `pulumi:"topic,optional"`
}

func (recipient *AlertRecipient) Annotate(a infer.Annotator) {
	a.Describe(&recipient.RecipientType, "Kind of target: email (the project's team members), webhook (Slack-compatible, works with Mattermost incoming webhooks), discord, googlechat, teams, ntfy, feishu or zulip.")
	a.Describe(&recipient.Url, "Webhook URL. Required for every type except email.")
	a.Describe(&recipient.TagsToAdd, "Event tags to include in the notification.")
	a.Describe(&recipient.BotEmail, "Zulip bot email; zulip only.")
	a.Describe(&recipient.ApiKey, "Zulip bot API key; zulip only.")
	a.Describe(&recipient.Channel, "Zulip channel; zulip only.")
	a.Describe(&recipient.Topic, "Zulip topic; zulip only. Defaults to \"GlitchTip Alerts\".")
}

// ProjectAlert manages an alert rule of a project.
type ProjectAlert struct{}

type ProjectAlertArgs struct {
	OrganizationSlug string           `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	ProjectSlug      string           `pulumi:"projectSlug" provider:"replaceOnChanges"`
	Name             string           `pulumi:"name,optional"`
	TimespanMinutes  int              `pulumi:"timespanMinutes,optional"`
	Quantity         int              `pulumi:"quantity,optional"`
	Uptime           bool             `pulumi:"uptime,optional"`
	Recipients       []AlertRecipient `pulumi:"recipients"`
}

type ProjectAlertState struct {
	ProjectAlertArgs
	AlertID string `pulumi:"alertId"`
}

func (r *ProjectAlert) Annotate(a infer.Annotator) {
	a.SetToken("index", "ProjectAlert")
	a.Describe(&r, "An alert rule of a GlitchTip project: notify the recipients when quantity events arrive within timespanMinutes, and optionally when an uptime monitor of the project fails. Recipients are replaced as a whole on update; GlitchTip matches them by type and URL. The resource ID is <organizationSlug>/<projectSlug>/<alertId>.")
}

func (args *ProjectAlertArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization the project belongs to.")
	a.Describe(&args.ProjectSlug, "Slug of the project the alert belongs to.")
	a.Describe(&args.Name, "Display name of the alert.")
	a.Describe(&args.TimespanMinutes, "Window in minutes in which quantity events trigger the alert. Defaults to 1.")
	a.Describe(&args.Quantity, "Number of events within timespanMinutes that trigger the alert. Defaults to 1.")
	a.Describe(&args.Uptime, "Also notify on every failed check of the project's uptime monitors.")
	a.Describe(&args.Recipients, "Notification targets. At least one; each type and URL pair once.")
	a.SetDefault(&args.TimespanMinutes, defaultAlertTimespan)
	a.SetDefault(&args.Quantity, defaultAlertQuantity)
}

func (state *ProjectAlertState) Annotate(a infer.Annotator) {
	a.Describe(&state.AlertID, "Numeric ID of the alert.")
}

func (ProjectAlert) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[ProjectAlertArgs], error) {
	args, failures, err := infer.DefaultCheck[ProjectAlertArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[ProjectAlertArgs]{}, err
	}
	if args.TimespanMinutes <= 0 {
		args.TimespanMinutes = defaultAlertTimespan
	}
	if args.Quantity <= 0 {
		args.Quantity = defaultAlertQuantity
	}
	if len(args.Recipients) == 0 {
		failures = append(failures, p.CheckFailure{Property: "recipients", Reason: "at least one recipient is required"})
	}
	seen := map[string]bool{}
	for i := range args.Recipients {
		recipient := &args.Recipients[i]
		if len(recipient.TagsToAdd) == 0 {
			recipient.TagsToAdd = nil
		}
		if recipient.RecipientType != "email" && recipient.Url == "" {
			failures = append(failures, p.CheckFailure{Property: fmt.Sprintf("recipients[%d].url", i), Reason: "url is required for " + recipient.RecipientType + " recipients"})
		}
		key := recipient.RecipientType + " " + recipient.Url
		if seen[key] {
			failures = append(failures, p.CheckFailure{Property: fmt.Sprintf("recipients[%d]", i), Reason: "GlitchTip keeps one recipient per type and URL"})
		}
		seen[key] = true
	}
	return infer.CheckResponse[ProjectAlertArgs]{Inputs: args, Failures: failures}, nil
}

func (ProjectAlert) Create(ctx context.Context, req infer.CreateRequest[ProjectAlertArgs]) (infer.CreateResponse[ProjectAlertState], error) {
	state := ProjectAlertState{ProjectAlertArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[ProjectAlertState]{Output: state}, nil
	}
	alert, err := client(ctx).CreateProjectAlert(ctx, req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, alertInput(req.Inputs))
	if err != nil {
		return infer.CreateResponse[ProjectAlertState]{}, err
	}
	state.AlertID = strconv.FormatInt(alert.ID, 10)
	return infer.CreateResponse[ProjectAlertState]{ID: alertID(req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, state.AlertID), Output: state}, nil
}

// Read finds the alert in the project's list; GlitchTip has no endpoint for a
// single alert. Zulip API keys are kept from the declared inputs when the
// response omits them.
func (ProjectAlert) Read(ctx context.Context, req infer.ReadRequest[ProjectAlertArgs, ProjectAlertState]) (infer.ReadResponse[ProjectAlertArgs, ProjectAlertState], error) {
	parts, err := splitID(req.ID, 3, "<organizationSlug>/<projectSlug>/<alertId>")
	if err != nil {
		return infer.ReadResponse[ProjectAlertArgs, ProjectAlertState]{}, err
	}
	alerts, err := client(ctx).ListProjectAlerts(ctx, parts[0], parts[1])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[ProjectAlertArgs, ProjectAlertState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[ProjectAlertArgs, ProjectAlertState]{}, err
	}
	for _, alert := range alerts {
		if strconv.FormatInt(alert.ID, 10) != parts[2] {
			continue
		}
		inputs := ProjectAlertArgs{
			OrganizationSlug: parts[0],
			ProjectSlug:      parts[1],
			Name:             deref(alert.Name),
			TimespanMinutes:  derefInt(alert.TimespanMinutes),
			Quantity:         derefInt(alert.Quantity),
			Uptime:           alert.Uptime,
			Recipients:       readRecipients(alert.AlertRecipients, req.Inputs.Recipients),
		}
		return infer.ReadResponse[ProjectAlertArgs, ProjectAlertState]{
			ID:     req.ID,
			Inputs: inputs,
			State:  ProjectAlertState{ProjectAlertArgs: inputs, AlertID: parts[2]},
		}, nil
	}
	return infer.ReadResponse[ProjectAlertArgs, ProjectAlertState]{}, nil
}

func (ProjectAlert) Diff(_ context.Context, req infer.DiffRequest[ProjectAlertArgs, ProjectAlertState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.ProjectAlertArgs, req.Inputs, "organizationSlug", "projectSlug")), nil
}

func (ProjectAlert) Update(ctx context.Context, req infer.UpdateRequest[ProjectAlertArgs, ProjectAlertState]) (infer.UpdateResponse[ProjectAlertState], error) {
	state := ProjectAlertState{ProjectAlertArgs: req.Inputs, AlertID: req.State.AlertID}
	if req.DryRun {
		return infer.UpdateResponse[ProjectAlertState]{Output: state}, nil
	}
	id, err := strconv.ParseInt(req.State.AlertID, 10, 64)
	if err != nil {
		return infer.UpdateResponse[ProjectAlertState]{}, fmt.Errorf("glitchtip: alert ID %q is not numeric", req.State.AlertID)
	}
	if _, err := client(ctx).UpdateProjectAlert(ctx, req.Inputs.OrganizationSlug, req.Inputs.ProjectSlug, id, alertInput(req.Inputs)); err != nil {
		return infer.UpdateResponse[ProjectAlertState]{}, err
	}
	return infer.UpdateResponse[ProjectAlertState]{Output: state}, nil
}

func (ProjectAlert) Delete(ctx context.Context, req infer.DeleteRequest[ProjectAlertState]) (infer.DeleteResponse, error) {
	id, err := strconv.ParseInt(req.State.AlertID, 10, 64)
	if err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("glitchtip: alert ID %q is not numeric", req.State.AlertID)
	}
	if err := client(ctx).DeleteProjectAlert(ctx, req.State.OrganizationSlug, req.State.ProjectSlug, id); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func alertID(organization, project, id string) string {
	return organization + "/" + project + "/" + id
}

func alertInput(args ProjectAlertArgs) glitchtip.ProjectAlertInput {
	recipients := make([]glitchtip.AlertRecipient, 0, len(args.Recipients))
	for _, recipient := range args.Recipients {
		recipients = append(recipients, glitchtip.AlertRecipient{
			RecipientType: recipient.RecipientType,
			URL:           recipient.Url,
			TagsToAdd:     recipient.TagsToAdd,
			BotEmail:      recipient.BotEmail,
			APIKey:        recipient.ApiKey,
			Channel:       recipient.Channel,
			Topic:         recipient.Topic,
		})
	}
	timespan, quantity := args.TimespanMinutes, args.Quantity
	return glitchtip.ProjectAlertInput{
		Name:            optional(args.Name),
		TimespanMinutes: &timespan,
		Quantity:        &quantity,
		Uptime:          args.Uptime,
		AlertRecipients: recipients,
	}
}

// readRecipients maps the API's recipients back to inputs, in the declared
// order where a declared recipient matches by type and URL.
func readRecipients(actual []glitchtip.AlertRecipient, declared []AlertRecipient) []AlertRecipient {
	remaining := make([]glitchtip.AlertRecipient, 0, len(actual))
	remaining = append(remaining, actual...)
	take := func(recipientType, url string) (glitchtip.AlertRecipient, bool) {
		for i, candidate := range remaining {
			if candidate.RecipientType == recipientType && candidate.URL == url {
				remaining = append(remaining[:i], remaining[i+1:]...)
				return candidate, true
			}
		}
		return glitchtip.AlertRecipient{}, false
	}
	result := make([]AlertRecipient, 0, len(actual))
	for _, want := range declared {
		if found, ok := take(want.RecipientType, want.Url); ok {
			result = append(result, toRecipient(found, want.ApiKey))
		}
	}
	for _, extra := range remaining {
		result = append(result, toRecipient(extra, ""))
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func toRecipient(recipient glitchtip.AlertRecipient, declaredKey string) AlertRecipient {
	out := AlertRecipient{RecipientType: recipient.RecipientType, Url: recipient.URL}
	if len(recipient.TagsToAdd) > 0 {
		out.TagsToAdd = recipient.TagsToAdd
	}
	configString := func(key string) string {
		if value, ok := recipient.Config[key].(string); ok {
			return value
		}
		return ""
	}
	out.BotEmail, out.Channel, out.Topic = configString("botEmail"), configString("channel"), configString("topic")
	out.ApiKey = configString("apiKey")
	if out.ApiKey == "" {
		out.ApiKey = declaredKey
	}
	return out
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
