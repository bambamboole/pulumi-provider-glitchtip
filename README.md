# pulumi-provider-glitchtip

A native Pulumi provider for [GlitchTip](https://glitchtip.com), written in Go with `pulumi-go-provider/infer`. It manages organizations, their members and teams, projects with their DSN keys and alert rules, uptime monitors, and issues the first API token of a freshly deployed instance without user interaction. The API shapes follow the OpenAPI document GlitchTip serves at `/api/openapi.json` and were checked against GlitchTip 6.2.6.

## Resources

- `glitchtip:index:Bootstrap` (API token of a user, obtained with email and password; see below)
- `glitchtip:index:Organization` (the slug is derived from the name and is the resource ID)
- `glitchtip:index:Team` (ID `<organizationSlug>/<slug>`; changing either replaces the team)
- `glitchtip:index:Project` (ID `<organizationSlug>/<projectSlug>`; exposes the DSN and CSP report URI of the key GlitchTip creates with the project)
- `glitchtip:index:ProjectKey` (an additional DSN key; ID `<organizationSlug>/<projectSlug>/<keyId>`)
- `glitchtip:index:ProjectAlert` (alert rule with recipients; ID `<organizationSlug>/<projectSlug>/<alertId>`; see below)
- `glitchtip:index:OrganizationMember` (invitation, role and team memberships; ID `<organizationSlug>/<memberId>`; see below)
- `glitchtip:index:UptimeMonitor` (HTTP, TCP, SSL or heartbeat check; ID `<organizationSlug>/<monitorId>`; see below)

## Provider configuration

```bash
pulumi config set glitchtip:baseUrl https://glitchtip.example.com
pulumi config set --secret glitchtip:apiToken <api-token>
```

Alternatively, use `GLITCHTIP_BASE_URL` and `GLITCHTIP_API_TOKEN`. The token may be omitted for a provider instance that only creates `Bootstrap` resources; every other request then fails with `glitchtip: missing API token`.

## Bootstrap: obtaining an API token without user interaction

A GlitchTip container creates its first user from `INITIAL_USER_EMAIL` and `INITIAL_USER_PASSWORD`. `glitchtip:index:Bootstrap` logs in with those credentials through the same endpoints as the web UI and issues an API token:

1. It waits until the instance answers, up to `readyTimeoutSeconds` (default 180), because a deployment triggered through Coolify or Compose starts asynchronously and may still be migrating.
2. It adopts an existing token with the same `tokenLabel` (default `pulumi`) and `scopes` (default: every scope GlitchTip offers), so a recreated resource does not pile up tokens. Otherwise it issues a new one.
3. The token is exposed as `token` (secret) and `tokenId`.

Changing `tokenLabel` or `scopes` rotates the token, changing `email` replaces the resource, and the password is never changed by the resource; keep it equal to the deployed `INITIAL_USER_PASSWORD`. A refresh logs in again and checks that the token still exists. A revoked token, or a host that no longer answers within 15 seconds (for example after a hostname change), marks the resource `repairRequired`; the next update issues a replacement token against the declared host, keeping the current one when the instance still knows it. A rejected login fails the refresh instead of dropping the state. Deleting the resource revokes the token and keeps the user.

Use two provider instances: one without a token for the bootstrap, one fed by its output for everything else.

```typescript
import * as glitchtip from "@bambamboole/pulumi-glitchtip";

const bootstrapProvider = new glitchtip.Provider("glitchtip-bootstrap", { baseUrl });
const bootstrap = new glitchtip.Bootstrap("infrastructure", {
    email: "infrastructure@example.com",
    password: ownerPassword,
    tokenLabel: "infrastructure",
}, { provider: bootstrapProvider, protect: true, dependsOn: deployment });

const provider = new glitchtip.Provider("glitchtip", { baseUrl, apiToken: bootstrap.token });
const organization = new glitchtip.Organization("acme", { name: "Acme" }, { provider });
const team = new glitchtip.Team("backend", { organizationSlug: organization.slug, slug: "backend" }, { provider });
const project = new glitchtip.Project("api", {
    organizationSlug: organization.slug,
    teamSlug: team.slug,
    name: "API",
    platform: "python",
}, { provider });
export const dsn = project.dsn;
export const cspReportUri = project.cspReportUri;
```

## Alerts

`glitchtip:index:ProjectAlert` notifies its recipients when `quantity` events (default 1) arrive within `timespanMinutes` (default 1), and with `uptime: true` on every failed check of the project's uptime monitors. Recipient types are `email` (the project's team members), `webhook` (Slack-compatible JSON, which Mattermost incoming webhooks accept), `discord`, `googlechat`, `teams`, `ntfy`, `feishu` and `zulip`; every type except `email` needs a `url`, Zulip additionally `botEmail`, `apiKey`, `channel` and optionally `topic`. GlitchTip keeps one recipient per type and URL and replaces the set on update. There is no endpoint for a single alert, so refresh and import read the project's alert list.

```typescript
new glitchtip.ProjectAlert("api-errors", {
    organizationSlug: organization.slug,
    projectSlug: project.projectSlug,
    name: "Errors to Mattermost",
    quantity: 1,
    timespanMinutes: 1,
    uptime: true,
    recipients: [
        { recipientType: "webhook", url: mattermostHook.url },
        { recipientType: "email" },
    ],
}, { provider });
```

## Members

`glitchtip:index:OrganizationMember` invites an email address with an organization `role` (`member`, `admin`, `manager` or `owner`; default `member`) and the `teams` it belongs to, and sends GlitchTip's invitation mail. An email that is already a member or has a pending invitation is adopted and brought to the declared role and teams instead of failing. The team list is authoritative: teams not listed are left on update. GlitchTip enforces two rules the provider surfaces as errors: with `ENABLE_USER_REGISTRATION=false` only emails of users that already exist on the instance can be invited, and the organization owner as well as the last member cannot be removed. `pending` reports whether the invitation is still open.

## Uptime monitors

`glitchtip:index:UptimeMonitor` creates a monitor in an organization. `monitorType` is one of `Ping`, `GET`, `POST`, `TCP Port`, `SSL` or `Heartbeat`; every type but `Heartbeat` needs a `url` (host:port for `TCP Port`). `GET` and `POST` compare the status with `expectedStatus` (default 200) and look for `expectedBody` in the response. `intervalSeconds` (default 60) is the check interval, or for heartbeats the silence after which the service counts as down; `timeoutSeconds` and `confirmationThreshold` (default 1) tune the check. Set `projectId` to the project whose alerts should fire, and enable `uptime` on that project's alert. Heartbeat monitors expose `heartbeatEndpoint`, the URL an external job must call.

The uptime feature is optional on the instance. Without `GLITCHTIP_ENABLE_UPTIME=true` the routes do not exist and the resource fails with a hint pointing at the setting. GlitchTip also refuses URLs that resolve to private addresses unless `GLITCHTIP_UPTIME_ALLOW_PRIVATE_IPS` is on.

```typescript
new glitchtip.UptimeMonitor("api-health", {
    organizationSlug: organization.slug,
    name: "API health",
    monitorType: "GET",
    url: "https://api.example.com/health",
    expectedStatus: 200,
    intervalSeconds: 60,
    confirmationThreshold: 2,
    projectId: project.projectId,
}, { provider });
```

## Behaviour worth knowing

- **Organizations** keep their slug for life: GlitchTip derives it from the name on creation and offers no way to change it. Renaming updates the display name only. When `ENABLE_ORGANIZATION_CREATION` is off on the instance, only a superuser (such as the initial user) may create organizations. Deleting an organization deletes every team and project in it.
- **Projects** are created through the owning team. Changing `teamSlug` attaches the new team and detaches the old one in place; a refresh that finds the declared team detached reports the first attached team so the next update re-attaches it. Changing `organizationSlug` or `slug` replaces the project, which deletes its events. Leave `slug` unset to let GlitchTip derive it from the name; the effective value is `projectSlug`.
- **DSNs**: GlitchTip creates one key with every project. `Project.dsn`, `Project.cspReportUri` and `Project.defaultKeyId` track it; when that key is deleted and exactly one key remains, the remaining one is adopted, otherwise both outputs become empty. `ProjectKey` manages further keys with an optional name and rate limit. Both expose `dsn` for SDK clients and `cspReportUri` as the `report-uri` of a Content-Security-Policy header. Deleting a key stops every client using its DSN.
- **Imports** work with the IDs above, e.g. `pulumi import glitchtip:index:Organization acme acme` or `pulumi import glitchtip:index:Project api acme/api`. `Bootstrap` cannot be imported because GlitchTip never reveals a token's value to a resource that did not issue it; create it instead and let it adopt the existing token by label and scopes.
- **Errors** carry the HTTP status and the first 300 characters of GlitchTip's response so validation messages such as a taken slug are visible. The login request never echoes anything but the status.

## Development

```bash
make build        # build the provider binary into bin/
make test         # unit tests against an in-memory GlitchTip
make lint         # golangci-lint v2 (see .golangci.yml)
make fmt          # gofmt all Go sources
make schema       # build the provider and dump schema.json
make gen-sdk      # regenerate the TypeScript SDK sources in sdk/nodejs
make build-sdk    # compile the SDK into sdk/nodejs/bin (what gets published)
make install-local  # install the plugin into the local Pulumi plugin cache
```

`go.sum` and the generated SDK sources under `sdk/nodejs` are checked in. CI fails if `go mod tidy` or `make gen-sdk` would produce a different result, so run them after changing resources or dependencies.

### Using the provider locally

1. Install the plugin into the local plugin cache:

   ```bash
   make install-local
   ```

2. Build the SDK and reference it from your Pulumi program:

   ```bash
   make build-sdk
   ```

   ```json
   {
     "dependencies": {
       "@bambamboole/pulumi-glitchtip": "file:/absolute/path/to/pulumi-provider-glitchtip/sdk/nodejs/bin"
     }
   }
   ```

   The plugin version reported by the binary, `pulumi-plugin.json` and the SDK's `package.json` must match; release-please keeps them in sync on releases.

## Releasing

Merging a release-please PR tags `vX.Y.Z`; the release workflow builds the plugin for Linux, macOS and Windows on amd64/arm64 with goreleaser, attaches `schema.json`, and publishes `@bambamboole/pulumi-glitchtip` to npm from `sdk/nodejs/bin`. Pulumi downloads the plugin from the GitHub release on demand.

The repository expects `RELEASE_PLEASE_TOKEN` for release-please. `NPM_TOKEN` is optional; without it the release workflow performs an npm dry-run instead.

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
