package provider

import (
	"context"
	"fmt"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// MonitorType is the kind of check an uptime monitor performs.
type MonitorType string

const (
	MonitorTypePing      MonitorType = "Ping"
	MonitorTypeGet       MonitorType = "GET"
	MonitorTypePost      MonitorType = "POST"
	MonitorTypePort      MonitorType = "TCP Port"
	MonitorTypeSSL       MonitorType = "SSL"
	MonitorTypeHeartbeat MonitorType = "Heartbeat"
)

func (MonitorType) Values() []infer.EnumValue[MonitorType] {
	return []infer.EnumValue[MonitorType]{
		{Name: "Ping", Value: MonitorTypePing, Description: "HTTP HEAD request; up when the URL answers."},
		{Name: "GET", Value: MonitorTypeGet, Description: "HTTP GET request; up when the status matches expectedStatus and the body contains expectedBody."},
		{Name: "POST", Value: MonitorTypePost, Description: "HTTP POST request; up when the status matches expectedStatus and the body contains expectedBody."},
		{Name: "TCPPort", Value: MonitorTypePort, Description: "TCP connection to host:port given as url."},
		{Name: "SSL", Value: MonitorTypeSSL, Description: "Certificate validity of the URL's host."},
		{Name: "Heartbeat", Value: MonitorTypeHeartbeat, Description: "Up while an external service keeps calling heartbeatEndpoint within interval."},
	}
}

const (
	defaultMonitorInterval  = 60
	defaultMonitorThreshold = 1
)

// UptimeMonitor manages an uptime monitor of an organization.
type UptimeMonitor struct{}

type UptimeMonitorArgs struct {
	OrganizationSlug      string      `pulumi:"organizationSlug" provider:"replaceOnChanges"`
	Name                  string      `pulumi:"name"`
	MonitorType           MonitorType `pulumi:"monitorType"`
	Url                   string      `pulumi:"url,optional"`
	ExpectedStatus        *int        `pulumi:"expectedStatus,optional"`
	ExpectedBody          string      `pulumi:"expectedBody,optional"`
	IntervalSeconds       int         `pulumi:"intervalSeconds,optional"`
	TimeoutSeconds        *int        `pulumi:"timeoutSeconds,optional"`
	ConfirmationThreshold int         `pulumi:"confirmationThreshold,optional"`
	ProjectID             string      `pulumi:"projectId,optional"`
}

type UptimeMonitorState struct {
	UptimeMonitorArgs
	MonitorID         string `pulumi:"monitorId"`
	HeartbeatEndpoint string `pulumi:"heartbeatEndpoint"`
}

func (r *UptimeMonitor) Annotate(a infer.Annotator) {
	a.SetToken("index", "UptimeMonitor")
	a.Describe(&r, "An uptime monitor of a GlitchTip organization: a periodic HTTP, TCP or certificate check, or a heartbeat endpoint an external job must call. Requires GLITCHTIP_ENABLE_UPTIME on the instance; without it the endpoints answer HTTP 404. Failures notify through project alerts with uptime enabled when projectId is set. The resource ID is <organizationSlug>/<monitorId>. Deleting the resource deletes the monitor with its check history.")
}

func (args *UptimeMonitorArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.OrganizationSlug, "Slug of the organization the monitor belongs to.")
	a.Describe(&args.Name, "Display name of the monitor.")
	a.Describe(&args.MonitorType, "Kind of check: Ping, GET, POST, TCP Port, SSL or Heartbeat.")
	a.Describe(&args.Url, "URL to check, or host:port for TCP Port. Not used by Heartbeat monitors. GlitchTip rejects private and internal addresses unless GLITCHTIP_UPTIME_ALLOW_PRIVATE_IPS is on.")
	a.Describe(&args.ExpectedStatus, "HTTP status that counts as up. Required for GET and POST; defaults to 200 for them.")
	a.Describe(&args.ExpectedBody, "Text the response body must contain for GET and POST checks. Empty accepts any body.")
	a.Describe(&args.IntervalSeconds, "Seconds between checks, 1 to 86400. For Heartbeat monitors the time without a call after which the service counts as down. Defaults to 60.")
	a.Describe(&args.TimeoutSeconds, "Seconds a check may take, 1 to 60. Unset uses GlitchTip's default of 20.")
	a.Describe(&args.ConfirmationThreshold, "Consecutive failed checks before the monitor counts as down and alerts fire, 1 to 100. Defaults to 1.")
	a.Describe(&args.ProjectID, "Numeric ID of the project whose alerts report failures, see Project.projectId. Unset leaves the monitor without alerting.")
	a.SetDefault(&args.IntervalSeconds, defaultMonitorInterval)
	a.SetDefault(&args.ConfirmationThreshold, defaultMonitorThreshold)
}

func (state *UptimeMonitorState) Annotate(a infer.Annotator) {
	a.Describe(&state.MonitorID, "Numeric ID of the monitor.")
	a.Describe(&state.HeartbeatEndpoint, "URL a Heartbeat monitor expects to be called; empty for other types.")
}

func (UptimeMonitor) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[UptimeMonitorArgs], error) {
	args, failures, err := infer.DefaultCheck[UptimeMonitorArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[UptimeMonitorArgs]{}, err
	}
	if args.IntervalSeconds <= 0 {
		args.IntervalSeconds = defaultMonitorInterval
	}
	if args.ConfirmationThreshold <= 0 {
		args.ConfirmationThreshold = defaultMonitorThreshold
	}
	if args.ExpectedStatus == nil && (args.MonitorType == MonitorTypeGet || args.MonitorType == MonitorTypePost) {
		status := 200
		args.ExpectedStatus = &status
	}
	if args.Url == "" && args.MonitorType != MonitorTypeHeartbeat {
		failures = append(failures, p.CheckFailure{Property: "url", Reason: fmt.Sprintf("url is required for %s monitors", args.MonitorType)})
	}
	return infer.CheckResponse[UptimeMonitorArgs]{Inputs: args, Failures: failures}, nil
}

func (UptimeMonitor) Create(ctx context.Context, req infer.CreateRequest[UptimeMonitorArgs]) (infer.CreateResponse[UptimeMonitorState], error) {
	state := UptimeMonitorState{UptimeMonitorArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[UptimeMonitorState]{Output: state}, nil
	}
	monitor, err := client(ctx).CreateMonitor(ctx, req.Inputs.OrganizationSlug, monitorInput(req.Inputs))
	if err != nil {
		return infer.CreateResponse[UptimeMonitorState]{}, uptimeError(err)
	}
	state.MonitorID, state.HeartbeatEndpoint = monitor.ID, deref(monitor.HeartbeatEndpoint)
	return infer.CreateResponse[UptimeMonitorState]{ID: req.Inputs.OrganizationSlug + "/" + monitor.ID, Output: state}, nil
}

func (UptimeMonitor) Read(ctx context.Context, req infer.ReadRequest[UptimeMonitorArgs, UptimeMonitorState]) (infer.ReadResponse[UptimeMonitorArgs, UptimeMonitorState], error) {
	parts, err := splitID(req.ID, 2, "<organizationSlug>/<monitorId>")
	if err != nil {
		return infer.ReadResponse[UptimeMonitorArgs, UptimeMonitorState]{}, err
	}
	monitor, err := client(ctx).GetMonitor(ctx, parts[0], parts[1])
	if glitchtip.IsNotFound(err) {
		return infer.ReadResponse[UptimeMonitorArgs, UptimeMonitorState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[UptimeMonitorArgs, UptimeMonitorState]{}, err
	}
	inputs := UptimeMonitorArgs{
		OrganizationSlug:      parts[0],
		Name:                  monitor.Name,
		MonitorType:           MonitorType(monitor.MonitorType),
		Url:                   monitor.URL,
		ExpectedStatus:        monitor.ExpectedStatus,
		ExpectedBody:          monitor.ExpectedBody,
		IntervalSeconds:       monitor.Interval,
		TimeoutSeconds:        monitor.Timeout,
		ConfirmationThreshold: monitor.ConfirmationThreshold,
		ProjectID:             deref(monitor.ProjectID),
	}
	return infer.ReadResponse[UptimeMonitorArgs, UptimeMonitorState]{
		ID:     req.ID,
		Inputs: inputs,
		State:  UptimeMonitorState{UptimeMonitorArgs: inputs, MonitorID: monitor.ID, HeartbeatEndpoint: deref(monitor.HeartbeatEndpoint)},
	}, nil
}

func (UptimeMonitor) Diff(_ context.Context, req infer.DiffRequest[UptimeMonitorArgs, UptimeMonitorState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.UptimeMonitorArgs, req.Inputs, "organizationSlug")), nil
}

func (UptimeMonitor) Update(ctx context.Context, req infer.UpdateRequest[UptimeMonitorArgs, UptimeMonitorState]) (infer.UpdateResponse[UptimeMonitorState], error) {
	state := UptimeMonitorState{UptimeMonitorArgs: req.Inputs, MonitorID: req.State.MonitorID, HeartbeatEndpoint: req.State.HeartbeatEndpoint}
	if req.DryRun {
		return infer.UpdateResponse[UptimeMonitorState]{Output: state}, nil
	}
	monitor, err := client(ctx).UpdateMonitor(ctx, req.Inputs.OrganizationSlug, req.State.MonitorID, monitorInput(req.Inputs))
	if err != nil {
		return infer.UpdateResponse[UptimeMonitorState]{}, uptimeError(err)
	}
	state.HeartbeatEndpoint = deref(monitor.HeartbeatEndpoint)
	return infer.UpdateResponse[UptimeMonitorState]{Output: state}, nil
}

func (UptimeMonitor) Delete(ctx context.Context, req infer.DeleteRequest[UptimeMonitorState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteMonitor(ctx, req.State.OrganizationSlug, req.State.MonitorID); err != nil && !glitchtip.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func monitorInput(args UptimeMonitorArgs) glitchtip.MonitorInput {
	return glitchtip.MonitorInput{
		MonitorType:           string(args.MonitorType),
		Name:                  args.Name,
		URL:                   args.Url,
		ExpectedStatus:        args.ExpectedStatus,
		ExpectedBody:          args.ExpectedBody,
		Interval:              args.IntervalSeconds,
		Timeout:               args.TimeoutSeconds,
		ConfirmationThreshold: args.ConfirmationThreshold,
		Project:               optional(args.ProjectID),
	}
}

// uptimeError explains the 404 an instance without the uptime feature returns.
func uptimeError(err error) error {
	if glitchtip.IsNotFound(err) {
		return fmt.Errorf("glitchtip: the uptime endpoints are not available; enable GLITCHTIP_ENABLE_UPTIME on the instance (%w)", err)
	}
	return err
}
