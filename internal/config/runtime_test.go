package config

import (
	"strings"
	"testing"
)

func TestRuntimeValidate(t *testing.T) {
	base := Runtime{Storage: "memory", HTTPAddr: ":8080", BaseURL: "http://localhost:8080"}
	cases := []struct {
		name string
		edit func(*Runtime)
		want string
	}{
		{name: "memory default"},
		{name: "invalid environment", edit: func(c *Runtime) { c.AppEnv = "prod" }, want: "APP_ENV"},
		{name: "broken listener", edit: func(c *Runtime) { c.HTTPAddr = "not-an-address" }, want: "HTTP_ADDR"},
		{name: "postgres requires URL", edit: func(c *Runtime) { c.Storage = "postgres" }, want: "DATABASE_URL"},
		{
			name: "benchmark not allowed in development",
			edit: func(c *Runtime) { c.UnsafeBenchmarkMode = true },
			want: "BENCHMARK_DISABLE_RATELIMIT",
		},
		{
			name: "benchmark must be loopback",
			edit: func(c *Runtime) { c.AppEnv = "test"; c.UnsafeBenchmarkMode = true },
			want: "loopback",
		},
		{
			name: "benchmark allowed for loopback",
			edit: func(c *Runtime) { c.AppEnv = "test"; c.HTTPAddr = "127.0.0.1:8080"; c.UnsafeBenchmarkMode = true },
		},
		{name: "unknown storage", edit: func(c *Runtime) { c.Storage = "sqlite" }, want: "STORAGE"},
		{name: "URL path", edit: func(c *Runtime) { c.BaseURL += "/graphql" }, want: "PUBLIC_BASE_URL"},
		{
			name: "URL credentials",
			edit: func(c *Runtime) { c.BaseURL = "http://user:pass@localhost" },
			want: "PUBLIC_BASE_URL",
		},
		{name: "HTTP production", edit: func(c *Runtime) { c.Production = true }, want: "HTTPS"},
		{name: "invalid production signing secret", edit: func(c *Runtime) {
			c.Production = true
			c.BaseURL = "https://comments.example.org"
			c.SigningKey = "not-hex"
		}, want: "COMMENG_SIGNING_KEY"},
		{
			name: "missing production secret",
			edit: func(c *Runtime) { c.Production = true; c.BaseURL = "https://comments.example.org" },
			want: "COMMENG_SIGNING_KEY",
		},
		{name: "valid production", edit: func(c *Runtime) {
			c.Production = true
			c.BaseURL = "https://comments.example.org"
			c.SigningKey = strings.Repeat("ab", 32)
		}},
		{name: "open proxy trust", edit: func(c *Runtime) {
			c.Production = true
			c.BaseURL = "https://comments.example.org"
			c.SigningKey = strings.Repeat("ab", 32)
			c.TrustedProxyCIDR = "0.0.0.0/0"
		}, want: "TRUSTED_PROXY_CIDR"},
		{
			name: "invalid permalink",
			edit: func(c *Runtime) { c.PermalinkTemplate = "https://example.org/posts/{post}" },
			want: "PERMALINK_TEMPLATE",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			settings := base
			if test.edit != nil {
				test.edit(&settings)
			}
			err := settings.Validate()
			if test.want == "" && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validation error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestValidPermalinkTemplate(t *testing.T) {
	cases := []struct {
		name       string
		template   string
		production bool
		valid      bool
	}{
		{"https production", "https://example.org/p/{post}#c{comment}", true, true},
		{"http dev", "http://localhost/p/{post}/c/{comment}", false, true},
		{"http production", "http://example.org/p/{post}/c/{comment}", true, false},
		{"relative", "/p/{post}/c/{comment}", false, false},
		{"malformed", "https://%zz/p/{post}/c/{comment}", false, false},
		{"missing comment", "https://example.org/p/{post}", false, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidPermalinkTemplate(test.template, test.production); got != test.valid {
				t.Fatalf("ValidPermalinkTemplate(%q) = %t, want %t", test.template, got, test.valid)
			}
		})
	}
}
