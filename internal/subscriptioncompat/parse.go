package subscriptioncompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxSourceBytes    = 2 << 20
	MaxSourceNodes    = 1000
	MaxPlanNodes      = 5000
	MaxPlanBytes      = 8 << 20
	MaxPrivateSources = 10
	MaxPrivateNodes   = 1000
	MaxPrivateBytes   = 2 << 20
)

type ParsedNode struct {
	Key, Name, Type string
	ShareLink       string
	Payload         map[string]any
	Position        int
}
type Snapshot struct {
	Nodes   []ParsedNode
	Skipped int
}

var (
	allowedTypes = map[string]bool{"http": true, "snell": true, "socks5": true, "ss": true, "ssr": true, "trojan": true, "vmess": true, "anytls": true, "hysteria": true, "hysteria2": true, "masque": true, "mieru": true, "ssh": true, "tuic": true, "vless": true, "wireguard": true}
	references   = map[string]bool{"dialer-proxy": true, "detour": true, "outbound": true, "outbounds": true, "proxy-provider": true, "provider": true, "use": true}
)

func cleanText(s string, max int) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) })
}

func boundedValue(value any, depth int, items *int, denyReferences bool) bool {
	*items++
	if depth > 16 || *items > 50000 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if !cleanText(key, 1024) || key == "<<" || (denyReferences && references[key]) || !boundedValue(child, depth+1, items, denyReferences) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !boundedValue(child, depth+1, items, denyReferences) {
				return false
			}
		}
	case string:
		return utf8.ValidString(v) && len(v) <= 65536 && !strings.ContainsRune(v, '\x00')
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	case nil, bool, int, int64, uint64, json.Number:
	default:
		return false
	}
	return true
}

func DecodeDocument(raw []byte) (map[string]any, error) {
	if len(raw) > MaxSourceBytes || !utf8.Valid(raw) {
		return nil, ErrInvalidSnapshot
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	tokens := lexer.Tokenize(string(raw))
	if len(tokens) > 100000 {
		return nil, ErrInvalidSnapshot
	}
	flow := 0
	indents := []int{0}
	line := 0
	for _, t := range tokens {
		switch t.Type.String() {
		case "Anchor", "Alias", "Tag", "MergeKey":
			return nil, ErrInvalidSnapshot
		case "MappingStart", "SequenceStart":
			flow++
		case "MappingEnd", "SequenceEnd":
			flow--
		}
		if flow > 16 {
			return nil, ErrInvalidSnapshot
		}
		if t.Position.Line != line {
			line = t.Position.Line
			c := t.Position.Column
			for len(indents) > 1 && c < indents[len(indents)-1] {
				indents = indents[:len(indents)-1]
			}
			if c > indents[len(indents)-1] {
				indents = append(indents, c)
			}
			if len(indents) > 20 {
				return nil, ErrInvalidSnapshot
			}
		}
	}
	var doc map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&doc) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || doc == nil {
		return nil, ErrInvalidSnapshot
	}
	items := 0
	if !boundedValue(doc, 0, &items, false) {
		return nil, ErrInvalidSnapshot
	}
	return doc, nil
}

func normalizeNode(v any) (map[string]any, error) {
	node, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalidSnapshot
	}
	n, _ := node["name"].(string)
	name := norm.NFKC.String(strings.TrimSpace(n))
	typ, _ := node["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))
	server, _ := node["server"].(string)
	server = strings.TrimSpace(server)
	port := 0
	switch p := node["port"].(type) {
	case int:
		port = p
	case uint64:
		if p <= 65535 {
			port = int(p)
		}
	case int64:
		if p <= 65535 {
			port = int(p)
		}
	case float64:
		if p == math.Trunc(p) && p <= 65535 {
			port = int(p)
		}
	case json.Number:
		port, _ = strconv.Atoi(string(p))
	case string:
		port, _ = strconv.Atoi(p)
	}
	if !cleanText(name, 256) || !allowedTypes[typ] || !cleanText(server, 512) || port < 1 || port > 65535 {
		return nil, ErrInvalidSnapshot
	}
	items := 0
	if !boundedValue(node, 0, &items, true) {
		return nil, ErrInvalidSnapshot
	}
	node["name"], node["type"], node["server"], node["port"] = name, typ, server, port
	b, err := json.Marshal(node)
	if err != nil || len(b) > 256<<10 {
		return nil, ErrInvalidSnapshot
	}
	return node, nil
}

func ParseSnapshot(raw []byte) (Snapshot, error) {
	var result Snapshot
	doc, err := DecodeDocument(raw)
	if err != nil {
		return result, err
	}
	nodes, ok := doc["proxies"].([]any)
	if !ok || len(nodes) > MaxSourceNodes {
		return result, ErrInvalidSnapshot
	}
	return normalizeSnapshot(nodes, nil)
}

// ShareLinkSnapshot uses the same identities and payload limits as YAML.
func ShareLinkSnapshot(nodes []any, links []string) (Snapshot, error) {
	if len(links) != len(nodes) || len(nodes) > MaxSourceNodes {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return normalizeSnapshot(nodes, links)
}

func normalizeSnapshot(nodes []any, links []string) (Snapshot, error) {
	var result Snapshot
	counts := map[string]int{}
	for pos, raw := range nodes {
		node, err := normalizeNode(raw)
		if err != nil {
			result.Skipped++
			continue
		}
		tuple := []any{node["name"], node["type"], node["server"], node["port"]}
		keyBase, _ := canonicalJSON(tuple)
		n := counts[string(keyBase)]
		counts[string(keyBase)]++
		tuple = append(tuple, n)
		keyBytes, _ := canonicalJSON(tuple)
		hash := sha256.Sum256(keyBytes)
		entry := ParsedNode{Key: hex.EncodeToString(hash[:]), Name: node["name"].(string), Type: node["type"].(string), Payload: node, Position: pos}
		if links != nil {
			entry.ShareLink = links[pos]
		}
		result.Nodes = append(result.Nodes, entry)
	}
	if len(result.Nodes) == 0 {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return result, nil
}

func hashText(s string) string { h := sha256.Sum256([]byte(s)); return fmt.Sprintf("%x", h) }

func canonicalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), err
}
