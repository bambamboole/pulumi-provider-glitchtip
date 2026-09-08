package glitchtip

import "testing"

func TestNextPageParsesGlitchTipLinkHeader(t *testing.T) {
	c, err := NewAnonymous("https://glitchtip.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	// Verbatim shape of GlitchTip 6.2.6, including the set-literal wrapping.
	more := `{'<https://glitchtip.example.com/api/0/organizations/?cursor=cD0x>; rel="previous"; results="false", <https://glitchtip.example.com/api/0/organizations/?cursor=cD0y&limit=50>; rel="next"; results="true"'}`
	next, err := c.nextPage(more, "/api/0/organizations/")
	if err != nil || next != "/api/0/organizations/?cursor=cD0y&limit=50" {
		t.Fatalf("expected the next page path, got %q %v", next, err)
	}
	last := `{'<https://glitchtip.example.com/api/0/organizations/>; rel="previous"; results="false", <https://glitchtip.example.com/api/0/organizations/>; rel="next"; results="false"'}`
	if next, err := c.nextPage(last, "/api/0/organizations/"); err != nil || next != "" {
		t.Fatalf("results=false must end pagination, got %q %v", next, err)
	}
	if next, err := c.nextPage("", "/api/0/organizations/"); err != nil || next != "" {
		t.Fatalf("a missing header must end pagination, got %q %v", next, err)
	}
	foreign := `<https://evil.example.com/api/0/organizations/?cursor=x>; rel="next"; results="true"`
	if _, err := c.nextPage(foreign, "/api/0/organizations/"); err == nil {
		t.Fatal("a link to another host must be rejected")
	}
	elsewhere := `<https://glitchtip.example.com/api/0/api-tokens/?cursor=x>; rel="next"; results="true"`
	if _, err := c.nextPage(elsewhere, "/api/0/organizations/"); err == nil {
		t.Fatal("a link to another endpoint must be rejected")
	}
}

func TestNewAnonymousNormalizesOrigin(t *testing.T) {
	c, err := NewAnonymous("https://glitchtip.example.com/")
	if err != nil || c.BaseURL() != "https://glitchtip.example.com" {
		t.Fatalf("unexpected base URL %q %v", c.BaseURL(), err)
	}
	for _, bad := range []string{"", "glitchtip.example.com", "https://user:pw@glitchtip.example.com", "https://glitchtip.example.com/api", "https://glitchtip.example.com/?x=1"} {
		if _, err := NewAnonymous(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}
