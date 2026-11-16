package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	ExternalGroup    = "MGMA · External"
	LocalPlaceholder = "__MGMA_LOCAL_NODES__"
)

var (
	reserved  = map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "GLOBAL": true, "PASS": true, "COMPATIBLE": true}
	ruleTypes = map[string]bool{}
)

func init() {
	for _, key := range strings.Fields("DOMAIN DOMAIN-SUFFIX DOMAIN-KEYWORD DOMAIN-REGEX GEOSITE IP-CIDR IP-CIDR6 IP-SUFFIX IP-ASN GEOIP SRC-GEOIP SRC-IP-ASN SRC-IP-CIDR SRC-IP-SUFFIX DST-PORT SRC-PORT IN-PORT IN-TYPE IN-USER IN-NAME PROCESS-PATH PROCESS-PATH-REGEX PROCESS-NAME PROCESS-NAME-REGEX UID NETWORK DSCP RULE-SET AND OR NOT MATCH") {
		ruleTypes[key] = true
	}
}

func mapList(v any) []map[string]any {
	switch list := v.(type) {
	case []map[string]any:
		return list
	case []any:
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func stringList(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := []string{}
		for _, item := range list {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func policyDocument(policy model.SubscriptionPolicy) (map[string]any, error) {
	if policy.Document == "" {
		return map[string]any{}, nil
	}
	return DecodeDocument([]byte(policy.Document))
}

func ValidatePolicy(policy model.SubscriptionPolicy) error {
	if !cleanText(policy.PrimaryGroup, 256) || len(policy.Document) > 1<<20 {
		return ErrInvalid
	}
	doc, err := policyDocument(policy)
	if err != nil {
		return ErrInvalid
	}
	if !allowedKeys(doc, "mixed-port port socks-port allow-lan bind-address mode log-level ipv6 external-controller unified-delay global-client-fingerprint experimental profile geox-url geo-auto-update geo-update-interval dns tun hosts sniffer find-process-mode tcp-concurrent keep-alive-interval keep-alive-idle disable-keep-alive authentication skip-auth-prefixes proxy-groups rule-providers rules clash-for-android") {
		return ErrInvalid
	}
	if _, exists := doc["authentication"]; exists {
		return ErrInvalid
	}
	groups := mapList(doc["proxy-groups"])
	if len(groups) == 0 || len(groups) > 2048 {
		return ErrInvalid
	}
	names := map[string]bool{}
	for _, g := range groups {
		n, _ := g["name"].(string)
		if !cleanText(n, 256) || reserved[n] || names[n] {
			return ErrInvalid
		}
		names[n] = true
		typ, _ := g["type"].(string)
		if !slices.Contains([]string{"select", "url-test", "fallback", "load-balance", "relay"}, typ) {
			return ErrInvalid
		}
		if _, exists := g["use"]; exists {
			return ErrInvalid
		}
	}
	if !names[policy.PrimaryGroup] {
		return ErrInvalid
	}
	names[ExternalGroup] = true
	edges := map[string][]string{}
	for _, g := range groups {
		for _, name := range stringList(g["proxies"]) {
			if !names[name] && !reserved[name] && name != LocalPlaceholder {
				return ErrInvalid
			}
			edges[g["name"].(string)] = append(edges[g["name"].(string)], name)
		}
	}
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(n string) bool {
		if active[n] {
			return false
		}
		if done[n] || reserved[n] || n == LocalPlaceholder || n == ExternalGroup {
			return true
		}
		active[n] = true
		for _, child := range edges[n] {
			if !visit(child) {
				return false
			}
		}
		delete(active, n)
		done[n] = true
		return true
	}
	for n := range edges {
		if !visit(n) {
			return ErrInvalid
		}
	}
	order := policy.GroupOrder
	if len(order) > 0 {
		expected := []string{}
		for _, g := range groups {
			if g["name"] != ExternalGroup {
				expected = append(expected, g["name"].(string))
			}
		}
		if len(order) != len(expected) {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, n := range order {
			if seen[n] || !slices.Contains(expected, n) {
				return ErrInvalid
			}
			seen[n] = true
		}
	}
	providers, _ := doc["rule-providers"].(map[string]any)
	return validateRules(stringList(doc["rules"]), names, providers)
}

func validateRules(rules []string, targets map[string]bool, providers map[string]any) error {
	if len(rules) == 0 || len(rules) > 10000 {
		return ErrInvalid
	}
	total := 0
	for i, rule := range rules {
		total += len(rule)
		if len(rule) > 8192 || total > 1000000 || strings.ContainsAny(rule, "\r\n\x00") {
			return ErrInvalid
		}
		parts := strings.Split(rule, ",")
		for j := range parts {
			parts[j] = strings.TrimSpace(parts[j])
			if parts[j] == "" {
				return ErrInvalid
			}
		}
		typ := parts[0]
		if !ruleTypes[typ] || len(parts) < 2 {
			return ErrInvalid
		}
		idx := len(parts) - 1
		if parts[idx] == "no-resolve" {
			idx--
		}
		if idx < 1 || (!targets[parts[idx]] && !reserved[parts[idx]]) {
			return ErrInvalid
		}
		if typ == "MATCH" {
			if i != len(rules)-1 || len(parts) != 2 {
				return ErrInvalid
			}
		} else if typ != "AND" && typ != "OR" && typ != "NOT" {
			if idx != 2 {
				return ErrInvalid
			}
		} else {
			depth := 0
			for _, r := range strings.Join(parts[1:idx], ",") {
				if r == '(' {
					depth++
				}
				if r == ')' {
					depth--
				}
				if depth < 0 {
					return ErrInvalid
				}
			}
			if depth != 0 {
				return ErrInvalid
			}
		}
		if typ == "RULE-SET" {
			if _, ok := providers[parts[1]]; !ok {
				return ErrInvalid
			}
		}
		if typ == "IP-CIDR" || typ == "IP-CIDR6" || typ == "SRC-IP-CIDR" {
			prefix, e := netip.ParsePrefix(parts[1])
			if e != nil || (typ == "IP-CIDR6" && !prefix.Addr().Is6()) {
				return ErrInvalid
			}
		}
		if typ == "DST-PORT" || typ == "SRC-PORT" || typ == "IN-PORT" {
			ps := strings.Split(parts[1], "-")
			if len(ps) > 2 {
				return ErrInvalid
			}
			last := 0
			for _, part := range ps {
				n, e := strconv.Atoi(part)
				if e != nil || n < 1 || n > 65535 || n < last {
					return ErrInvalid
				}
				last = n
			}
		}
		if slices.Contains([]string{"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD"}, typ) && regexp.MustCompile(`\s`).MatchString(parts[1]) {
			return ErrInvalid
		}
	}
	if !strings.HasPrefix(rules[len(rules)-1], "MATCH,") {
		return ErrInvalid
	}
	return nil
}

func (s *Service) Policy() (model.SubscriptionPolicy, error) {
	var p model.SubscriptionPolicy
	err := s.DB.First(&p, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.SubscriptionPolicy{PrimaryGroup: "PROXY", Document: `{"proxy-groups":[{"name":"PROXY","type":"select","proxies":["__MGMA_LOCAL_NODES__","MGMA · External"]}],"rules":["MATCH,PROXY"]}`}, nil
	}
	return p, err
}

func (s *Service) SavePolicy(p model.SubscriptionPolicy) (model.SubscriptionPolicy, error) {
	if err := ValidatePolicy(p); err != nil {
		return p, err
	}
	doc, _ := policyDocument(p)
	b, _ := json.Marshal(doc)
	p.Document = string(b)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var old model.SubscriptionPolicy
		err := tx.First(&old, 1).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if old.Revision != p.Revision {
			return ErrConflict
		}
		p.ID = 1
		p.Revision++
		return tx.Save(&p).Error
	})
	return p, err
}

func ValidateMembers(tx *gorm.DB, p model.PortalPlan) error {
	if len(p.GroupMembers) > 64 {
		return ErrInvalid
	}
	if len(p.GroupMembers) == 0 {
		return nil
	}
	policy, err := (&Service{DB: tx}).Policy()
	if err != nil {
		return err
	}
	doc, err := policyDocument(policy)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, g := range mapList(doc["proxy-groups"]) {
		names[g["name"].(string)] = true
	}
	allowed := map[string]bool{"external": true}
	for _, id := range p.InboundIDs {
		allowed["inbound:"+strconv.Itoa(id)] = true
	}
	for name, members := range p.GroupMembers {
		if !names[name] || name == ExternalGroup || len(members) > 128 {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, m := range members {
			if !allowed[m] || seen[m] {
				return ErrInvalid
			}
			seen[m] = true
		}
	}
	return nil
}
