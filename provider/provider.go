package provider

import (
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// New builds the GlitchTip provider, wiring configuration and all supported
// resources into the schema.
func New() (p.Provider, error) {
	return infer.NewProviderBuilder().
		WithConfig(infer.Config(&Config{})).
		// Nested types pick up the Go package name as their module; publish
		// them alongside the resources instead.
		WithModuleMap(map[tokens.ModuleName]tokens.ModuleName{"provider": "index"}).
		WithResources(
			infer.Resource(Bootstrap{}),
			infer.Resource(Organization{}),
			infer.Resource(Team{}),
			infer.Resource(Project{}),
			infer.Resource(ProjectKey{}),
			infer.Resource(ProjectAlert{}),
			infer.Resource(OrganizationMember{}),
			infer.Resource(UptimeMonitor{}),
		).
		WithDisplayName("GlitchTip").
		WithDescription("Manage resources on a GlitchTip instance: organizations, their members and teams, projects with their DSN keys and alert rules, uptime monitors, and the first API token of a freshly deployed instance.").
		WithPublisher("bambamboole").
		WithRepository("https://github.com/bambamboole/pulumi-provider-glitchtip").
		WithHomepage("https://glitchtip.com").
		WithLicense("Apache-2.0").
		// Let Pulumi download the plugin binary from GitHub Releases on demand.
		// release-please tags releases vX.Y.Z and the archive is named
		// pulumi-resource-glitchtip-vX.Y.Z-<os>-<arch>.tar.gz, matching Pulumi's
		// plugin naming. The $%7BVERSION%7D placeholder is interpolated by the
		// Pulumi CLI on download.
		WithPluginDownloadURL("https://github.com/bambamboole/pulumi-provider-glitchtip/releases/download/v$%7BVERSION%7D").
		Build()
}
