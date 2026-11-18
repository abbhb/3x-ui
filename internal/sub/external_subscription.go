package sub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// External subscription fetching: a remote URL whose body is a share-link
// list. Fetches are cached briefly and bounded so a dead provider can't stall.

const (
	subscriptionCacheTTL      = 5 * time.Minute
	subscriptionMaxBytes      = 2 << 20 // 2 MiB
	subscriptionMaxLinks      = 1000
	subscriptionCacheCapacity = 256
)

// The provider picks every redirect target, so each hop is resolved, checked and dialed by
// the SSRF guard; its headers are bounded like its body.
var subscriptionHTTPClient = &http.Client{
	Timeout: 6 * time.Second,
	Transport: &http.Transport{
		DialContext:            netsafe.SSRFGuardedDialContext,
		TLSHandshakeTimeout:    5 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        90 * time.Second,
	},
}

// subscriptionProxy is the host's proxy setting: the proxy is the operator's and does the
// dialling, so a proxied fetch goes through it as the default transport did.
var subscriptionProxy = http.ProxyFromEnvironment

var proxiedSubscriptionHTTPClient = &http.Client{
	Timeout: 6 * time.Second,
	Transport: &http.Transport{
		Proxy:                  func(r *http.Request) (*url.URL, error) { return subscriptionProxy(r) },
		TLSHandshakeTimeout:    5 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        90 * time.Second,
	},
}

// subscriptionClientFor picks the proxied client when the host's proxy setting covers req.
func subscriptionClientFor(req *http.Request) *http.Client {
	if proxyURL, err := subscriptionProxy(req); err == nil && proxyURL != nil {
		return proxiedSubscriptionHTTPClient
	}
	return subscriptionHTTPClient
}

// operatorChosePrivateHost reports a URL naming a private address itself: the operator picked
// it. A hostname does not count, since the provider controls what its DNS answers.
func operatorChosePrivateHost(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return ip != nil && netsafe.IsBlockedIP(ip)
}

type subscriptionContent struct {
	links []string
	nodes []expandedLink
}

type subscriptionCacheEntry struct {
	subscriptionContent
	fetchedAt time.Time
}

type subscriptionFetch struct {
	done chan struct{}
	subscriptionContent
}

var subscriptionCache = struct {
	sync.Mutex
	m        map[string]subscriptionCacheEntry
	inflight map[string]*subscriptionFetch
}{
	m:        make(map[string]subscriptionCacheEntry),
	inflight: make(map[string]*subscriptionFetch),
}

// subscriptionFetchResult reports whether this caller performed the network
// fetch, so only it records status and cache hits stay read-only.
type subscriptionFetchResult struct {
	subscriptionContent
	fetched bool
	err     error
}

// fetchSubscriptionLinks returns the share links contained in a remote
// subscription URL, using a short-lived cache. On any failure it returns the
// last cached value (if present) or nil — never an error, so the rest of the
// client's subscription still renders.
func fetchSubscriptionLinks(rawURL string) subscriptionFetchResult {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return subscriptionFetchResult{}
	}

	subscriptionCache.Lock()
	cached, ok := subscriptionCache.m[rawURL]
	if ok && time.Since(cached.fetchedAt) < subscriptionCacheTTL {
		subscriptionCache.Unlock()
		return subscriptionFetchResult{subscriptionContent: cached.subscriptionContent}
	}
	if fetch, waiting := subscriptionCache.inflight[rawURL]; waiting {
		subscriptionCache.Unlock()
		<-fetch.done
		return subscriptionFetchResult{subscriptionContent: fetch.subscriptionContent}
	}
	fetch := &subscriptionFetch{done: make(chan struct{})}
	subscriptionCache.inflight[rawURL] = fetch
	subscriptionCache.Unlock()
	defer func() {
		subscriptionCache.Lock()
		close(fetch.done)
		delete(subscriptionCache.inflight, rawURL)
		subscriptionCache.Unlock()
	}()

	content, err := fetchSubscriptionContent(rawURL)
	if err != nil {
		if ok {
			fetch.subscriptionContent = cached.subscriptionContent
		}
		return subscriptionFetchResult{subscriptionContent: fetch.subscriptionContent, fetched: true, err: err}
	}

	subscriptionCache.Lock()
	subscriptionCache.m[rawURL] = subscriptionCacheEntry{subscriptionContent: content, fetchedAt: time.Now()}
	trimSubscriptionCacheLocked(rawURL)
	subscriptionCache.Unlock()
	fetch.subscriptionContent = content
	return subscriptionFetchResult{subscriptionContent: content, fetched: true}
}

func trimSubscriptionCacheLocked(keep string) {
	for len(subscriptionCache.m) > subscriptionCacheCapacity {
		var oldestURL string
		var oldest time.Time
		for rawURL, entry := range subscriptionCache.m {
			if rawURL == keep {
				continue
			}
			if oldestURL == "" || entry.fetchedAt.Before(oldest) {
				oldestURL = rawURL
				oldest = entry.fetchedAt
			}
		}
		if oldestURL == "" {
			return
		}
		delete(subscriptionCache.m, oldestURL)
	}
}

// recordExternalSubscriptionFetch stamps status on every row holding this URL,
// keyed by value because row ids churn on save and the cache is per URL.
func recordExternalSubscriptionFetch(rawURL string, fetchErr error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return
	}
	lastFetchError := ""
	if fetchErr != nil {
		lastFetchError = common.TruncateStatusText(service.WithoutRequestURL(fetchErr).Error())
	}
	if err := database.GetDB().
		Model(&model.ClientExternalLink{}).
		Where("kind = ? AND value = ?", model.ExternalLinkKindSubscription, rawURL).
		Updates(map[string]any{
			"last_fetch_at":    time.Now().UnixMilli(),
			"last_fetch_error": lastFetchError,
		}).Error; err != nil {
		logger.Warningf("sub: recording fetch status for an external subscription: %v", err)
	}
}

func fetchSubscriptionContent(rawURL string) (subscriptionContent, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		return subscriptionContent{}, service.WithoutRequestURL(err)
	}
	// A provider on the operator's own network is fetched; the hops a public provider picks by
	// redirecting stay held to public addresses.
	req = req.WithContext(netsafe.ContextWithAllowPrivate(req.Context(), operatorChosePrivateHost(req.URL)))
	// Some providers gate the link body on a known client User-Agent.
	req.Header.Set("User-Agent", externalSubUserAgent())
	// A 3x-ui donor with an HWID limit answers 404 when the header is empty (#6559).
	if hwid := service.ExternalSubscriptionHwid(); hwid != "" {
		req.Header.Set("X-HWID", hwid)
	}
	resp, err := subscriptionClientFor(req).Do(req)
	if err != nil {
		return subscriptionContent{}, service.WithoutRequestURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return subscriptionContent{}, errBadStatus
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, subscriptionMaxBytes+1))
	if err != nil {
		return subscriptionContent{}, err
	}
	if len(body) > subscriptionMaxBytes {
		return subscriptionContent{}, errSubscriptionBodyTooLarge
	}
	if bytes.Contains(body, []byte("proxies")) && looksLikeYAML(body) {
		snapshot, err := subscriptioncompat.ParseSnapshot(body)
		if err != nil {
			return subscriptionContent{}, err
		}
		nodes := make([]expandedLink, 0, len(snapshot.Nodes))
		for _, n := range snapshot.Nodes {
			// These keys are trusted bookkeeping for local inbounds only.
			delete(n.Payload, "_mgma-inbound-id")
			delete(n.Payload, "_mgma-host-id")
			payload, err := json.Marshal(n.Payload)
			if err != nil {
				return subscriptionContent{}, err
			}
			nodes = append(nodes, expandedLink{Link: subscriptioncompat.NodeShareLink(n.Payload), Name: n.Name, Proxy: payload})
		}
		return subscriptionContent{nodes: nodes}, nil
	}
	links := decodeSubscriptionBody(body)
	if len(links) > subscriptionMaxLinks {
		logger.Warningf("sub: an external subscription lists %d share links; serving the first %d", len(links), subscriptionMaxLinks)
		links = links[:subscriptionMaxLinks]
	}
	return subscriptionContent{links: links}, nil
}

// A YAML document can contain individual URIs inside rules or comments. Only
// its top-level proxies are eligible for a native subscription.
func looksLikeYAML(raw []byte) bool {
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		scheme, _, _ := strings.Cut(line, "://")
		return !slices.Contains(serverShareLinkSchemes, strings.ToLower(scheme))
	}
	return false
}

var (
	errBadStatus                = &subError{"non-2xx subscription response"}
	errSubscriptionBodyTooLarge = &subError{"subscription response body exceeds size limit"}
)

// externalSubUserAgent returns the panel setting for external subscription
// fetches, or the historical client UA when it is unset or the DB is unreachable.
func externalSubUserAgent() string {
	if database.GetDB() == nil {
		return service.DefaultExternalSubUserAgent
	}
	ua, err := (&service.SettingService{}).GetExternalSubUserAgent()
	if err != nil {
		return service.DefaultExternalSubUserAgent
	}
	return ua
}

type subError struct{ msg string }

func (e *subError) Error() string { return e.msg }

// serverShareLinkSchemes are the server links a raw subscription carries on: what the link
// parser reads, what the panel itself writes, and common third-party server schemes.
var serverShareLinkSchemes = []string{
	"vmess", "vless", "trojan", "ss", "ssr", "hysteria", "hysteria2", "hy2", "tuic", "wireguard", "wg",
	"vpn", "socks", "socks5", "anytls", "juicity", "naive+https", "tg",
}

// decodeSubscriptionBody handles the common base64-encoded newline list as well as a
// plain-text body, keeping only server share links: app deeplinks and plain URLs would
// reach the subscriber's app as directives the operator never chose.
func decodeSubscriptionBody(body []byte) []string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil
	}
	if decoded, ok := tryDecodeBase64Body(text); ok {
		text = strings.TrimSpace(decoded)
	}
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		scheme, _, found := strings.Cut(ln, "://")
		if found && slices.ContainsFunc(serverShareLinkSchemes, func(s string) bool { return strings.EqualFold(s, scheme) }) {
			out = append(out, ln)
		}
	}
	return out
}

func tryDecodeBase64Body(s string) (string, bool) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
	if b, err := base64.StdEncoding.DecodeString(padBase64Sub(clean)); err == nil {
		return string(b), true
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(clean, "=")); err == nil {
		return string(b), true
	}
	return "", false
}
