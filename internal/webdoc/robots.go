// Package webdoc fetches tender documents from buyers' own procurement
// portals, within the limits those portals set for automated clients.
//
// Tender documents are public, but the sites that host them are not open to
// bulk retrieval: most Italian procurement portals answer "Disallow: /" to
// every agent except search engines, and gate the procedure page behind a
// captcha. This package treats those signals as binding rather than as
// obstacles: it reads robots.txt, obeys it, and reports plainly when a
// document cannot be retrieved, so an inaccessible file is never mistaken for
// an absent one.
package webdoc

import (
	"bufio"
	"strings"
)

// robots is a parsed robots.txt: the rule groups that apply to one user agent.
type robots struct {
	groups []group
}

type group struct {
	agents []string
	rules  []rule
}

type rule struct {
	path  string
	allow bool
}

// parseRobots reads a robots.txt body. Unknown directives and malformed lines
// are skipped, matching how servers expect the file to be treated.
func parseRobots(body string) *robots {
	r := &robots{}
	var cur *group
	// A blank line ends a group, but consecutive User-agent lines share one.
	pendingAgents := false

	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "user-agent":
			if cur == nil || !pendingAgents {
				r.groups = append(r.groups, group{})
				cur = &r.groups[len(r.groups)-1]
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
			pendingAgents = true
		case "allow", "disallow":
			if cur == nil {
				continue
			}
			pendingAgents = false
			// "Disallow:" with no value is an explicit allow-all.
			if value == "" && key == "disallow" {
				continue
			}
			cur.rules = append(cur.rules, rule{path: value, allow: key == "allow"})
		}
	}
	return r
}

// allows reports whether userAgent may fetch path.
//
// The most specific group wins: an exact user-agent match is preferred over
// the "*" catch-all, and a site that names no applicable group allows
// everything. Within a group the longest matching pattern wins, and Allow
// beats Disallow on equal length — the behaviour search engines implement.
func (r *robots) allows(userAgent, path string) bool {
	if r == nil || len(r.groups) == 0 {
		return true
	}
	ua := strings.ToLower(userAgent)

	var exact, wildcard *group
	for i := range r.groups {
		for _, a := range r.groups[i].agents {
			if a == "*" {
				if wildcard == nil {
					wildcard = &r.groups[i]
				}
				continue
			}
			// robots.txt agent tokens match a prefix of the product token.
			if a != "" && strings.Contains(ua, a) && exact == nil {
				exact = &r.groups[i]
			}
		}
	}

	g := exact
	if g == nil {
		g = wildcard
	}
	if g == nil {
		return true
	}

	best := rule{}
	bestLen := -1
	for _, ru := range g.rules {
		if !matchPattern(ru.path, path) {
			continue
		}
		n := len(ru.path)
		if n > bestLen || (n == bestLen && ru.allow) {
			best, bestLen = ru, n
		}
	}
	if bestLen < 0 {
		return true // no rule speaks to this path
	}
	return best.allow
}

// matchPattern applies robots.txt path matching: "*" stands for any run of
// characters and a trailing "$" anchors the end of the path.
func matchPattern(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}

	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		var idx int
		if i == 0 {
			// The first segment must sit at the start of the path.
			if !strings.HasPrefix(path[pos:], part) {
				return false
			}
			idx = 0
		} else {
			idx = strings.Index(path[pos:], part)
			if idx < 0 {
				return false
			}
		}
		pos += idx + len(part)
	}

	if anchored {
		// With a trailing wildcard the tail is free; otherwise it must end here.
		if !strings.HasSuffix(pattern, "*") && pos != len(path) {
			return false
		}
	}
	return true
}
