package subscriptioncompat

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/link"
)

// NodeShareLink converts normalized snapshot nodes only when each supplied
// option can be preserved by the native share-link and Xray JSON readers.
// Unknown transports and protocol-specific options remain available in Mihomo.
func NodeShareLink(node map[string]any) string {
	typ, _ := node["type"].(string)
	host, _ := node["server"].(string)
	if host == "" || strings.ContainsAny(host, "/?#@[]") {
		return ""
	}
	port := number(node["port"])
	if port < 1 || port > 65535 {
		return ""
	}
	if udp, exists := node["udp"]; exists && udp != true {
		return ""
	}
	common := "name type server port udp "
	target := url.URL{Scheme: typ, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	query := url.Values{}
	credential, _ := node["password"].(string)
	switch typ {
	case "ss":
		cipher, _ := node["cipher"].(string)
		if cipher == "" || credential == "" || strings.Contains(cipher, ":") || !allowedKeys(node, common+"cipher password") {
			return ""
		}
		credential = base64.RawURLEncoding.EncodeToString([]byte(cipher + ":" + credential))
	case "hysteria2", "trojan":
		if !allowedKeys(node, common+"password sni skip-cert-verify") {
			return ""
		}
		if skip, exists := node["skip-cert-verify"]; exists && skip != false {
			return ""
		}
		if sni, exists := node["sni"]; exists {
			value, ok := sni.(string)
			if !ok {
				return ""
			}
			query.Set("sni", value)
		}
	default:
		return ""
	}
	if credential == "" {
		return ""
	}
	target.User = url.User(credential)
	target.RawQuery = query.Encode()
	uri := target.String()
	if _, err := link.ParseLink(uri); err != nil {
		return ""
	}
	return uri
}
