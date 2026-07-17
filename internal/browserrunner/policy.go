// Package browserrunner provides Hecate's deliberately narrow native browser
// inspection capability. It does not expose browser interaction primitives to
// an agent: a caller can ask it to load one URL and receive bounded,
// text-only evidence from a fresh browser profile.
package browserrunner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// browserNonPublicPrefixes supplements net.IP's category helpers with IANA
// special-purpose, documentation, benchmarking, and reserved ranges that can
// be globally-unicast-shaped but must not be treated as browser destinations.
// This is deliberately conservative admission control for the local browser
// feature, not an egress firewall.
var browserNonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

const DefaultTimeout = 20 * time.Second

var (
	// ErrUnavailable means no explicit local browser runtime was configured.
	ErrUnavailable = errors.New("browser inspection is unavailable")
	// ErrInvalidURL means the requested URL is not a normal HTTP(S) URL.
	ErrInvalidURL = errors.New("browser inspection URL is invalid")
	// ErrOriginNotAllowed means the requested or final origin is outside the
	// exact operator-configured allowlist.
	ErrOriginNotAllowed = errors.New("browser inspection origin is not allowed")
	// ErrPrivateNetwork means a configured browser origin resolved to a
	// loopback, private, link-local, or otherwise non-public address while the
	// runtime's explicit private-network opt-in remains off.
	ErrPrivateNetwork = errors.New("browser inspection private network destination is not allowed")
	// ErrInspectionFailed is intentionally generic. Browser diagnostics can
	// contain operator data and must not be surfaced to models or persisted.
	ErrInspectionFailed = errors.New("browser inspection failed")
)

// Inspector is the runtime seam used by the orchestration loop. Production
// uses ChromiumInspector; tests can supply a deterministic implementation.
type Inspector interface {
	Inspect(context.Context, InspectRequest) (InspectResult, error)
}

// InspectRequest admits one GET page load. URL may contain a path, but all
// navigations and subresource requests must remain on one of AllowedOrigins
// and use only GET or HEAD. This deliberately blocks page-initiated POST/PUT/
// PATCH/DELETE calls before they can make browser evidence mutate an allowed
// application.
type InspectRequest struct {
	URL            string
	AllowedOrigins []string
}

// InspectResult is intentionally text-only. It contains no screenshots,
// cookies, downloaded content, raw CDP messages, or browser profile state.
type InspectResult struct {
	FinalURL      string
	FinalOrigin   string
	Title         string
	Accessibility []AccessibilityNode
	Console       []ConsoleMessage
	Network       NetworkSummary
}

type AccessibilityNode struct {
	Role        string
	Name        string
	Description string
	Value       string
}

type ConsoleMessage struct {
	Level string
	Text  string
}

type NetworkSummary struct {
	Requests        int
	Navigations     int
	BlockedRequests int
}

// Config requires an explicit browser executable. Hecate deliberately does
// not search PATH, attach to a running browser, or download Chromium.
type Config struct {
	ExecutablePath string
	Timeout        time.Duration
	// AllowPrivateIPs is intentionally false by default. Exact origin policy
	// prevents cross-origin browsing, but it is not a substitute for an
	// operator explicitly deciding that a local/private browser destination is
	// acceptable.
	AllowPrivateIPs bool
}

type requestPolicy struct {
	targetURL string
	allowed   map[string]struct{}
}

type lookupIPAddrs func(context.Context, string) ([]net.IPAddr, error)

type browserHostMapping struct {
	Hostname string
	Address  string
}

// preflightHostMappings resolves each allowed hostname once and returns a
// numeric Chromium resolver map for it. This narrows the DNS-rebinding window
// between Hecate's private-IP admission check and Chromium's own navigation;
// it is defense in depth, not a substitute for host firewall or egress policy.
func (p requestPolicy) preflightHostMappings(ctx context.Context, allowPrivateIPs bool, lookup lookupIPAddrs) ([]browserHostMapping, error) {
	if lookup == nil {
		return nil, ErrInspectionFailed
	}
	origins := make([]string, 0, len(p.allowed))
	for origin := range p.allowed {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	seenHosts := make(map[string]struct{}, len(origins))
	mappings := make([]browserHostMapping, 0, len(origins))
	for _, origin := range origins {
		parsed, err := parseInspectableURL(origin)
		if err != nil {
			return nil, ErrInspectionFailed
		}
		hostname := strings.ToLower(parsed.Hostname())
		if _, seen := seenHosts[hostname]; seen {
			continue
		}
		seenHosts[hostname] = struct{}{}
		if literal := net.ParseIP(hostname); literal != nil {
			if !allowPrivateIPs && !isPublicBrowserIP(literal) {
				return nil, ErrPrivateNetwork
			}
			continue
		}
		if !safeHostResolverHostname(hostname) {
			return nil, ErrInspectionFailed
		}
		addresses, err := lookup(ctx, hostname)
		if err != nil || len(addresses) == 0 {
			return nil, ErrInspectionFailed
		}
		address, err := chooseBrowserAddress(addresses, allowPrivateIPs)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, browserHostMapping{Hostname: hostname, Address: address})
	}
	return mappings, nil
}

func chooseBrowserAddress(addresses []net.IPAddr, allowPrivateIPs bool) (string, error) {
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address.IP == nil {
			continue
		}
		if !allowPrivateIPs && !isPublicBrowserIP(address.IP) {
			return "", ErrPrivateNetwork
		}
		values = append(values, address.IP.String())
	}
	if len(values) == 0 {
		return "", ErrInspectionFailed
	}
	sort.Strings(values)
	return values[0], nil
}

func safeHostResolverHostname(hostname string) bool {
	// Chromium resolver rules treat `*` as a hostname pattern. Browser evidence
	// needs a literal configured hostname, so reject it with the separators that
	// could otherwise form an additional rule as well.
	return hostname != "" && !strings.ContainsAny(hostname, ",* \t\r\n")
}

func hostResolverRules(mappings []browserHostMapping) (string, error) {
	if len(mappings) == 0 {
		return "", nil
	}
	rules := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if !safeHostResolverHostname(mapping.Hostname) {
			return "", ErrInspectionFailed
		}
		ip := net.ParseIP(mapping.Address)
		if ip == nil {
			return "", ErrInspectionFailed
		}
		address := ip.String()
		if strings.Contains(address, ":") {
			address = "[" + address + "]"
		}
		rules = append(rules, "MAP "+mapping.Hostname+" "+address)
	}
	return strings.Join(rules, ", "), nil
}

func isPublicBrowserIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, prefix := range browserNonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func newRequestPolicy(req InspectRequest) (requestPolicy, error) {
	target, err := parseInspectionTargetURL(req.URL)
	if err != nil {
		return requestPolicy{}, err
	}
	allowedOrigins, err := NormalizeAllowedOrigins(req.AllowedOrigins)
	if err != nil || len(allowedOrigins) == 0 {
		return requestPolicy{}, fmt.Errorf("%w: no valid allowed origins", ErrOriginNotAllowed)
	}
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	if _, ok := allowed[originForURL(target)]; !ok {
		return requestPolicy{}, ErrOriginNotAllowed
	}
	return requestPolicy{targetURL: target.String(), allowed: allowed}, nil
}

func (p requestPolicy) allowsURL(raw string) bool {
	parsed, err := parseInspectableURL(raw)
	if err != nil {
		return false
	}
	_, ok := p.allowed[originForURL(parsed)]
	return ok
}

func (p requestPolicy) allowsRequest(rawURL, method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method != "GET" && method != "HEAD" {
		return false
	}
	return p.allowsURL(rawURL)
}

// NormalizeAllowedOrigins accepts exact HTTP(S) origins, canonicalizes them,
// removes duplicates, and sorts the result to make persisted configuration and
// approvals deterministic.
func NormalizeAllowedOrigins(origins []string) ([]string, error) {
	if len(origins) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(origins))
	for _, raw := range origins {
		origin, err := NormalizeOrigin(raw)
		if err != nil {
			return nil, err
		}
		seen[origin] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for origin := range seen {
		out = append(out, origin)
	}
	sort.Strings(out)
	return out, nil
}

// NormalizeOrigin accepts only a scheme, host, and optional port. A trailing
// slash is tolerated so a copied browser origin works, but paths, queries,
// fragments, and credentials are rejected rather than silently broadened.
func NormalizeOrigin(raw string) (string, error) {
	parsed, err := parseInspectableURL(raw)
	if err != nil {
		return "", err
	}
	if hostname := parsed.Hostname(); net.ParseIP(hostname) == nil && !safeHostResolverHostname(hostname) {
		return "", fmt.Errorf("%w: allowed origin must use a literal hostname", ErrInvalidURL)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%w: allowed origin must not include a path", ErrInvalidURL)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", fmt.Errorf("%w: allowed origin must not include a query or fragment", ErrInvalidURL)
	}
	return originForURL(parsed), nil
}

// OriginForURL returns the normalized origin for a page URL. Unlike
// NormalizeOrigin it accepts paths, queries, and fragments because those can
// occur in browser redirects and subresource requests; callers should persist
// only the returned origin.
func OriginForURL(raw string) (string, error) {
	parsed, err := parseInspectableURL(raw)
	if err != nil {
		return "", err
	}
	return originForURL(parsed), nil
}

// InspectionOriginForURL returns the normalized origin for a browser evidence
// target. This narrow first slice rejects credentials, queries, and fragments:
// assistant tool-call arguments are retained in run checkpoints and events,
// so accepting a query here could persist an accidental secret. Redirects and
// subresources can still use query strings; only the requested target URL is
// constrained.
func InspectionOriginForURL(raw string) (string, error) {
	parsed, err := parseInspectionTargetURL(raw)
	if err != nil {
		return "", err
	}
	return originForURL(parsed), nil
}

// RedactURL returns an operator-safe URL for text evidence. It removes
// credentials, query strings, and fragments before the value can reach a
// task artifact, trace, or model conversation.
func RedactURL(raw string) string {
	parsed, err := parseInspectableURL(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func parseInspectableURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrInvalidURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrInvalidURL
	}
	if parsed.Hostname() == "" {
		return nil, ErrInvalidURL
	}
	return parsed, nil
}

func parseInspectionTargetURL(raw string) (*url.URL, error) {
	parsed, err := parseInspectableURL(raw)
	if err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return nil, fmt.Errorf("%w: browser inspection URL must not include a query or fragment", ErrInvalidURL)
	}
	return parsed, nil
}

func originForURL(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host
}
