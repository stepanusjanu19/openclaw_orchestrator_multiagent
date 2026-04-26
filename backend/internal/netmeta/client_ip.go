package netmeta

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"multiagent-platform/internal/config"
)

type ClientIP struct {
	Addr              netip.Addr
	ProxyChainDepth   int
	TrustedProxyChain bool
	RealIPSource      string
	GuardProvider     string
	RemoteAddr        netip.Addr
	IgnoredHeaderRisk bool
	ForwardedDepth    int
}

func ClientIPFromRequest(r *http.Request, cfg *config.Config) ClientIP {
	remote := RemoteAddrIP(r.RemoteAddr)
	out := ClientIP{Addr: remote, RemoteAddr: remote, IgnoredHeaderRisk: HasForwardedHeaders(r)}
	if !TrustedProxyRequest(r, cfg) {
		return out
	}
	out.TrustedProxyChain = true
	out.GuardProvider = GuardProvider(r)
	out.IgnoredHeaderRisk = false
	out.ForwardedDepth = forwardedDepth(r, 0)
	if addr, ok := singleIPHeader(r, "CF-Connecting-IP"); ok {
		out.Addr = addr
		out.ProxyChainDepth = forwardedDepth(r, 1)
		out.RealIPSource = "cf_connecting_ip"
		return out
	}
	if addr, ok := singleIPHeader(r, "True-Client-IP"); ok {
		out.Addr = addr
		out.ProxyChainDepth = forwardedDepth(r, 1)
		out.RealIPSource = "true_client_ip"
		return out
	}
	if addr, ok := singleIPHeader(r, "X-Real-IP"); ok {
		out.Addr = addr
		out.ProxyChainDepth = forwardedDepth(r, 1)
		out.RealIPSource = "x_real_ip"
		return out
	}
	if chain := ForwardedForChain(r.Header.Get("X-Forwarded-For")); len(chain) > 0 {
		out.Addr = chain[0]
		out.ProxyChainDepth = len(chain)
		out.RealIPSource = "x_forwarded_for"
		return out
	}
	if chain := ForwardedChain(r.Header.Get("Forwarded")); len(chain) > 0 {
		out.Addr = chain[0]
		out.ProxyChainDepth = len(chain)
		out.RealIPSource = "forwarded"
		return out
	}
	return out
}

func ClientIPString(r *http.Request, cfg *config.Config) string {
	ip := ClientIPFromRequest(r, cfg)
	if ip.Addr.IsValid() {
		return ip.Addr.String()
	}
	return "unknown"
}

func TrustedProxyRequest(r *http.Request, cfg *config.Config) bool {
	remote := RemoteAddrIP(r.RemoteAddr)
	if !remote.IsValid() {
		return false
	}
	if remote.IsLoopback() {
		return true
	}
	for _, prefix := range ParseCIDRList(cfgString(cfg, "trusted")) {
		if prefix.Contains(remote) {
			return true
		}
	}
	return false
}

func RemoteAddrIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func ForwardedForChain(raw string) []netip.Addr {
	parts := strings.Split(raw, ",")
	out := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err == nil {
			out = append(out, addr.Unmap())
		}
	}
	return out
}

func ForwardedChain(raw string) []netip.Addr {
	entries := strings.Split(raw, ",")
	out := make([]netip.Addr, 0, len(entries))
	for _, entry := range entries {
		for _, param := range strings.Split(entry, ";") {
			key, val, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "for") {
				continue
			}
			if addr, ok := parseForwardedAddr(val); ok {
				out = append(out, addr)
			}
		}
	}
	return out
}

func parseForwardedAddr(raw string) (netip.Addr, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), `"`)
	if raw == "" || strings.HasPrefix(raw, "_") || strings.EqualFold(raw, "unknown") {
		return netip.Addr{}, false
	}
	if strings.HasPrefix(raw, "[") {
		if end := strings.Index(raw, "]"); end > 0 {
			raw = raw[1:end]
		}
	} else if strings.Count(raw, ":") == 1 {
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		}
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func singleIPHeader(r *http.Request, key string) (netip.Addr, bool) {
	raw := strings.TrimSpace(r.Header.Get(key))
	if raw == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func forwardedDepth(r *http.Request, fallback int) int {
	if chain := ForwardedForChain(r.Header.Get("X-Forwarded-For")); len(chain) > 0 {
		return len(chain)
	}
	if chain := ForwardedChain(r.Header.Get("Forwarded")); len(chain) > 0 {
		return len(chain)
	}
	return fallback
}

func HasForwardedHeaders(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, key := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP"} {
		if strings.TrimSpace(r.Header.Get(key)) != "" {
			return true
		}
	}
	return false
}

func ForwardedHeaderSummary(r *http.Request) map[string]interface{} {
	if r == nil {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"has_forwarded":        strings.TrimSpace(r.Header.Get("Forwarded")) != "",
		"forwarded_depth":      len(ForwardedChain(r.Header.Get("Forwarded"))),
		"has_x_forwarded_for":  strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "",
		"x_forwarded_depth":    len(ForwardedForChain(r.Header.Get("X-Forwarded-For"))),
		"has_x_real_ip":        strings.TrimSpace(r.Header.Get("X-Real-IP")) != "",
		"has_cf_connecting_ip": strings.TrimSpace(r.Header.Get("CF-Connecting-IP")) != "",
		"has_true_client_ip":   strings.TrimSpace(r.Header.Get("True-Client-IP")) != "",
		"has_cf_ray":           strings.TrimSpace(r.Header.Get("CF-Ray")) != "",
		"has_via":              strings.TrimSpace(r.Header.Get("Via")) != "",
	}
}

func GuardProvider(r *http.Request) string {
	if r == nil {
		return ""
	}
	if r.Header.Get("CF-Ray") != "" || r.Header.Get("CF-Connecting-IP") != "" || r.Header.Get("CF-IPCountry") != "" {
		return "cloudflare"
	}
	host := strings.ToLower(r.Host)
	for _, key := range []string{"Via", "X-Forwarded-Host", "Forwarded"} {
		if strings.Contains(strings.ToLower(r.Header.Get(key)), "ngrok") {
			return "ngrok"
		}
	}
	if strings.Contains(host, "ngrok") {
		return "ngrok"
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Via")), "adguard") || strings.Contains(strings.ToLower(r.UserAgent()), "adguard") {
		return "adguard"
	}
	return ""
}

type TrafficAssessment struct {
	Class   string
	Score   int
	Signals []string
}

func ClassifyTraffic(r *http.Request, cfg *config.Config) TrafficAssessment {
	score := 0
	signals := []string{}
	add := func(points int, signal string) {
		score += points
		signals = append(signals, signal)
	}
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent()))
	path := strings.ToLower(r.URL.Path)
	if ua == "" {
		add(25, "missing_user_agent")
	} else if looksLikeScraperUA(ua) {
		add(55, "scraper_user_agent")
	} else if looksLikeAutomationUA(ua) {
		add(30, "automation_user_agent")
	}
	if strings.HasPrefix(path, "/api/webhook/") {
		if r.Method == http.MethodPost && !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			add(35, "webhook_post_non_json")
		}
		if r.Method == http.MethodGet && strings.TrimSpace(r.Header.Get("Accept-Language")) == "" {
			add(10, "browser_language_missing")
		}
	}
	if len(r.URL.RawQuery) > 512 {
		add(25, "large_query_string")
	}
	if strings.Contains(path, "..") || strings.Contains(path, "wp-admin") || strings.Contains(path, ".env") || strings.Contains(path, "php") {
		add(55, "path_probe")
	}
	if HasForwardedHeaders(r) && !TrustedProxyRequest(r, cfg) {
		add(30, "untrusted_forwarded_headers")
	}
	if GuardProvider(r) != "" && !TrustedProxyRequest(r, cfg) {
		add(15, "guard_header_untrusted")
	}
	class := "unknown"
	switch {
	case score >= 85:
		class = "abuse_likely"
	case score >= 60:
		class = "scraper_likely"
	case score >= 35:
		class = "automation_likely"
	case score <= 10 && ua != "" && strings.TrimSpace(r.Header.Get("Accept")) != "":
		class = "human_likely"
	}
	return TrafficAssessment{Class: class, Score: score, Signals: signals}
}

func looksLikeAutomationUA(ua string) bool {
	needles := []string{"curl", "wget", "python-requests", "httpclient", "okhttp", "go-http-client", "node-fetch", "axios", "postman", "insomnia"}
	for _, needle := range needles {
		if strings.Contains(ua, needle) {
			return true
		}
	}
	return false
}

func looksLikeScraperUA(ua string) bool {
	needles := []string{"scrapy", "spider", "crawler", "bot", "headless", "phantomjs", "selenium", "playwright"}
	for _, needle := range needles {
		if strings.Contains(ua, needle) {
			return true
		}
	}
	return false
}

func ParseCIDRList(raw string) []netip.Prefix {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]netip.Prefix, 0, len(fields))
	for _, field := range fields {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(field))
		if err == nil {
			out = append(out, prefix.Masked())
		}
	}
	return out
}

func cfgString(cfg *config.Config, key string) string {
	if cfg == nil {
		return ""
	}
	switch key {
	case "trusted":
		return cfg.GatewayTrustedProxyCIDRs
	default:
		return ""
	}
}
