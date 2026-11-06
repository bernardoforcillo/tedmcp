package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Over HTTP, anything that can reach the port can run every tool — and with
// the team tools, rewrite the team's shortlist. Bearer tokens close that: each
// member of the team gets a token, the server knows who is calling, and the
// shortlist records who changed what.
//
// Tokens are configured, not issued: a list of "name:token" pairs in
// TEDMCP_TOKENS, or one "name token" pair per line in the file named by
// -tokens-file / TEDMCP_TOKENS_FILE. This is deliberately simpler than OAuth.
// A team of analysts behind one ingress does not need an authorization server;
// it needs the open endpoint shut, and a name on each request.

// tokenSet maps the digest of each token to the name it belongs to. Tokens are
// compared by digest in constant time, so neither their length nor their
// prefix leaks through timing.
type tokenSet map[[32]byte]string

// loadTokens reads tokens from the environment and, optionally, a file. An
// empty set means authentication is off.
func loadTokens(file string) (tokenSet, error) {
	set := tokenSet{}
	add := func(name, token, where string) error {
		name, token = strings.TrimSpace(name), strings.TrimSpace(token)
		if name == "" || token == "" {
			return fmt.Errorf("%s: each entry needs a name and a token", where)
		}
		if len(token) < 16 {
			return fmt.Errorf("%s: the token for %s is shorter than 16 characters", where, name)
		}
		set[sha256.Sum256([]byte(token))] = name
		return nil
	}

	if env := strings.TrimSpace(os.Getenv("TEDMCP_TOKENS")); env != "" {
		for _, entry := range strings.Split(env, ",") {
			name, token, ok := strings.Cut(strings.TrimSpace(entry), ":")
			if !ok {
				return nil, fmt.Errorf("TEDMCP_TOKENS: entries are name:token, separated by commas")
			}
			if err := add(name, token, "TEDMCP_TOKENS"); err != nil {
				return nil, err
			}
		}
	}

	if file = strings.TrimSpace(file); file == "" {
		file = strings.TrimSpace(os.Getenv("TEDMCP_TOKENS_FILE"))
	}
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("tokens file: %w", err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				return nil, fmt.Errorf("%s:%d: expected \"name token\"", file, n)
			}
			if err := add(fields[0], fields[1], fmt.Sprintf("%s:%d", file, n)); err != nil {
				return nil, err
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// verifier checks a bearer token against the set. Every candidate is compared,
// so the time taken does not depend on which token matched.
func (s tokenSet) verifier() auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		sum := sha256.Sum256([]byte(token))
		name := ""
		for digest, n := range s {
			if subtle.ConstantTimeCompare(digest[:], sum[:]) == 1 {
				name = n
			}
		}
		if name == "" {
			return nil, auth.ErrInvalidToken
		}
		// Configured tokens do not expire; the SDK requires an expiry, so
		// each verification grants a short one.
		return &auth.TokenInfo{UserID: name, Expiration: time.Now().Add(time.Hour)}, nil
	}
}
