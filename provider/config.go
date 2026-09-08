package provider

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

const (
	envBaseURL  = "GLITCHTIP_BASE_URL"
	envAPIToken = "GLITCHTIP_API_TOKEN"
)

// Config holds the provider-level configuration.
type Config struct {
	// BaseURL is the origin of the GlitchTip instance.
	BaseURL string `pulumi:"baseUrl,optional"`
	// ApiToken is an API token of a GlitchTip user.
	ApiToken string `pulumi:"apiToken,optional" provider:"secret"`

	client *glitchtip.Client
}

func (c *Config) Annotate(a infer.Annotator) {
	a.Describe(&c, "Manage resources on a GlitchTip instance through its REST API.")
	a.Describe(&c.BaseURL, "Origin of the GlitchTip instance, e.g. https://glitchtip.example.com. Defaults to the GLITCHTIP_BASE_URL environment variable.")
	a.Describe(&c.ApiToken, "API token of a GlitchTip user (Profile > Auth Tokens), or the token issued by a Bootstrap resource. Defaults to the GLITCHTIP_API_TOKEN environment variable. May be omitted for a provider that only creates Bootstrap resources.")
	a.SetDefault(&c.BaseURL, "", envBaseURL)
	a.SetDefault(&c.ApiToken, "", envAPIToken)
}

// Configure validates the configuration and builds the API client once per
// provider process.
func (c *Config) Configure(_ context.Context) error {
	baseURL := firstNonEmpty(c.BaseURL, os.Getenv(envBaseURL))
	token := firstNonEmpty(c.ApiToken, os.Getenv(envAPIToken))
	if baseURL == "" {
		return errors.New("glitchtip: missing base URL; set the provider's baseUrl or the " + envBaseURL + " environment variable")
	}
	// Without a token the provider can still run Bootstrap resources, which
	// authenticate on their own; every other request fails with ErrMissingToken.
	client, err := glitchtip.New(baseURL, token)
	if err != nil {
		return err
	}
	c.client = client
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
