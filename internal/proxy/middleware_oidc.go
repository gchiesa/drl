package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/gchiesa/drl/internal/config"
)

// oidcContextKey is a typed key for values stored in request context to avoid collisions.
type oidcContextKey string

const oidcClaimsKey oidcContextKey = "oidc_claims"

// OIDCClaims holds the verified token identity injected into the request context
// by the OIDC middleware. Downstream handlers (including the accounting engine) can
// retrieve it via OIDCClaimsFromContext.
type OIDCClaims struct {
	Subject string
	Scopes  []string
	Roles   []string
}

// OIDCClaimsFromContext retrieves the verified OIDC claims from the request context.
// The second return value is false when no claims have been injected (i.e. the route
// has require-auth false, or OIDC is not configured for this host).
func OIDCClaimsFromContext(ctx context.Context) (OIDCClaims, bool) {
	c, ok := ctx.Value(oidcClaimsKey).(OIDCClaims)
	return c, ok
}

// oidcVerifier wraps a go-oidc IDTokenVerifier together with its host-level config.
type oidcVerifier struct {
	verifier    *gooidc.IDTokenVerifier
	cfg         config.ProxyOIDCConfig
	scopesField string // JWT claim field for scopes; default "scope"
	rolesField  string // JWT claim field for roles;  default "roles"
}

// newOIDCVerifier initialises an OIDC provider for the given host config by fetching
// the issuer's OpenID-configuration discovery document. Returns (nil, nil) when the
// issuer field is empty (OIDC disabled for this host).
func newOIDCVerifier(ctx context.Context, cfg config.ProxyOIDCConfig) (*oidcVerifier, error) {
	if cfg.Issuer == "" {
		return nil, nil
	}

	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider %q: %w", cfg.Issuer, err)
	}

	verifierCfg := &gooidc.Config{}
	if cfg.ClientID != "" {
		verifierCfg.ClientID = cfg.ClientID
	} else {
		verifierCfg.SkipClientIDCheck = true
	}

	scopesField := "scope"
	if cfg.Claims.Scopes != "" {
		scopesField = cfg.Claims.Scopes
	}
	rolesField := "roles"
	if cfg.Claims.Roles != "" {
		rolesField = cfg.Claims.Roles
	}

	return &oidcVerifier{
		verifier:    provider.Verifier(verifierCfg),
		cfg:         cfg,
		scopesField: scopesField,
		rolesField:  rolesField,
	}, nil
}

// oidcMiddleware returns an http.Handler that verifies a Bearer JWT token before
// delegating to next. It is a method on Server so it can call metrics helpers safely
// (nil-guard is respected — if s.metrics is nil, metric calls are skipped).
//
// Pipeline:
//  1. Extract "Authorization: Bearer <token>" — missing → 401
//  2. Verify signature & expiry via go-oidc — failure → 401
//  3. Audience check (when cfg.Audience is set) — mismatch → 403
//  4. Unmarshal custom claims
//  5. Scope enforcement (when requiredScopes is non-empty) — missing → 403
//  6. Inject OIDCClaims into request context
//  7. Call next
func (s *Server) oidcMiddleware(
	hostname string,
	v *oidcVerifier,
	requiredScopes []string,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Step 1: token extraction
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			s.metrics.IncOIDCRequest(hostname, r.URL.Path, "missing_token")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		rawToken := strings.TrimPrefix(authHeader, "Bearer ")

		// Step 2: signature + expiry verification
		idToken, err := v.verifier.Verify(r.Context(), rawToken)
		if err != nil {
			status := classifyOIDCError(err)
			s.metrics.IncOIDCRequest(hostname, r.URL.Path, status)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Record verification latency (crypto round-trip only)
		s.metrics.ObserveOIDCLatency(hostname, r.URL.Path, time.Since(start).Seconds())

		// Step 3: audience check
		if v.cfg.Audience != "" {
			found := false
			for _, a := range idToken.Audience {
				if a == v.cfg.Audience {
					found = true
					break
				}
			}
			if !found {
				s.metrics.IncOIDCRequest(hostname, r.URL.Path, "forbidden_scope")
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}

		// Step 4: unmarshal custom claims
		var rawClaims map[string]interface{}
		if err := idToken.Claims(&rawClaims); err != nil {
			s.metrics.IncOIDCRequest(hostname, r.URL.Path, "invalid_token")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		tokenScopes := extractStringSliceClaim(rawClaims, v.scopesField)
		tokenRoles := extractStringSliceClaim(rawClaims, v.rolesField)

		// Step 5: scope enforcement
		if len(requiredScopes) > 0 && !hasRequiredScopes(tokenScopes, requiredScopes) {
			s.metrics.IncOIDCRequest(hostname, r.URL.Path, "forbidden_scope")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Step 6: record success and inject claims
		s.metrics.IncOIDCRequest(hostname, r.URL.Path, "success")

		claims := OIDCClaims{
			Subject: idToken.Subject,
			Scopes:  tokenScopes,
			Roles:   tokenRoles,
		}
		ctx := context.WithValue(r.Context(), oidcClaimsKey, claims)

		// Step 7: forward
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractStringSliceClaim extracts a string slice from a JWT claim field.
// It handles three formats:
//   - space-delimited string: "read write"
//   - comma-delimited string: "read,write"
//   - JSON array: ["read", "write"]
func extractStringSliceClaim(claims map[string]interface{}, field string) []string {
	v, ok := claims[field]
	if !ok {
		return nil
	}
	switch tv := v.(type) {
	case string:
		parts := strings.FieldsFunc(tv, func(r rune) bool { return r == ' ' || r == ',' })
		return parts
	case []interface{}:
		result := make([]string, 0, len(tv))
		for _, item := range tv {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return tv
	default:
		return nil
	}
}

// hasRequiredScopes returns true when every element of required is present in tokenScopes.
func hasRequiredScopes(tokenScopes, required []string) bool {
	set := make(map[string]struct{}, len(tokenScopes))
	for _, s := range tokenScopes {
		set[s] = struct{}{}
	}
	for _, req := range required {
		if _, ok := set[req]; !ok {
			return false
		}
	}
	return true
}

// classifyOIDCError maps go-oidc error messages to the label values used in
// drl_proxy_oidc_requests_total{status=...}.
func classifyOIDCError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "expired"):
		return "token_expired"
	case strings.Contains(msg, "signature"):
		return "invalid_signature"
	default:
		return "invalid_token"
	}
}
