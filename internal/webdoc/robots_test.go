package webdoc

import "testing"

// portaleAppalti is the robots.txt served by the Maggioli "Portale Appalti"
// software that most Italian municipalities run: everything closed, with a
// narrow opening for search engines on the listing pages only.
const portaleAppalti = `User-agent: *
Disallow: /

User-agent: googlebot
Allow: /PortaleAppalti/it/ppgare_bandi_lista.wp*
Allow: /PortaleAppalti/it/ppgare_avvisi_lista.wp*

User-agent: bingbot
Allow: /PortaleAppalti/it/ppgare_bandi_lista.wp*
`

func TestRobotsPortaleAppalti(t *testing.T) {
	r := parseRobots(portaleAppalti)

	tests := []struct {
		name  string
		agent string
		path  string
		want  bool
	}{
		{"our agent is denied the procedure page", "tedmcp/0.1", "/PortaleAppalti/it/procedure/codice/G00749", false},
		{"our agent is denied even the listing", "tedmcp/0.1", "/PortaleAppalti/it/ppgare_bandi_lista.wp", false},
		{"our agent is denied the root", "tedmcp/0.1", "/", false},
		{"googlebot may read the listing", "Googlebot/2.1", "/PortaleAppalti/it/ppgare_bandi_lista.wp?x=1", true},
		{"googlebot has no rule for the procedure page, so it is allowed", "Googlebot/2.1", "/PortaleAppalti/it/procedure/codice/G00749", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.allows(tt.agent, tt.path); got != tt.want {
				t.Fatalf("allows(%q, %q) = %v, want %v", tt.agent, tt.path, got, tt.want)
			}
		})
	}
}

func TestRobotsRules(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		agent string
		path  string
		want  bool
	}{
		{
			name: "empty robots allows everything",
			body: "", agent: "tedmcp", path: "/anything", want: true,
		},
		{
			name:  "bare Disallow is an allow-all",
			body:  "User-agent: *\nDisallow:",
			agent: "tedmcp", path: "/anything", want: true,
		},
		{
			name:  "longest match wins over a broader disallow",
			body:  "User-agent: *\nDisallow: /docs\nAllow: /docs/public",
			agent: "tedmcp", path: "/docs/public/file.pdf", want: true,
		},
		{
			name:  "allow beats disallow at equal length",
			body:  "User-agent: *\nDisallow: /a\nAllow: /a",
			agent: "tedmcp", path: "/a", want: true,
		},
		{
			name:  "wildcard in the middle of a pattern",
			body:  "User-agent: *\nDisallow: */cards/attachments/download/*",
			agent: "tedmcp", path: "/x/cards/attachments/download/9", want: false,
		},
		{
			name:  "end anchor does not match a longer path",
			body:  "User-agent: *\nDisallow: /private$",
			agent: "tedmcp", path: "/private/file", want: true,
		},
		{
			name:  "end anchor matches exactly",
			body:  "User-agent: *\nDisallow: /private$",
			agent: "tedmcp", path: "/private", want: false,
		},
		{
			name:  "consecutive user-agent lines share one group",
			body:  "User-agent: alpha\nUser-agent: beta\nDisallow: /x",
			agent: "beta", path: "/x", want: false,
		},
		{
			name:  "a named group beats the catch-all",
			body:  "User-agent: *\nDisallow: /\n\nUser-agent: tedmcp\nAllow: /open",
			agent: "tedmcp/0.1", path: "/open/doc.pdf", want: true,
		},
		{
			name:  "rules of a group that does not apply are ignored",
			body:  "User-agent: googlebot\nDisallow: /",
			agent: "tedmcp", path: "/anything", want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRobots(tt.body).allows(tt.agent, tt.path); got != tt.want {
				t.Fatalf("allows(%q, %q) = %v, want %v", tt.agent, tt.path, got, tt.want)
			}
		})
	}
}
