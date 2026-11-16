package subscriptioncompat

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
)

var ErrFetch = errors.New("external_upstream_unavailable")

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00") || strings.Contains(u.Hostname(), "%") {
		return ErrInvalid
	}
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
			return ErrInvalid
		}
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (!ip.IsGlobalUnicast() || netsafe.IsBlockedIP(ip)) {
		return ErrInvalid
	}
	return nil
}

func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrFetch
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 || len(ips) > 64 {
		return nil, ErrFetch
	}
	for _, ip := range ips {
		if !ip.IP.IsGlobalUnicast() || netsafe.IsBlockedIP(ip.IP) {
			return nil, ErrFetch
		}
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, ErrFetch
}

func FetchPublic(ctx context.Context, raw string) ([]byte, error) {
	if ValidateURL(raw) != nil {
		return nil, ErrFetch
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: publicDial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 4 * time.Second, MaxResponseHeaderBytes: 64 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || ValidateURL(req.URL.String()) != nil {
			return ErrFetch
		}
		req.Header.Del("Referer")
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, ErrFetch
	}
	req.Header.Set("User-Agent", "clash.meta")
	req.Header.Set("Accept", "application/yaml, text/yaml, text/plain, */*")
	response, err := client.Do(req)
	if err != nil {
		return nil, ErrFetch
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrFetch
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxSourceBytes+1))
	if err != nil || len(body) > MaxSourceBytes {
		return nil, ErrFetch
	}
	return body, nil
}
