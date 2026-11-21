package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

type normalizedOrigin struct {
	scheme, host string
	port         uint16
}

// OriginPolicy is an immutable HTTP/WebSocket origin allowlist. Construct it
// before serving requests; a wildcard never accepts malformed or opaque origins.
type OriginPolicy struct {
	origins   []normalizedOrigin
	allowAll  bool
	allowNull bool
	single    string
}

var defaultOriginPolicy = OriginPolicy{allowAll: true}

// ParseOriginPolicy accepts comma-separated HTTP(S) origins, "*", and "null".
// An empty configuration is invalid; callers select their default explicitly.
func ParseOriginPolicy(configured string) (*OriginPolicy, error) {
	p := &OriginPolicy{}
	for item := range strings.SplitSeq(configured, ",") {
		item = strings.TrimSpace(item)
		switch item {
		case "*":
			p.allowAll = true
		case "null":
			p.allowNull = true
		default:
			origin, ok := parseOrigin(item)
			if !ok {
				return nil, fmt.Errorf("invalid origin configuration %q", item)
			}
			p.origins = append(p.origins, origin)
			p.single = item
		}
	}
	if len(p.origins) != 1 || p.allowNull {
		p.single = ""
	}
	return p, nil
}

// Match validates the Origin header and returns its CORS response value.
// Native clients may omit Origin; empty or repeated headers are invalid.
func (p *OriginPolicy) Match(r *http.Request) (string, bool) {
	values, present := r.Header["Origin"]
	if !present {
		if p.allowAll {
			return "*", true
		}
		return p.single, true
	}
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if value == "null" {
		return value, p.allowNull
	}
	if value != "" && value == p.single {
		if p.allowAll {
			return "*", true
		}
		return value, true
	}
	origin, ok := parseOrigin(value)
	if !ok {
		return "", false
	}
	if p.allowAll {
		return "*", true
	}
	for _, allowed := range p.origins {
		if origin.port == allowed.port && origin.scheme == allowed.scheme && origin.host == allowed.host {
			return value, true
		}
	}
	return "", false
}

func parseOrigin(value string) (normalizedOrigin, bool) {
	scheme, authority, found := strings.Cut(value, "://")
	if !found || strings.ContainsAny(authority, "/?#\\@% \t\r\n") {
		return normalizedOrigin{}, false
	}
	host, port, ok := originAuthority(authority)
	if !ok {
		return normalizedOrigin{}, false
	}
	origin := normalizedOrigin{scheme: strings.ToLower(scheme), port: 80}
	if origin.scheme == "https" {
		origin.port = 443
	} else if origin.scheme != "http" {
		return normalizedOrigin{}, false
	}
	normalizedHost, err := normalizeOriginHost(host)
	if err != nil {
		return normalizedOrigin{}, false
	}
	origin.host = normalizedHost
	if port != "" {
		parsed, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return normalizedOrigin{}, false
		}
		origin.port = uint16(parsed)
	}
	return origin, true
}

func originAuthority(authority string) (host, port string, valid bool) {
	if strings.HasPrefix(authority, "[") {
		host, suffix, found := strings.Cut(authority[1:], "]")
		if !found || !strings.Contains(host, ":") {
			return "", "", false
		}
		if suffix == "" {
			return host, "", true
		}
		if !strings.HasPrefix(suffix, ":") || len(suffix) == 1 {
			return "", "", false
		}
		return host, suffix[1:], true
	}
	host, port, found := strings.Cut(authority, ":")
	return host, port, !found || port != ""
}

func normalizeOriginHost(host string) (string, error) {
	if strings.Contains(host, ":") {
		address, err := netip.ParseAddr(host)
		if err != nil || address.Zone() != "" {
			return "", fmt.Errorf("invalid origin IP host")
		}
		return address.String(), nil
	}
	if host == "" || strings.ContainsAny(host, "[]%*") {
		return "", fmt.Errorf("invalid origin host")
	}
	return idna.Lookup.ToASCII(host)
}

// SetOriginPolicy shares one parsed policy with the gateway's HTTP middleware.
// Configure it before HTTP/WebSocket serving; RPC preparation may run concurrently.
func (rt *Router) SetOriginPolicy(policy *OriginPolicy) {
	rt.originPolicy.Store(policy)
}

func (rt *Router) configuredOriginPolicy() (*OriginPolicy, error) {
	if policy := rt.originPolicy.Load(); policy != nil {
		return policy, nil
	}
	if len(rt.WSOrigins) == 0 {
		return &defaultOriginPolicy, nil
	}
	configured := strings.Join(rt.WSOrigins, ",")
	if rt.WSAllowAllOrigins {
		configured += ",*"
	}
	return ParseOriginPolicy(configured)
}
