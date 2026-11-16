package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type HostInput struct {
	MihomoOnly   *bool  `json:"mihomo_only"`
	Revision     int64  `json:"revision"`
	CascadeMode  string `json:"cascade_mode"`
	EntryNodeID  *int   `json:"entry_node_id"`
	EntryYAML    string `json:"entry_yaml,omitempty"`
	ClientStream string `json:"client_stream"`
	ClearEntry   bool   `json:"clear_entry"`
}

func publicEntry(tx *gorm.DB, id int) (map[string]any, error) {
	var node model.SubscriptionNode
	if tx.Where("id = ? AND is_active = ?", id, true).First(&node).Error != nil {
		return nil, ErrCascade
	}
	source, err := sourceFor(tx, node.SourceID, nil)
	if err != nil || source.OwnerAccountID != nil || !source.Enabled {
		return nil, ErrCascade
	}
	return decryptedNode(node)
}

func decryptedNode(node model.SubscriptionNode) (map[string]any, error) {
	raw, err := Decrypt("proxy", node.PayloadCiphertext)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrEncryptedUnavailable
	}
	payload, err := normalizeNode(value)
	if err != nil {
		return nil, ErrEncryptedUnavailable
	}
	return payload, nil
}

func (s *Service) Host(id int) (model.SubscriptionHost, error) {
	var host model.Host
	if s.DB.First(&host, id).Error != nil {
		return model.SubscriptionHost{}, ErrNotFound
	}
	var row model.SubscriptionHost
	err := s.DB.First(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.SubscriptionHost{HostID: id, CascadeMode: "off"}, nil
	}
	row.EntryConfigured = row.EntryCiphertext != "" || row.EntryNodeID != nil
	return row, err
}

func (s *Service) SaveHost(id int, input HostInput) (model.SubscriptionHost, error) {
	var row model.SubscriptionHost
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var host model.Host
		if tx.First(&host, id).Error != nil {
			return ErrNotFound
		}
		var ib model.Inbound
		if tx.First(&ib, host.InboundId).Error != nil {
			return ErrNotFound
		}
		err := tx.First(&row, id).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Revision != input.Revision {
			return ErrConflict
		}
		mode := input.CascadeMode
		if mode == "" {
			mode = "off"
		}
		if mode != "off" && mode != "replace" && mode != "additional" {
			return ErrInvalid
		}
		stream := ""
		if input.ClientStream != "" {
			var value map[string]any
			if json.Unmarshal([]byte(input.ClientStream), &value) != nil {
				return ErrInvalid
			}
			var base map[string]any
			if json.Unmarshal([]byte(ib.StreamSettings), &base) != nil || base["network"] != "xhttp" && base["network"] != "splithttp" {
				return ErrInvalid
			}
			transport, _ := base["xhttpSettings"].(map[string]any)
			if transport == nil {
				transport, _ = base["splithttpSettings"].(map[string]any)
			}
			path, _ := transport["path"].(string)
			if err := ValidateConnection(value, path, false); err != nil {
				return err
			}
			raw, _ := json.Marshal(value)
			stream = string(raw)
		}
		row.HostID, row.Revision, row.CascadeMode, row.ClientStream = id, row.Revision+1, mode, stream
		if input.MihomoOnly != nil {
			row.MihomoOnly = *input.MihomoOnly
		}
		if stream != "" {
			row.MihomoOnly = true
		}
		if input.ClearEntry || mode == "off" {
			row.EntryCiphertext = ""
			row.EntryNodeID = nil
		}
		if mode != "off" && input.EntryNodeID != nil {
			if _, err := publicEntry(tx, *input.EntryNodeID); err != nil {
				return err
			}
			row.EntryNodeID = input.EntryNodeID
			row.EntryCiphertext = ""
		}
		if mode != "off" && input.EntryYAML != "" {
			if input.EntryNodeID != nil {
				return ErrInvalid
			}
			node, err := ParseEntry(input.EntryYAML)
			if err != nil {
				return err
			}
			plain, _ := json.Marshal(node)
			cipher, err := Encrypt("proxy", plain)
			if err != nil {
				return err
			}
			row.EntryCiphertext = cipher
			row.EntryNodeID = nil
		}
		if mode != "off" && row.EntryNodeID == nil && row.EntryCiphertext == "" {
			return ErrInvalid
		}
		return tx.Save(&row).Error
	})
	row.EntryConfigured = row.EntryCiphertext != "" || row.EntryNodeID != nil
	return row, err
}

func allowedKeys(v map[string]any, keys string) bool {
	allowed := map[string]bool{}
	for _, k := range strings.Fields(keys) {
		allowed[k] = true
	}
	for key := range v {
		if !allowed[key] {
			return false
		}
	}
	return true
}

var intervalPattern = regexp.MustCompile(`^[0-9]+(?:-[0-9]+)?$`)

func validInterval(v any) bool {
	str := fmt.Sprint(v)
	if !intervalPattern.MatchString(str) || len(str) > 24 {
		return false
	}
	parts := strings.Split(str, "-")
	a, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || a > 2147483647 {
		return false
	}
	if len(parts) == 2 {
		b, err := strconv.ParseUint(parts[1], 10, 32)
		return err == nil && a <= b && b <= 2147483647
	}
	return true
}

func ValidateConnection(v map[string]any, path string, download bool) error {
	if !allowedKeys(v, "address port network security tlsSettings realitySettings xhttpSettings splithttpSettings") {
		return ErrInvalid
	}
	if n, exists := v["network"]; exists && n != "xhttp" && n != "splithttp" {
		return ErrInvalid
	}
	if address, exists := v["address"]; exists {
		str, ok := address.(string)
		if !ok || !cleanText(str, 512) || strings.ContainsAny(str, " /?#@") {
			return ErrInvalid
		}
	}
	if port, exists := v["port"]; exists {
		n := number(port)
		if n < 1 || n > 65535 {
			return ErrInvalid
		}
	}
	sec, ok := v["security"].(string)
	if _, exists := v["security"]; exists && !ok {
		return ErrInvalid
	}
	if sec == "" {
		sec = "none"
	}
	if sec != "none" && sec != "tls" && sec != "reality" {
		return ErrInvalid
	}
	for _, key := range []string{"tlsSettings", "realitySettings"} {
		if raw, exists := v[key]; exists {
			tls, ok := raw.(map[string]any)
			if !ok || !allowedKeys(tls, "serverName fingerprint alpn allowInsecure publicKey password shortId") {
				return ErrInvalid
			}
			if key == "tlsSettings" && !allowedKeys(tls, "serverName fingerprint alpn allowInsecure") {
				return ErrInvalid
			}
			for field, value := range tls {
				switch field {
				case "allowInsecure":
					if _, ok := value.(bool); !ok {
						return ErrInvalid
					}
				case "alpn":
					list, ok := value.([]any)
					if !ok || len(list) == 0 || len(list) > 8 {
						return ErrInvalid
					}
					for _, protocol := range list {
						if protocol != "h2" && protocol != "http/1.1" && protocol != "h3" {
							return ErrInvalid
						}
					}
				default:
					text, ok := value.(string)
					if !ok || len(text) > 512 || strings.ContainsAny(text, "\r\n\x00") {
						return ErrInvalid
					}
				}
			}
			items := 0
			if !boundedValue(tls, 0, &items, false) {
				return ErrInvalid
			}
		}
	}
	if sec != "none" {
		tls, ok := v[sec+"Settings"].(map[string]any)
		if !ok {
			return ErrInvalid
		}
		name, _ := tls["serverName"].(string)
		if !cleanText(name, 512) {
			return ErrInvalid
		}
		if sec == "reality" {
			pk, _ := tls["publicKey"].(string)
			if pk == "" {
				pk, _ = tls["password"].(string)
			}
			if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(pk) {
				return ErrInvalid
			}
			sid, _ := tls["shortId"].(string)
			if !regexp.MustCompile(`^(?:[a-fA-F0-9]{2}){0,8}$`).MatchString(sid) {
				return ErrInvalid
			}
		}
	}
	tr, _ := v["xhttpSettings"].(map[string]any)
	if tr == nil {
		tr, _ = v["splithttpSettings"].(map[string]any)
	}
	if tr == nil {
		return ErrInvalid
	}
	return validateTransport(tr, path, download)
}

func validateTransport(v map[string]any, path string, download bool) error {
	keys := "path host headers mode noGRPCHeader xPaddingBytes scMaxEachPostBytes scMinPostsIntervalMs xmux"
	if !download {
		keys += " extra downloadSettings"
	}
	if !allowedKeys(v, keys) {
		return ErrInvalid
	}
	if p, _ := v["path"].(string); p != path {
		return ErrInvalid
	}
	items := 0
	if !boundedValue(v, 0, &items, true) {
		return ErrInvalid
	}
	if mode, exists := v["mode"]; exists && mode != "auto" && mode != "packet-up" && mode != "stream-up" && mode != "stream-one" {
		return ErrInvalid
	}
	if host, exists := v["host"]; exists {
		text, ok := host.(string)
		if !ok || len(text) > 1024 || strings.ContainsAny(text, "\r\n\x00") {
			return ErrInvalid
		}
	}
	if raw, exists := v["noGRPCHeader"]; exists {
		if _, ok := raw.(bool); !ok {
			return ErrInvalid
		}
	}
	if raw, exists := v["headers"]; exists {
		headers, ok := raw.(map[string]any)
		if !ok || len(headers) > 64 {
			return ErrInvalid
		}
		for name, value := range headers {
			text, ok := value.(string)
			if !ok || !cleanText(name, 128) || strings.ContainsAny(name, " :\t") || len(text) > 2048 || strings.ContainsAny(text, "\r\n\x00") {
				return ErrInvalid
			}
		}
	}
	for _, key := range []string{"xPaddingBytes", "scMaxEachPostBytes", "scMinPostsIntervalMs"} {
		if value, exists := v[key]; exists && !validInterval(value) {
			return ErrInvalid
		}
	}
	if raw, exists := v["xmux"]; exists {
		xmux, ok := raw.(map[string]any)
		if !ok || !allowedKeys(xmux, "maxConcurrency maxConnections cMaxReuseTimes hMaxRequestTimes hMaxReusableSecs hKeepAlivePeriod") {
			return ErrInvalid
		}
		for key, value := range xmux {
			if key == "hKeepAlivePeriod" && (number(value) < 0 || fmt.Sprint(number(value)) != fmt.Sprint(value)) {
				return ErrInvalid
			}
			if !validInterval(value) {
				return ErrInvalid
			}
		}
	}
	if raw, exists := v["extra"]; exists {
		extra, ok := raw.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		copy := cloneMap(extra)
		for _, key := range []string{"host", "path", "mode"} {
			copy[key] = v[key]
			if copy[key] == nil {
				delete(copy, key)
			}
		}
		if err := validateTransport(copy, path, true); err != nil {
			delete(copy, "downloadSettings")
			if err := validateTransport(copy, path, true); err != nil {
				return err
			}
		}
		if down, exists := extra["downloadSettings"]; exists {
			d, ok := down.(map[string]any)
			if !ok || ValidateConnection(d, path, true) != nil {
				return ErrInvalid
			}
		}
	}
	if raw, exists := v["downloadSettings"]; exists {
		down, ok := raw.(map[string]any)
		if !ok || ValidateConnection(down, path, true) != nil {
			return ErrInvalid
		}
	}
	return nil
}

func number(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	}
	return 0
}

func cloneMap(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func EffectiveXHTTP(v map[string]any) map[string]any {
	if extra, ok := v["extra"].(map[string]any); ok {
		out := cloneMap(extra)
		for _, key := range []string{"host", "path", "mode"} {
			out[key] = v[key]
		}
		if out["mode"] == nil || out["mode"] == "" {
			out["mode"] = "auto"
		}
		return out
	}
	return v
}

func MihomoConnection(v map[string]any) map[string]any {
	sec, _ := v["security"].(string)
	tls, _ := v[sec+"Settings"].(map[string]any)
	address, _ := v["address"].(string)
	out := map[string]any{"tls": sec == "tls" || sec == "reality", "reality-opts": map[string]any{"public-key": ""}, "servername": address, "skip-cert-verify": false, "client-fingerprint": "chrome", "alpn": []string{"http/1.1"}}
	if address != "" {
		out["server"] = strings.Trim(address, "[]")
	}
	if port, exists := v["port"]; exists {
		out["port"] = port
	}
	if sec == "tls" || sec == "reality" {
		out["alpn"] = []string{"h2"}
	}
	for _, pair := range [][2]string{{"serverName", "servername"}, {"allowInsecure", "skip-cert-verify"}, {"fingerprint", "client-fingerprint"}, {"alpn", "alpn"}} {
		if value, ok := tls[pair[0]]; ok {
			out[pair[1]] = value
		}
	}
	if sec == "reality" {
		pk := tls["password"]
		if pk == nil {
			pk = tls["publicKey"]
		}
		sid := tls["shortId"]
		if sid == nil {
			sid = ""
		}
		out["reality-opts"] = map[string]any{"public-key": pk, "short-id": sid, "support-x25519mlkem768": true}
	}
	return out
}

type EntryChoice struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	SourceID   int    `json:"source_id"`
	SourceName string `json:"source_name"`
	ProxyType  string `json:"proxy_type"`
}

func (s *Service) EntryChoices(search string, selected int) ([]EntryChoice, error) {
	rows := []EntryChoice{}
	if len(search) > 256 {
		return rows, ErrInvalid
	}
	q := s.DB.Table("subscription_nodes AS n").Select("n.id,n.name,n.source_id,s.name AS source_name,n.proxy_type").Joins("JOIN subscription_sources AS s ON s.id = n.source_id").Where("s.owner_account_id IS NULL AND s.enabled = ? AND n.is_active = ?", true, true)
	if search != "" {
		q = q.Where("LOWER(n.name) LIKE ? OR LOWER(s.name) LIKE ?", "%"+strings.ToLower(search)+"%", "%"+strings.ToLower(search)+"%")
	}
	err := q.Order("n.source_id,n.position,n.id").Limit(100).Find(&rows).Error
	if err == nil && selected > 0 {
		found := false
		for _, r := range rows {
			found = found || r.ID == selected
		}
		if !found {
			var item EntryChoice
			query := s.DB.Table("subscription_nodes AS n").Select("n.id,n.name,n.source_id,s.name AS source_name,n.proxy_type").Joins("JOIN subscription_sources AS s ON s.id=n.source_id").Where("n.id=? AND n.is_active=? AND s.enabled=? AND s.owner_account_id IS NULL", selected, true, true)
			if query.Take(&item).Error == nil {
				rows = append([]EntryChoice{item}, rows...)
			}
		}
	}
	return rows, err
}
