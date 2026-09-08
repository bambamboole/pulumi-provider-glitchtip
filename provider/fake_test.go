package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// fakeGlitchTip is an in-memory GlitchTip covering the endpoints the resources
// use: allauth login, API tokens, organizations, teams, projects, keys. Lists
// are paginated with Link headers in pages of pageSize so pagination is
// exercised by every test.
type fakeGlitchTip struct {
	t        *testing.T
	mu       sync.Mutex
	server   *httptest.Server
	users    map[string]string // email -> password
	sessions map[string]string // sessionid -> email
	tokens   []*fakeToken
	orgs     map[string]*fakeOrg // slug -> organization
	uptime   bool                // GLITCHTIP_ENABLE_UPTIME
	nextID   int
	pageSize int
	ready    bool
	requests []string
}

type fakeToken struct {
	ID     int64    `json:"id"`
	Label  string   `json:"label"`
	Scopes []string `json:"scopes"`
	Token  string   `json:"token"`
	email  string
}

type fakeOrg struct {
	id, name, slug string
	teams          map[string]*fakeTeam
	projects       map[string]*fakeProject
	monitors       []*glitchtip.Monitor
	members        []*fakeMember
}

type fakeMember struct {
	id, email, role string
	pending         bool
	teams           []string
}

type fakeTeam struct{ id, slug string }

type fakeProject struct {
	id, name, slug string
	platform       *string
	throttle       int
	teams          []string
	keys           []*fakeKey
	alerts         []*glitchtip.ProjectAlert
}

type fakeKey struct {
	id, public string
	name       *string
	rateLimit  *glitchtip.RateLimit
}

func newFakeGlitchTip(t *testing.T) *fakeGlitchTip {
	fastBootstrap(t)
	f := &fakeGlitchTip{
		t:        t,
		users:    map[string]string{"owner@example.com": "owner-secret"},
		sessions: map[string]string{},
		orgs:     map[string]*fakeOrg{},
		pageSize: 2,
		ready:    true,
		uptime:   true,
	}
	f.server = httptest.NewServer(f)
	t.Cleanup(f.server.Close)
	return f
}

// addToken issues a token for the owner directly, as the UI would.
func (f *fakeGlitchTip) addToken(label string, scopes []string) *fakeToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issueToken("owner@example.com", label, scopes)
}

func (f *fakeGlitchTip) issueToken(email, label string, scopes []string) *fakeToken {
	f.nextID++
	token := &fakeToken{ID: int64(f.nextID), Label: label, Scopes: scopes, Token: fmt.Sprintf("tok-%d", f.nextID), email: email}
	f.tokens = append(f.tokens, token)
	return token
}

// addOrg adds the organization "Acme" with slug "acme".
func (f *fakeGlitchTip) addOrg() *fakeOrg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createOrg("Acme")
}

func (f *fakeGlitchTip) createOrg(name string) *fakeOrg {
	f.nextID++
	base := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
	slug := base
	for n := 1; f.orgs[slug] != nil; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	org := &fakeOrg{id: strconv.Itoa(f.nextID), name: name, slug: slug, teams: map[string]*fakeTeam{}, projects: map[string]*fakeProject{}}
	f.orgs[slug] = org
	return org
}

func (f *fakeGlitchTip) addTeam(org *fakeOrg, slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createTeam(org, slug)
}

func (f *fakeGlitchTip) createTeam(org *fakeOrg, slug string) *fakeTeam {
	f.nextID++
	team := &fakeTeam{id: strconv.Itoa(f.nextID), slug: slug}
	org.teams[slug] = team
	return team
}

// addProject adds a project owned by the "backend" team.
func (f *fakeGlitchTip) addProject(org *fakeOrg, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createProject(org, "backend", glitchtip.ProjectInput{Name: name})
}

func (f *fakeGlitchTip) createProject(org *fakeOrg, team string, input glitchtip.ProjectInput) *fakeProject {
	f.nextID++
	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input.Name), " ", "-"))
	if input.Slug != nil && *input.Slug != "" {
		slug = *input.Slug
	}
	project := &fakeProject{id: strconv.Itoa(f.nextID), name: input.Name, slug: slug, platform: input.Platform, throttle: input.EventThrottleRate, teams: []string{team}}
	org.projects[slug] = project
	f.createKey(project, glitchtip.ProjectKeyInput{Name: ptr("Default")})
	return project
}

func (f *fakeGlitchTip) createKey(project *fakeProject, input glitchtip.ProjectKeyInput) *fakeKey {
	f.nextID++
	key := &fakeKey{id: fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID), public: fmt.Sprintf("public-%d", f.nextID), name: input.Name, rateLimit: input.RateLimit}
	project.keys = append(project.keys, key)
	return key
}

func (f *fakeGlitchTip) keyJSON(project *fakeProject, key *fakeKey) map[string]any {
	host := strings.TrimPrefix(f.server.URL, "http://")
	return map[string]any{
		"id": key.id, "name": key.name, "label": key.name, "public": key.public, "projectID": project.id, "rateLimit": key.rateLimit,
		"dsn": map[string]string{
			"public":   fmt.Sprintf("http://%s@%s/%s", key.public, host, project.id),
			"secret":   fmt.Sprintf("http://%s@%s/%s", key.public, host, project.id),
			"security": fmt.Sprintf("http://%s/api/%s/security/?glitchtip_key=%s", host, project.id, key.public),
		},
	}
}

func orgJSON(org *fakeOrg) map[string]any {
	return map[string]any{"id": org.id, "name": org.name, "slug": org.slug}
}

func teamJSON(team *fakeTeam) map[string]any {
	return map[string]any{"id": team.id, "slug": team.slug, "isMember": true, "memberCount": 1}
}

func projectJSON(project *fakeProject) map[string]any {
	return map[string]any{"id": project.id, "name": project.name, "slug": project.slug, "platform": project.platform, "eventThrottleRate": project.throttle, "isMember": true}
}

func (f *fakeGlitchTip) addMember(org *fakeOrg, email, role string, pending bool, teams ...string) *fakeMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	member := &fakeMember{id: strconv.Itoa(f.nextID), email: email, role: role, pending: pending, teams: teams}
	org.members = append(org.members, member)
	return member
}

func memberJSON(member *fakeMember) map[string]any {
	teams := member.teams
	if teams == nil {
		teams = []string{}
	}
	return map[string]any{"id": member.id, "email": member.email, "role": member.role, "roleName": member.role, "pending": member.pending, "isOwner": member.role == "owner", "teams": teams}
}

// client returns an API client authenticated with a token of the owner.
func (f *fakeGlitchTip) client() *glitchtip.Client {
	token := f.addToken("test", []string{"org:admin"})
	c, err := glitchtip.New(f.server.URL, token.Token)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// ctx returns a context carrying an authenticated client, as the provider does.
func (f *fakeGlitchTip) ctx() context.Context {
	return context.WithValue(context.Background(), clientKey{}, f.client())
}

func (f *fakeGlitchTip) countRequests(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request == method+" "+path {
			count++
		}
	}
	return count
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decode(r *http.Request, into any) {
	_ = json.NewDecoder(r.Body).Decode(into)
}

// paginate writes one page of items and a GlitchTip-style Link header.
func (f *fakeGlitchTip) paginate(w http.ResponseWriter, r *http.Request, items []any) {
	start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	end := min(start+f.pageSize, len(items))
	page := items[start:end]
	if page == nil {
		page = []any{}
	}
	results := "false"
	if end < len(items) {
		results = "true"
	}
	// GlitchTip 6.2.6 wraps the header in a Python set literal ({'...'}).
	w.Header().Set("Link", fmt.Sprintf(`{'<%s%s?cursor=0>; rel="previous"; results="false", <%s%s?cursor=%d>; rel="next"; results="%s"'}`,
		f.server.URL, r.URL.Path, f.server.URL, r.URL.Path, end, results))
	writeJSON(w, http.StatusOK, page)
}

func (f *fakeGlitchTip) authenticated(r *http.Request) bool {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		for _, token := range f.tokens {
			if token.Token == strings.TrimPrefix(auth, "Bearer ") {
				return true
			}
		}
		return false
	}
	session, err := r.Cookie("sessionid")
	if err != nil || f.sessions[session.Value] == "" {
		return false
	}
	if r.Method != http.MethodGet {
		csrf, err := r.Cookie("csrftoken")
		if err != nil || r.Header.Get("X-CSRFToken") != csrf.Value {
			return false
		}
	}
	return true
}

var (
	tokenPath    = regexp.MustCompile(`^/api/0/api-tokens/(\d+)/$`)
	orgPath      = regexp.MustCompile(`^/api/0/organizations/([^/]+)/$`)
	orgTeamsPath = regexp.MustCompile(`^/api/0/organizations/([^/]+)/teams/$`)
	teamPath     = regexp.MustCompile(`^/api/0/teams/([^/]+)/([^/]+)/$`)
	teamProjects = regexp.MustCompile(`^/api/0/teams/([^/]+)/([^/]+)/projects/$`)
	projectPath  = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/$`)
	projectTeams = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/teams/$`)
	projectTeam  = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/teams/([^/]+)/$`)
	keysPath     = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/keys/$`)
	keyPath      = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/keys/([^/]+)/$`)
	alertsPath   = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/alerts/$`)
	alertPath    = regexp.MustCompile(`^/api/0/projects/([^/]+)/([^/]+)/alerts/(\d+)/$`)
	monitorsPath = regexp.MustCompile(`^/api/0/organizations/([^/]+)/monitors/$`)
	monitorPath  = regexp.MustCompile(`^/api/0/organizations/([^/]+)/monitors/(\d+)/$`)
	membersPath  = regexp.MustCompile(`^/api/0/organizations/([^/]+)/members/$`)
	memberPath   = regexp.MustCompile(`^/api/0/organizations/([^/]+)/members/(\d+)/$`)
	memberTeam   = regexp.MustCompile(`^/api/0/organizations/([^/]+)/members/(\d+)/teams/([^/]+)/$`)
)

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
}

func (f *fakeGlitchTip) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	path := r.URL.Path

	switch {
	case path == "/_allauth/browser/v1/config":
		if !f.ready {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "csrf-1", Path: "/"})
		writeJSON(w, http.StatusOK, map[string]any{"status": 200})
		return
	case path == "/_allauth/browser/v1/auth/login" && r.Method == http.MethodPost:
		csrf, err := r.Cookie("csrftoken")
		if err != nil || r.Header.Get("X-CSRFToken") != csrf.Value {
			writeJSON(w, http.StatusForbidden, map[string]any{"detail": "CSRF failed"})
			return
		}
		var body struct{ Email, Password string }
		decode(r, &body)
		if f.users[body.Email] == "" || f.users[body.Email] != body.Password {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": 400, "errors": []any{map[string]string{"code": "email_password_mismatch"}}})
			return
		}
		f.nextID++
		session := fmt.Sprintf("session-%d", f.nextID)
		f.sessions[session] = body.Email
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: session, Path: "/"})
		writeJSON(w, http.StatusOK, map[string]any{"status": 200})
		return
	}
	if !f.authenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Unauthorized"})
		return
	}
	// GlitchTip serves the token endpoints to browser sessions only.
	if strings.HasPrefix(path, "/api/0/api-tokens/") && r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Unauthorized"})
		return
	}

	switch {
	case path == "/api/0/api-tokens/" && r.Method == http.MethodGet:
		items := []any{}
		for _, token := range f.tokens {
			items = append(items, token)
		}
		f.paginate(w, r, items)
	case path == "/api/0/api-tokens/" && r.Method == http.MethodPost:
		var body struct {
			Label  string   `json:"label"`
			Scopes []string `json:"scopes"`
		}
		decode(r, &body)
		writeJSON(w, http.StatusCreated, f.issueToken("owner@example.com", body.Label, body.Scopes))
	case tokenPath.MatchString(path) && r.Method == http.MethodDelete:
		id, _ := strconv.ParseInt(tokenPath.FindStringSubmatch(path)[1], 10, 64)
		for i, token := range f.tokens {
			if token.ID == id {
				f.tokens = append(f.tokens[:i], f.tokens[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})

	case path == "/api/0/organizations/" && r.Method == http.MethodGet:
		items := []any{}
		for _, org := range f.orgs {
			items = append(items, orgJSON(org))
		}
		f.paginate(w, r, items)
	case path == "/api/0/organizations/" && r.Method == http.MethodPost:
		var body struct{ Name string }
		decode(r, &body)
		writeJSON(w, http.StatusCreated, orgJSON(f.createOrg(body.Name)))
	case orgPath.MatchString(path):
		org := f.orgs[orgPath.FindStringSubmatch(path)[1]]
		if org == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, orgJSON(org))
		case http.MethodPut:
			var body struct{ Name string }
			decode(r, &body)
			org.name = body.Name
			writeJSON(w, http.StatusOK, orgJSON(org))
		case http.MethodDelete:
			delete(f.orgs, org.slug)
			w.WriteHeader(http.StatusNoContent)
		}

	case orgTeamsPath.MatchString(path):
		org := f.orgs[orgTeamsPath.FindStringSubmatch(path)[1]]
		if org == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		if r.Method == http.MethodPost {
			var body struct{ Slug string }
			decode(r, &body)
			if org.teams[body.Slug] != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Team slug already exists"})
				return
			}
			writeJSON(w, http.StatusCreated, teamJSON(f.createTeam(org, body.Slug)))
			return
		}
		items := []any{}
		for _, team := range org.teams {
			items = append(items, teamJSON(team))
		}
		f.paginate(w, r, items)
	case teamPath.MatchString(path):
		m := teamPath.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.teams[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, teamJSON(org.teams[m[2]]))
		case http.MethodDelete:
			delete(org.teams, m[2])
			w.WriteHeader(http.StatusNoContent)
		}
	case teamProjects.MatchString(path) && r.Method == http.MethodPost:
		m := teamProjects.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.teams[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		var input glitchtip.ProjectInput
		decode(r, &input)
		writeJSON(w, http.StatusCreated, projectJSON(f.createProject(org, m[2], input)))

	case projectPath.MatchString(path):
		m := projectPath.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.projects[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		project := org.projects[m[2]]
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, projectJSON(project))
		case http.MethodPut:
			var input glitchtip.ProjectInput
			decode(r, &input)
			project.name, project.platform, project.throttle = input.Name, input.Platform, input.EventThrottleRate
			if input.Slug != nil && *input.Slug != project.slug {
				delete(org.projects, project.slug)
				project.slug = *input.Slug
				org.projects[project.slug] = project
			}
			writeJSON(w, http.StatusOK, projectJSON(project))
		case http.MethodDelete:
			delete(org.projects, project.slug)
			w.WriteHeader(http.StatusNoContent)
		}
	case projectTeams.MatchString(path) && r.Method == http.MethodGet:
		m := projectTeams.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.projects[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		items := []any{}
		for _, slug := range org.projects[m[2]].teams {
			items = append(items, teamJSON(org.teams[slug]))
		}
		f.paginate(w, r, items)
	case projectTeam.MatchString(path):
		m := projectTeam.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.projects[m[2]] == nil || org.teams[m[3]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		project := org.projects[m[2]]
		switch r.Method {
		case http.MethodPost:
			project.teams = append(project.teams, m[3])
			writeJSON(w, http.StatusCreated, projectJSON(project))
		case http.MethodDelete:
			for i, slug := range project.teams {
				if slug == m[3] {
					project.teams = append(project.teams[:i], project.teams[i+1:]...)
					break
				}
			}
			writeJSON(w, http.StatusOK, projectJSON(project))
		}

	case keysPath.MatchString(path):
		m := keysPath.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.projects[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		project := org.projects[m[2]]
		if r.Method == http.MethodPost {
			var input glitchtip.ProjectKeyInput
			decode(r, &input)
			writeJSON(w, http.StatusCreated, f.keyJSON(project, f.createKey(project, input)))
			return
		}
		items := []any{}
		for _, key := range project.keys {
			items = append(items, f.keyJSON(project, key))
		}
		f.paginate(w, r, items)
	case keyPath.MatchString(path):
		m := keyPath.FindStringSubmatch(path)
		org := f.orgs[m[1]]
		if org == nil || org.projects[m[2]] == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
			return
		}
		project := org.projects[m[2]]
		for i, key := range project.keys {
			if key.id != m[3] {
				continue
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, http.StatusOK, f.keyJSON(project, key))
			case http.MethodPut:
				var input glitchtip.ProjectKeyInput
				decode(r, &input)
				key.name, key.rateLimit = input.Name, input.RateLimit
				writeJSON(w, http.StatusOK, f.keyJSON(project, key))
			case http.MethodDelete:
				project.keys = append(project.keys[:i], project.keys[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found"})
	case alertsPath.MatchString(path), alertPath.MatchString(path):
		f.serveAlerts(w, r, path)
	case monitorsPath.MatchString(path), monitorPath.MatchString(path):
		f.serveMonitors(w, r, path)
	case membersPath.MatchString(path), memberPath.MatchString(path), memberTeam.MatchString(path):
		f.serveMembers(w, r, path)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, path)
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "unexpected"})
	}
}

func ptr[T any](v T) *T { return &v }

// serveAlerts mirrors apps/alerts/api.py: recipients are matched by type and
// URL on update, Zulip fields are folded into config, and there is no GET for
// a single alert.
func (f *fakeGlitchTip) serveAlerts(w http.ResponseWriter, r *http.Request, path string) {
	var m []string
	if m = alertPath.FindStringSubmatch(path); m == nil {
		m = alertsPath.FindStringSubmatch(path)
	}
	org := f.orgs[m[1]]
	if org == nil || org.projects[m[2]] == nil {
		notFound(w)
		return
	}
	project := org.projects[m[2]]
	store := func(input glitchtip.ProjectAlertInput, existing []glitchtip.AlertRecipient) []glitchtip.AlertRecipient {
		out := []glitchtip.AlertRecipient{}
		for _, recipient := range input.AlertRecipients {
			stored := glitchtip.AlertRecipient{RecipientType: recipient.RecipientType, URL: recipient.URL, TagsToAdd: recipient.TagsToAdd}
			for _, previous := range existing {
				if previous.RecipientType == recipient.RecipientType && previous.URL == recipient.URL {
					stored.ID = previous.ID
				}
			}
			if stored.ID == nil {
				f.nextID++
				stored.ID = ptr(int64(f.nextID))
			}
			if recipient.RecipientType == "zulip" {
				stored.Config = map[string]any{"botEmail": recipient.BotEmail, "apiKey": recipient.APIKey, "channel": recipient.Channel, "topic": recipient.Topic}
			}
			out = append(out, stored)
		}
		return out
	}
	if len(m) == 3 {
		switch r.Method {
		case http.MethodGet:
			items := []any{}
			for _, alert := range project.alerts {
				items = append(items, alert)
			}
			f.paginate(w, r, items)
		case http.MethodPost:
			var input glitchtip.ProjectAlertInput
			decode(r, &input)
			f.nextID++
			alert := &glitchtip.ProjectAlert{ID: int64(f.nextID), Name: input.Name, TimespanMinutes: input.TimespanMinutes, Quantity: input.Quantity, Uptime: input.Uptime}
			alert.AlertRecipients = store(input, nil)
			project.alerts = append(project.alerts, alert)
			writeJSON(w, http.StatusCreated, alert)
		}
		return
	}
	for i, alert := range project.alerts {
		if strconv.FormatInt(alert.ID, 10) != m[3] {
			continue
		}
		switch r.Method {
		case http.MethodPut:
			var input glitchtip.ProjectAlertInput
			decode(r, &input)
			alert.Name, alert.TimespanMinutes, alert.Quantity, alert.Uptime = input.Name, input.TimespanMinutes, input.Quantity, input.Uptime
			alert.AlertRecipients = store(input, alert.AlertRecipients)
			writeJSON(w, http.StatusOK, alert)
		case http.MethodDelete:
			project.alerts = append(project.alerts[:i], project.alerts[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	notFound(w)
}

// serveMonitors mirrors apps/uptime/api.py. Without the uptime feature the
// routes are not registered at all, which the fake reports as 404.
func (f *fakeGlitchTip) serveMonitors(w http.ResponseWriter, r *http.Request, path string) {
	var m []string
	if m = monitorPath.FindStringSubmatch(path); m == nil {
		m = monitorsPath.FindStringSubmatch(path)
	}
	org := f.orgs[m[1]]
	if !f.uptime || org == nil {
		notFound(w)
		return
	}
	apply := func(monitor *glitchtip.Monitor, input glitchtip.MonitorInput) {
		monitor.MonitorType, monitor.Name, monitor.URL = input.MonitorType, input.Name, input.URL
		monitor.ExpectedStatus, monitor.ExpectedBody = input.ExpectedStatus, input.ExpectedBody
		monitor.Interval, monitor.Timeout, monitor.ConfirmationThreshold = input.Interval, input.Timeout, input.ConfirmationThreshold
		monitor.ProjectID = nil
		if input.Project != nil {
			for _, project := range org.projects {
				if project.id == *input.Project {
					monitor.ProjectID = ptr(project.id)
				}
			}
		}
		if input.MonitorType == "Heartbeat" && monitor.EndpointID == nil {
			endpoint := fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID)
			monitor.EndpointID = &endpoint
			monitor.HeartbeatEndpoint = ptr(fmt.Sprintf("%s/api/0/organizations/%s/heartbeat_check/%s/", f.server.URL, org.slug, endpoint))
		}
	}
	if len(m) == 2 {
		switch r.Method {
		case http.MethodGet:
			items := []any{}
			for _, monitor := range org.monitors {
				items = append(items, monitor)
			}
			f.paginate(w, r, items)
		case http.MethodPost:
			var input glitchtip.MonitorInput
			decode(r, &input)
			f.nextID++
			monitor := &glitchtip.Monitor{ID: strconv.Itoa(f.nextID)}
			apply(monitor, input)
			org.monitors = append(org.monitors, monitor)
			writeJSON(w, http.StatusCreated, monitor)
		}
		return
	}
	for i, monitor := range org.monitors {
		if monitor.ID != m[2] {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, monitor)
		case http.MethodPut:
			var input glitchtip.MonitorInput
			decode(r, &input)
			apply(monitor, input)
			writeJSON(w, http.StatusOK, monitor)
		case http.MethodDelete:
			org.monitors = append(org.monitors[:i], org.monitors[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	notFound(w)
}

// serveMembers mirrors apps/organizations_ext/api.py: an invitation for an
// existing member answers 409, PUT changes only the role, teams have their own
// endpoints, and the owner cannot be removed.
func (f *fakeGlitchTip) serveMembers(w http.ResponseWriter, r *http.Request, path string) {
	var m []string
	if m = memberTeam.FindStringSubmatch(path); m == nil {
		if m = memberPath.FindStringSubmatch(path); m == nil {
			m = membersPath.FindStringSubmatch(path)
		}
	}
	org := f.orgs[m[1]]
	if org == nil {
		notFound(w)
		return
	}
	if len(m) == 2 {
		switch r.Method {
		case http.MethodGet:
			items := []any{}
			for _, member := range org.members {
				items = append(items, memberJSON(member))
			}
			f.paginate(w, r, items)
		case http.MethodPost:
			var invite glitchtip.MemberInvite
			decode(r, &invite)
			for _, member := range org.members {
				if member.email == invite.Email && !member.pending {
					writeJSON(w, http.StatusConflict, map[string]any{"detail": "The user " + invite.Email + " is already a member"})
					return
				}
				if member.email == invite.Email && member.pending {
					if !invite.Reinvite {
						writeJSON(w, http.StatusConflict, map[string]any{"detail": "already invited"})
						return
					}
					writeJSON(w, http.StatusCreated, memberJSON(member))
					return
				}
			}
			f.nextID++
			member := &fakeMember{id: strconv.Itoa(f.nextID), email: invite.Email, role: invite.OrgRole, pending: true}
			for _, role := range invite.TeamRoles {
				if org.teams[role.TeamSlug] != nil {
					member.teams = append(member.teams, role.TeamSlug)
				}
			}
			org.members = append(org.members, member)
			writeJSON(w, http.StatusCreated, memberJSON(member))
		}
		return
	}
	for i, member := range org.members {
		if member.id != m[2] {
			continue
		}
		if len(m) == 4 {
			if org.teams[m[3]] == nil {
				notFound(w)
				return
			}
			switch r.Method {
			case http.MethodPost:
				member.teams = append(member.teams, m[3])
				writeJSON(w, http.StatusCreated, teamJSON(org.teams[m[3]]))
			case http.MethodDelete:
				for j, team := range member.teams {
					if team == m[3] {
						member.teams = append(member.teams[:j], member.teams[j+1:]...)
						break
					}
				}
				writeJSON(w, http.StatusOK, teamJSON(org.teams[m[3]]))
			}
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, memberJSON(member))
		case http.MethodPut:
			var body struct {
				OrgRole string `json:"orgRole"`
			}
			decode(r, &body)
			member.role = body.OrgRole
			writeJSON(w, http.StatusOK, memberJSON(member))
		case http.MethodDelete:
			if member.role == "owner" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "User is organization owner. Transfer ownership first."})
				return
			}
			org.members = append(org.members[:i], org.members[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	notFound(w)
}
