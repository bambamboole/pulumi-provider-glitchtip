// Package glitchtip is a hand-written client for the parts of the GlitchTip
// API the provider manages: API tokens, organizations, teams, projects and
// project keys (DSNs). Field shapes follow the OpenAPI document GlitchTip
// serves at /api/openapi.json (checked against GlitchTip 6.2.6).
//
// Two authentication modes exist. A Client built with New sends an API token
// as a bearer token. A Client built with NewAnonymous has no credentials until
// Login establishes a browser session through the allauth headless endpoints,
// which is how the Bootstrap resource issues the first API token.
package glitchtip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	apiPath      = "/api/0"
	allauthPath  = "/_allauth/browser/v1"
	maxPages     = 100
	maxErrorBody = 300
)

// ErrMissingToken is returned by every request of a provider configured
// without an API token; only Bootstrap resources work with such a provider.
var ErrMissingToken = errors.New("glitchtip: missing API token; set the provider's apiToken or the GLITCHTIP_API_TOKEN environment variable")

// ErrNotReady reports that the instance did not answer within the deadline.
var ErrNotReady = errors.New("glitchtip: the instance did not become ready before the deadline")

// Error is an unexpected HTTP status from the GlitchTip API.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *Error) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("glitchtip: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("glitchtip: %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is an HTTP 404.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is an HTTP 409.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsUnauthorized reports whether err is an HTTP 401 or 403.
func IsUnauthorized(err error) bool {
	return hasStatus(err, http.StatusUnauthorized) || hasStatus(err, http.StatusForbidden)
}

func hasStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// Client talks to one GlitchTip instance.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
	// cookies holds the browser session after Login; csrftoken is echoed in
	// the X-CSRFToken header as Django requires.
	cookies map[string]string
}

// New returns a client authenticated with an API token. An empty token yields
// a client whose requests fail with ErrMissingToken.
func New(baseURL, token string) (*Client, error) {
	c, err := NewAnonymous(baseURL)
	if err != nil {
		return nil, err
	}
	c.token = strings.TrimSpace(token)
	return c, nil
}

// NewAnonymous returns a client without credentials, to be used with Login.
func NewAnonymous(baseURL string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("glitchtip: base URL %q must include scheme and host", baseURL)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return nil, fmt.Errorf("glitchtip: base URL %q must be an origin without credentials, path, or query", baseURL)
	}
	return &Client{
		baseURL: parsed.Scheme + "://" + parsed.Host,
		http:    &http.Client{Timeout: 30 * time.Second},
		cookies: map[string]string{},
	}, nil
}

// BaseURL returns the normalized origin of the instance.
func (c *Client) BaseURL() string { return c.baseURL }

// HasCredentials reports whether requests carry a token or a session.
func (c *Client) HasCredentials() bool { return c.token != "" || c.cookies["sessionid"] != "" }

// WaitReady polls the allauth configuration endpoint until the instance
// answers or the timeout passes. Coolify starts a deployment asynchronously,
// so the first request after a deploy may reach a container that is still
// migrating. The endpoint also issues the CSRF cookie Login needs.
func (c *Client) WaitReady(ctx context.Context, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		response, err := c.send(ctx, http.MethodGet, allauthPath+"/config", nil)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return ErrNotReady
		}
		wait := interval
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Login opens a browser session for the user. Failures never echo the request
// because it carries the password.
func (c *Client) Login(ctx context.Context, email, password string) error {
	if c.cookies["csrftoken"] == "" {
		if err := c.WaitReady(ctx, 0, 0); err != nil {
			return fmt.Errorf("glitchtip: fetching the CSRF cookie: %w", err)
		}
		if c.cookies["csrftoken"] == "" {
			return errors.New("glitchtip: the instance did not issue a CSRF cookie")
		}
	}
	response, err := c.send(ctx, http.MethodPost, allauthPath+"/auth/login", map[string]string{"email": email, "password": password})
	if err != nil {
		return fmt.Errorf("glitchtip: login request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return &Error{StatusCode: response.StatusCode, Method: http.MethodPost, Path: allauthPath + "/auth/login", Body: "login rejected; check the user's email and password"}
	}
	if c.cookies["sessionid"] == "" {
		return errors.New("glitchtip: login did not issue a session cookie")
	}
	return nil
}

// send performs one request with the client's credentials and records the
// cookies of the response.
func (c *Client) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.token != "":
		request.Header.Set("Authorization", "Bearer "+c.token)
	case len(c.cookies) > 0:
		pairs := make([]string, 0, len(c.cookies))
		for name, value := range c.cookies {
			pairs = append(pairs, name+"="+value)
		}
		request.Header.Set("Cookie", strings.Join(pairs, "; "))
		if csrf := c.cookies["csrftoken"]; csrf != "" {
			request.Header.Set("X-CSRFToken", csrf)
			request.Header.Set("Origin", c.baseURL)
			request.Header.Set("Referer", c.baseURL+"/")
		}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	for _, cookie := range response.Cookies() {
		c.cookies[cookie.Name] = cookie.Value
	}
	return response, nil
}

// do performs an authenticated API request and decodes a JSON response into
// out when out is not nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if !c.HasCredentials() {
		return ErrMissingToken
	}
	response, err := c.send(ctx, method, path, body)
	if err != nil {
		return fmt.Errorf("glitchtip: %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("glitchtip: reading %s %s: %w", method, path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{StatusCode: response.StatusCode, Method: method, Path: path, Body: truncate(string(payload))}
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("glitchtip: decoding %s %s: %w", method, path, err)
	}
	return nil
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"(?:;\s*results="(true|false)")?`)

// list follows GlitchTip's Link-header pagination and appends every page's
// items to out, which must be a pointer to a slice.
func (c *Client) list(ctx context.Context, path string, out any) error {
	if !c.HasCredentials() {
		return ErrMissingToken
	}
	var pages []json.RawMessage
	next := path
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("glitchtip: GET %s exceeded %d pages", path, maxPages)
		}
		response, err := c.send(ctx, http.MethodGet, next, nil)
		if err != nil {
			return fmt.Errorf("glitchtip: GET %s: %w", next, err)
		}
		payload, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			return fmt.Errorf("glitchtip: reading GET %s: %w", next, err)
		}
		if response.StatusCode != http.StatusOK {
			return &Error{StatusCode: response.StatusCode, Method: http.MethodGet, Path: next, Body: truncate(string(payload))}
		}
		var items []json.RawMessage
		if err := json.Unmarshal(payload, &items); err != nil {
			return fmt.Errorf("glitchtip: decoding GET %s: %w", next, err)
		}
		pages = append(pages, items...)
		next, err = c.nextPage(response.Header.Get("Link"), path)
		if err != nil {
			return err
		}
	}
	merged, err := json.Marshal(pages)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, out)
}

// nextPage extracts the next page path from a Link header. GlitchTip marks a
// final page with results="false" on the next link, and 6.2.6 wraps the whole
// header in a Python set literal ({'...'}), which the regexp tolerates.
func (c *Client) nextPage(header, path string) (string, error) {
	for _, part := range strings.Split(header, ",") {
		match := nextLink.FindStringSubmatch(strings.TrimSpace(part))
		if match == nil {
			continue
		}
		if match[2] == "false" {
			return "", nil
		}
		target, err := url.Parse(match[1])
		if err != nil {
			return "", fmt.Errorf("glitchtip: unexpected pagination link %q", match[1])
		}
		if target.Host != "" && target.Scheme+"://"+target.Host != c.baseURL {
			return "", fmt.Errorf("glitchtip: pagination left the instance: %q", match[1])
		}
		if strings.Split(path, "?")[0] != target.Path {
			return "", fmt.Errorf("glitchtip: pagination left the endpoint: %q", match[1])
		}
		return target.Path + "?" + target.RawQuery, nil
	}
	return "", nil
}

func truncate(body string) string {
	body = strings.TrimSpace(body)
	if len(body) > maxErrorBody {
		return body[:maxErrorBody] + "…"
	}
	return body
}

func escape(segment string) string { return url.PathEscape(segment) }

// APIToken is a personal API token of the logged-in user. GlitchTip returns
// the token value on every listing, not only on creation. The token endpoints
// answer browser sessions only; a bearer token gets HTTP 401.
type APIToken struct {
	ID     int64    `json:"id"`
	Label  string   `json:"label"`
	Scopes []string `json:"scopes"`
	Token  string   `json:"token"`
}

// ListAPITokens lists the tokens of the authenticated user.
func (c *Client) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	var tokens []APIToken
	err := c.list(ctx, apiPath+"/api-tokens/", &tokens)
	return tokens, err
}

// CreateAPIToken issues a token with the given label and scope names.
func (c *Client) CreateAPIToken(ctx context.Context, label string, scopes []string) (APIToken, error) {
	var token APIToken
	err := c.do(ctx, http.MethodPost, apiPath+"/api-tokens/", map[string]any{"label": label, "scopes": scopes}, &token)
	return token, err
}

// DeleteAPIToken revokes a token.
func (c *Client) DeleteAPIToken(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/api-tokens/%d/", apiPath, id), nil, nil)
}

// Organization is a GlitchTip organization. The slug is derived from the name
// on creation and cannot be changed through the API.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ListOrganizations lists the organizations visible to the user.
func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	var organizations []Organization
	err := c.list(ctx, apiPath+"/organizations/", &organizations)
	return organizations, err
}

// CreateOrganization creates an organization; GlitchTip derives the slug.
func (c *Client) CreateOrganization(ctx context.Context, name string) (Organization, error) {
	var organization Organization
	err := c.do(ctx, http.MethodPost, apiPath+"/organizations/", map[string]string{"name": name}, &organization)
	return organization, err
}

// GetOrganization fetches an organization by slug.
func (c *Client) GetOrganization(ctx context.Context, slug string) (Organization, error) {
	var organization Organization
	err := c.do(ctx, http.MethodGet, apiPath+"/organizations/"+escape(slug)+"/", nil, &organization)
	return organization, err
}

// UpdateOrganization renames an organization.
func (c *Client) UpdateOrganization(ctx context.Context, slug, name string) (Organization, error) {
	var organization Organization
	err := c.do(ctx, http.MethodPut, apiPath+"/organizations/"+escape(slug)+"/", map[string]string{"name": name}, &organization)
	return organization, err
}

// DeleteOrganization deletes an organization with everything in it.
func (c *Client) DeleteOrganization(ctx context.Context, slug string) error {
	return c.do(ctx, http.MethodDelete, apiPath+"/organizations/"+escape(slug)+"/", nil, nil)
}

// Team is a team of an organization.
type Team struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

func teamPath(organization, team string) string {
	return apiPath + "/teams/" + escape(organization) + "/" + escape(team) + "/"
}

// ListTeams lists the teams of an organization.
func (c *Client) ListTeams(ctx context.Context, organization string) ([]Team, error) {
	var teams []Team
	err := c.list(ctx, apiPath+"/organizations/"+escape(organization)+"/teams/", &teams)
	return teams, err
}

// CreateTeam creates a team in an organization.
func (c *Client) CreateTeam(ctx context.Context, organization, slug string) (Team, error) {
	var team Team
	err := c.do(ctx, http.MethodPost, apiPath+"/organizations/"+escape(organization)+"/teams/", map[string]string{"slug": slug}, &team)
	return team, err
}

// GetTeam fetches a team by organization and slug.
func (c *Client) GetTeam(ctx context.Context, organization, slug string) (Team, error) {
	var team Team
	err := c.do(ctx, http.MethodGet, teamPath(organization, slug), nil, &team)
	return team, err
}

// DeleteTeam deletes a team.
func (c *Client) DeleteTeam(ctx context.Context, organization, slug string) error {
	return c.do(ctx, http.MethodDelete, teamPath(organization, slug), nil, nil)
}

// Project is a GlitchTip project.
type Project struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	Platform          *string `json:"platform"`
	EventThrottleRate int     `json:"eventThrottleRate"`
}

// ProjectInput is the body of project creation and update requests. Nil
// pointers are sent as null, which clears the field.
type ProjectInput struct {
	Name              string  `json:"name"`
	Slug              *string `json:"slug,omitempty"`
	Platform          *string `json:"platform"`
	EventThrottleRate int     `json:"eventThrottleRate"`
}

func projectPath(organization, project string) string {
	return apiPath + "/projects/" + escape(organization) + "/" + escape(project) + "/"
}

// CreateProject creates a project owned by a team.
func (c *Client) CreateProject(ctx context.Context, organization, team string, input ProjectInput) (Project, error) {
	var project Project
	err := c.do(ctx, http.MethodPost, teamPath(organization, team)+"projects/", input, &project)
	return project, err
}

// GetProject fetches a project by organization and slug.
func (c *Client) GetProject(ctx context.Context, organization, slug string) (Project, error) {
	var project Project
	err := c.do(ctx, http.MethodGet, projectPath(organization, slug), nil, &project)
	return project, err
}

// UpdateProject updates a project. Changing the slug changes the path of every
// later request, so callers keep the slug unless they intend to move it.
func (c *Client) UpdateProject(ctx context.Context, organization, slug string, input ProjectInput) (Project, error) {
	var project Project
	err := c.do(ctx, http.MethodPut, projectPath(organization, slug), input, &project)
	return project, err
}

// DeleteProject deletes a project with its events and keys.
func (c *Client) DeleteProject(ctx context.Context, organization, slug string) error {
	return c.do(ctx, http.MethodDelete, projectPath(organization, slug), nil, nil)
}

// ListProjectTeams lists the teams a project belongs to.
func (c *Client) ListProjectTeams(ctx context.Context, organization, project string) ([]Team, error) {
	var teams []Team
	err := c.list(ctx, projectPath(organization, project)+"teams/", &teams)
	return teams, err
}

// AddProjectTeam grants a team access to a project.
func (c *Client) AddProjectTeam(ctx context.Context, organization, project, team string) error {
	return c.do(ctx, http.MethodPost, projectPath(organization, project)+"teams/"+escape(team)+"/", nil, nil)
}

// RemoveProjectTeam revokes a team's access to a project.
func (c *Client) RemoveProjectTeam(ctx context.Context, organization, project, team string) error {
	return c.do(ctx, http.MethodDelete, projectPath(organization, project)+"teams/"+escape(team)+"/", nil, nil)
}

// RateLimit throttles a project key to count events per window seconds.
type RateLimit struct {
	Window int `json:"window"`
	Count  int `json:"count"`
}

// ProjectKey is a DSN of a project. DSN holds the "public" DSN among others.
type ProjectKey struct {
	ID        string            `json:"id"`
	Name      *string           `json:"name"`
	Label     *string           `json:"label"`
	Public    string            `json:"public"`
	DSN       map[string]string `json:"dsn"`
	RateLimit *RateLimit        `json:"rateLimit"`
}

// ProjectKeyInput is the body of key creation and update requests.
type ProjectKeyInput struct {
	Name      *string    `json:"name"`
	RateLimit *RateLimit `json:"rateLimit"`
}

// ListProjectKeys lists the keys of a project.
func (c *Client) ListProjectKeys(ctx context.Context, organization, project string) ([]ProjectKey, error) {
	var keys []ProjectKey
	err := c.list(ctx, projectPath(organization, project)+"keys/", &keys)
	return keys, err
}

// CreateProjectKey issues a new key.
func (c *Client) CreateProjectKey(ctx context.Context, organization, project string, input ProjectKeyInput) (ProjectKey, error) {
	var key ProjectKey
	err := c.do(ctx, http.MethodPost, projectPath(organization, project)+"keys/", input, &key)
	return key, err
}

// GetProjectKey fetches a key by ID.
func (c *Client) GetProjectKey(ctx context.Context, organization, project, id string) (ProjectKey, error) {
	var key ProjectKey
	err := c.do(ctx, http.MethodGet, projectPath(organization, project)+"keys/"+escape(id)+"/", nil, &key)
	return key, err
}

// UpdateProjectKey changes the name and rate limit of a key.
func (c *Client) UpdateProjectKey(ctx context.Context, organization, project, id string, input ProjectKeyInput) (ProjectKey, error) {
	var key ProjectKey
	err := c.do(ctx, http.MethodPut, projectPath(organization, project)+"keys/"+escape(id)+"/", input, &key)
	return key, err
}

// DeleteProjectKey revokes a key.
func (c *Client) DeleteProjectKey(ctx context.Context, organization, project, id string) error {
	return c.do(ctx, http.MethodDelete, projectPath(organization, project)+"keys/"+escape(id)+"/", nil, nil)
}
