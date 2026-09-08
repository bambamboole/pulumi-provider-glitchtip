package glitchtip

import (
	"context"
	"fmt"
	"net/http"
)

// Monitor is an uptime monitor of an organization. The uptime endpoints exist
// only when GLITCHTIP_ENABLE_UPTIME is on; otherwise they answer HTTP 404.
// Shapes follow apps/uptime/schema.py of GlitchTip 6.2.6.
type Monitor struct {
	ID                    string  `json:"id"`
	MonitorType           string  `json:"monitorType"`
	Name                  string  `json:"name"`
	URL                   string  `json:"url"`
	ExpectedStatus        *int    `json:"expectedStatus"`
	ExpectedBody          string  `json:"expectedBody"`
	Interval              int     `json:"interval"`
	Timeout               *int    `json:"timeout"`
	ConfirmationThreshold int     `json:"confirmationThreshold"`
	ProjectID             *string `json:"projectId"`
	EndpointID            *string `json:"endpointId"`
	HeartbeatEndpoint     *string `json:"heartbeatEndpoint"`
	IsUp                  *bool   `json:"isUp"`
}

// MonitorInput is the body of monitor creation and update requests. Every
// field is sent because the update handler assigns all of them.
type MonitorInput struct {
	MonitorType           string  `json:"monitorType"`
	Name                  string  `json:"name"`
	URL                   string  `json:"url"`
	ExpectedStatus        *int    `json:"expectedStatus"`
	ExpectedBody          string  `json:"expectedBody"`
	Interval              int     `json:"interval"`
	Timeout               *int    `json:"timeout"`
	ConfirmationThreshold int     `json:"confirmationThreshold"`
	Project               *string `json:"project"`
}

func monitorsPath(organization string) string {
	return apiPath + "/organizations/" + escape(organization) + "/monitors/"
}

// ListMonitors lists the monitors of an organization.
func (c *Client) ListMonitors(ctx context.Context, organization string) ([]Monitor, error) {
	var monitors []Monitor
	err := c.list(ctx, monitorsPath(organization), &monitors)
	return monitors, err
}

// CreateMonitor creates a monitor.
func (c *Client) CreateMonitor(ctx context.Context, organization string, input MonitorInput) (Monitor, error) {
	var monitor Monitor
	err := c.do(ctx, http.MethodPost, monitorsPath(organization), input, &monitor)
	return monitor, err
}

// GetMonitor fetches a monitor by ID.
func (c *Client) GetMonitor(ctx context.Context, organization, id string) (Monitor, error) {
	var monitor Monitor
	err := c.do(ctx, http.MethodGet, monitorsPath(organization)+escape(id)+"/", nil, &monitor)
	return monitor, err
}

// UpdateMonitor replaces the settings of a monitor.
func (c *Client) UpdateMonitor(ctx context.Context, organization, id string, input MonitorInput) (Monitor, error) {
	var monitor Monitor
	err := c.do(ctx, http.MethodPut, monitorsPath(organization)+escape(id)+"/", input, &monitor)
	return monitor, err
}

// DeleteMonitor deletes a monitor with its check history.
func (c *Client) DeleteMonitor(ctx context.Context, organization, id string) error {
	return c.do(ctx, http.MethodDelete, monitorsPath(organization)+escape(id)+"/", nil, nil)
}

// AlertRecipient is a notification target of a project alert. Zulip settings
// travel flat in requests and come back nested under Config.
type AlertRecipient struct {
	ID            *int64         `json:"id,omitempty"`
	RecipientType string         `json:"recipientType"`
	URL           string         `json:"url"`
	TagsToAdd     []string       `json:"tagsToAdd,omitempty"`
	Config        map[string]any `json:"config,omitempty"`
	BotEmail      string         `json:"botEmail,omitempty"`
	APIKey        string         `json:"apiKey,omitempty"`
	Channel       string         `json:"channel,omitempty"`
	Topic         string         `json:"topic,omitempty"`
}

// ProjectAlert is an alert rule of a project.
type ProjectAlert struct {
	ID              int64            `json:"id"`
	Name            *string          `json:"name"`
	TimespanMinutes *int             `json:"timespanMinutes"`
	Quantity        *int             `json:"quantity"`
	Uptime          bool             `json:"uptime"`
	AlertRecipients []AlertRecipient `json:"alertRecipients"`
}

// ProjectAlertInput is the body of alert creation and update requests. The
// update handler applies only the fields present, so every field is sent.
type ProjectAlertInput struct {
	Name            *string          `json:"name"`
	TimespanMinutes *int             `json:"timespanMinutes"`
	Quantity        *int             `json:"quantity"`
	Uptime          bool             `json:"uptime"`
	AlertRecipients []AlertRecipient `json:"alertRecipients"`
}

func alertsPath(organization, project string) string {
	return projectPath(organization, project) + "alerts/"
}

// ListProjectAlerts lists the alert rules of a project. There is no endpoint
// for a single alert.
func (c *Client) ListProjectAlerts(ctx context.Context, organization, project string) ([]ProjectAlert, error) {
	var alerts []ProjectAlert
	err := c.list(ctx, alertsPath(organization, project), &alerts)
	return alerts, err
}

// CreateProjectAlert creates an alert rule.
func (c *Client) CreateProjectAlert(ctx context.Context, organization, project string, input ProjectAlertInput) (ProjectAlert, error) {
	var alert ProjectAlert
	err := c.do(ctx, http.MethodPost, alertsPath(organization, project), input, &alert)
	return alert, err
}

// UpdateProjectAlert replaces an alert rule. Recipients are matched by type
// and URL; the rest are deleted.
func (c *Client) UpdateProjectAlert(ctx context.Context, organization, project string, id int64, input ProjectAlertInput) (ProjectAlert, error) {
	var alert ProjectAlert
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s%d/", alertsPath(organization, project), id), input, &alert)
	return alert, err
}

// DeleteProjectAlert deletes an alert rule.
func (c *Client) DeleteProjectAlert(ctx context.Context, organization, project string, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s%d/", alertsPath(organization, project), id), nil, nil)
}

// Member is a user or pending invitation of an organization.
type Member struct {
	ID      string   `json:"id"`
	Email   string   `json:"email"`
	Role    string   `json:"role"`
	Pending bool     `json:"pending"`
	IsOwner bool     `json:"isOwner"`
	Teams   []string `json:"teams"`
}

// MemberInvite is the body of an invitation.
type MemberInvite struct {
	Email     string     `json:"email"`
	OrgRole   string     `json:"orgRole"`
	TeamRoles []TeamRole `json:"teamRoles"`
	Reinvite  bool       `json:"reinvite"`
}

// TeamRole names a team an invited member joins.
type TeamRole struct {
	TeamSlug string `json:"teamSlug"`
	Role     string `json:"role"`
}

func membersPath(organization string) string {
	return apiPath + "/organizations/" + escape(organization) + "/members/"
}

// ListMembers lists the members and pending invitations of an organization.
func (c *Client) ListMembers(ctx context.Context, organization string) ([]Member, error) {
	var members []Member
	err := c.list(ctx, membersPath(organization), &members)
	return members, err
}

// GetMember fetches a member with its team slugs.
func (c *Client) GetMember(ctx context.Context, organization, id string) (Member, error) {
	var member Member
	err := c.do(ctx, http.MethodGet, membersPath(organization)+escape(id)+"/", nil, &member)
	return member, err
}

// InviteMember invites a user by email. With registration disabled GlitchTip
// accepts only emails of existing users; an existing member answers HTTP 409.
func (c *Client) InviteMember(ctx context.Context, organization string, invite MemberInvite) (Member, error) {
	var member Member
	err := c.do(ctx, http.MethodPost, membersPath(organization), invite, &member)
	return member, err
}

// UpdateMemberRole sets the organization role of a member.
func (c *Client) UpdateMemberRole(ctx context.Context, organization, id, role string) (Member, error) {
	var member Member
	err := c.do(ctx, http.MethodPut, membersPath(organization)+escape(id)+"/", map[string]any{"orgRole": role, "teamRoles": []TeamRole{}}, &member)
	return member, err
}

// DeleteMember removes a member or withdraws an invitation.
func (c *Client) DeleteMember(ctx context.Context, organization, id string) error {
	return c.do(ctx, http.MethodDelete, membersPath(organization)+escape(id)+"/", nil, nil)
}

// AddMemberTeam adds a member to a team.
func (c *Client) AddMemberTeam(ctx context.Context, organization, id, team string) error {
	return c.do(ctx, http.MethodPost, membersPath(organization)+escape(id)+"/teams/"+escape(team)+"/", nil, nil)
}

// RemoveMemberTeam removes a member from a team.
func (c *Client) RemoveMemberTeam(ctx context.Context, organization, id, team string) error {
	return c.do(ctx, http.MethodDelete, membersPath(organization)+escape(id)+"/teams/"+escape(team)+"/", nil, nil)
}
