package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type Overlay struct {
	ID        int
	GroupName string
	Nodes     []map[string]any
}
type Projection struct {
	Sources      []Overlay
	GroupMembers map[string][]string
	Client       *model.ClientRecord
	HasExternal  bool
}

func (s *Service) Projection(subID string) (Projection, error) {
	var result Projection
	if subID == "" {
		return result, nil
	}
	var clients []model.ClientRecord
	if err := s.DB.Where("sub_id = ?", subID).Limit(2).Find(&clients).Error; err != nil {
		return result, err
	}
	if len(clients) != 1 {
		return result, nil
	}
	client := clients[0]
	if !client.Enable || (client.ExpiryTime > 0 && client.ExpiryTime <= time.Now().UnixMilli()) {
		return result, nil
	}
	var a model.PortalAccount
	err := s.DB.Where("client_id = ?", client.Id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !a.IsActive {
		return result, nil
	}
	selections := []model.ExternalSelection{}
	var sub model.PortalSubscription
	err = s.DB.First(&sub, a.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err == nil && sub.DisabledAt == 0 && (sub.ExpiresAt == 0 || sub.ExpiresAt > time.Now().UnixMilli()) {
		var plan model.PortalPlan
		if err = s.DB.First(&plan, sub.PlanID).Error; err != nil {
			return result, err
		}
		selections = plan.ExternalSelections
		result.GroupMembers = plan.GroupMembers
	}
	var private []model.SubscriptionSource
	if err = s.DB.Where("owner_account_id = ? AND enabled = ?", a.ID, true).Order("id").Limit(MaxPrivateSources + 1).Find(&private).Error; err != nil {
		return result, err
	}
	if len(private) > MaxPrivateSources {
		return result, ErrBudget
	}
	for _, source := range private {
		selections = append(selections, model.ExternalSelection{SourceID: source.ID, Mode: "all"})
	}
	result.HasExternal = len(selections) > 0
	result.Client = &client
	publicCount, publicBytes, privateCount, privateBytes := 0, 0, 0, 0
	for _, sel := range selections {
		source, err := sourceFor(s.DB, sel.SourceID, nil)
		if err != nil {
			return result, err
		}
		if source.OwnerAccountID != nil && (*source.OwnerAccountID != a.ID || !source.Enabled) {
			continue
		}
		q := s.DB.Where("source_id = ? AND is_active = ?", source.ID, true)
		if sel.Mode == "selected" {
			q = q.Where("id IN ?", sel.NodeIDs)
		} else if sel.Mode != "all" {
			return result, ErrInvalid
		}
		var nodes []model.SubscriptionNode
		if err = q.Order("position,id").Limit(MaxSourceNodes + 1).Find(&nodes).Error; err != nil {
			return result, err
		}
		if len(nodes) > MaxSourceNodes {
			return result, ErrBudget
		}
		overlay := Overlay{ID: source.ID, GroupName: source.GroupName}
		for _, node := range nodes {
			payload, err := decryptedNode(node)
			if err != nil {
				return result, err
			}
			raw, _ := json.Marshal(payload)
			if source.OwnerAccountID == nil {
				publicCount++
				publicBytes += len(raw)
			} else {
				privateCount++
				privateBytes += len(raw)
			}
			if publicCount > MaxPlanNodes || publicBytes > MaxPlanBytes || privateCount > MaxPrivateNodes || privateBytes > MaxPrivateBytes {
				return result, ErrBudget
			}
			overlay.Nodes = append(overlay.Nodes, payload)
		}
		if len(overlay.Nodes) > 0 {
			result.Sources = append(result.Sources, overlay)
		}
	}
	slices.SortFunc(result.Sources, func(a, b Overlay) int { return a.ID - b.ID })
	return result, nil
}

func allocateName(base, key string, used map[string]bool) string {
	name := base
	if used[name] || reserved[name] {
		name = base + " · " + hashText(key)[:10]
	}
	for used[name] || reserved[name] {
		name += "_"
	}
	used[name] = true
	return name
}

func (s *Service) Render(config map[string]any, projection Projection, legacy bool) error {
	proxies := mapList(config["proxies"])
	groups := mapList(config["proxy-groups"])
	used := map[string]bool{}
	for _, p := range proxies {
		n, _ := p["name"].(string)
		used[n] = true
	}
	for _, g := range groups {
		n, _ := g["name"].(string)
		used[n] = true
	}
	helpers := map[string]string{}
	local := []map[string]any{}
	helperNodes := []map[string]any{}
	additions := map[string]string{}
	inboundNames := map[string][]string{}
	for _, p := range proxies {
		hostID := number(p["_mgma-host-id"])
		inboundID := number(p["_mgma-inbound-id"])
		delete(p, "_mgma-host-id")
		delete(p, "_mgma-inbound-id")
		var route model.SubscriptionHost
		err := s.DB.First(&route, hostID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		emitted := []map[string]any{p}
		if err == nil && route.CascadeMode != "" && route.CascadeMode != "off" {
			if legacy {
				return ErrMihomoRequired
			}
			var entry map[string]any
			if route.EntryNodeID != nil {
				entry, err = publicEntry(s.DB, *route.EntryNodeID)
			} else {
				raw, e := Decrypt("proxy", route.EntryCiphertext)
				err = e
				if err == nil {
					if json.Unmarshal(raw, &entry) != nil {
						err = ErrCascade
					} else {
						entry, err = normalizeNode(entry)
					}
				}
			}
			if err != nil {
				return ErrCascade
			}
			raw, _ := json.Marshal(entry)
			key := hashText(string(raw))
			name := helpers[key]
			if name == "" {
				name = allocateName("MGMA-HOP · "+key[:20], "hop:"+key, used)
				helpers[key] = name
				entry["name"] = name
				helperNodes = append(helperNodes, entry)
			}
			landing := p
			if route.CascadeMode == "additional" {
				landing = cloneMap(p)
				landing["name"] = allocateName(p["name"].(string)+" · 级联", "landing:"+strconv.Itoa(hostID), used)
				additions[p["name"].(string)] = landing["name"].(string)
				emitted = append(emitted, landing)
			}
			landing["dialer-proxy"] = name
		}
		local = append(local, emitted...)
		if inboundID > 0 {
			ref := "inbound:" + strconv.Itoa(inboundID)
			for _, p := range emitted {
				inboundNames[ref] = append(inboundNames[ref], p["name"].(string))
			}
		}
	}
	names := []string{}
	for _, p := range local {
		if n, ok := p["name"].(string); ok {
			names = append(names, n)
		}
	}
	policy, err := s.Policy()
	if err != nil {
		return err
	}
	if policy.Enabled && !legacy {
		doc, e := policyDocument(policy)
		if e != nil {
			return e
		}
		for key, value := range doc {
			config[key] = value
		}
		groups = mapList(config["proxy-groups"])
		for _, g := range groups {
			members := []string{}
			for _, member := range stringList(g["proxies"]) {
				if member == LocalPlaceholder {
					members = append(members, names...)
				} else {
					members = append(members, member)
				}
			}
			if g["include-all"] == true || g["include-all-proxies"] == true {
				members = append(members, names...)
			}
			if filter, ok := g["filter"].(string); ok && filter != "" {
				pattern, e := regexp.Compile(filter)
				if e != nil {
					return ErrInvalid
				}
				filtered := []string{}
				for _, n := range members {
					if pattern.MatchString(n) {
						filtered = append(filtered, n)
					}
				}
				members = filtered
			}
			for _, key := range []string{"include-all", "include-all-proxies", "include-all-providers", "use", "filter"} {
				delete(g, key)
			}
			g["proxies"] = uniqueNames(members)
		}
	}
	for _, g := range groups {
		members := stringList(g["proxies"])
		for _, name := range slices.Clone(members) {
			if extra := additions[name]; extra != "" && !slices.Contains(members, extra) {
				members = append(members, extra)
			}
		}
		if g["include-all"] == true || g["include-all-proxies"] == true {
			candidates := slices.Clone(names)
			for _, pair := range [][2]string{{"filter", "include"}, {"exclude-filter", "exclude"}} {
				if raw, ok := g[pair[0]].(string); ok && raw != "" {
					pattern, e := regexp.Compile(raw)
					if e != nil {
						return ErrInvalid
					}
					candidates = slices.DeleteFunc(candidates, func(name string) bool {
						match := pattern.MatchString(name)
						return (pair[1] == "include" && !match) || (pair[1] == "exclude" && match)
					})
				}
			}
			members = append(members, candidates...)
		}
		for _, key := range []string{"include-all", "include-all-proxies", "include-all-providers"} {
			delete(g, key)
		}
		g["proxies"] = uniqueNames(members)
	}

	used = map[string]bool{ExternalGroup: true}
	for _, p := range append(local, helperNodes...) {
		used[p["name"].(string)] = true
	}
	for _, g := range groups {
		used[g["name"].(string)] = true
	}
	externalGroups := []map[string]any{}
	sourceNames := []string{}
	for _, overlay := range projection.Sources {
		name := allocateName(overlay.GroupName, "source:"+strconv.Itoa(overlay.ID), used)
		members := []string{}
		for index, raw := range overlay.Nodes {
			p := cloneMap(raw)
			if legacy && !legacyExternal(p) {
				continue
			}
			base := "外部 · " + name + " / " + p["name"].(string)
			p["name"] = allocateName(base, strconv.Itoa(overlay.ID)+":"+strconv.Itoa(index), used)
			local = append(local, p)
			members = append(members, p["name"].(string))
		}
		if len(members) > 0 {
			sourceNames = append(sourceNames, name)
			externalGroups = append(externalGroups, map[string]any{"name": name, "type": "select", "proxies": members})
		}
	}
	aggregate := map[string]any(nil)
	for _, g := range groups {
		if g["name"] == ExternalGroup {
			aggregate = g
			break
		}
	}
	if aggregate == nil && (projection.HasExternal || policy.Enabled) {
		aggregate = map[string]any{"name": ExternalGroup, "type": "select"}
		groups = append(groups, aggregate)
	}
	if aggregate != nil {
		if len(sourceNames) == 0 {
			aggregate["proxies"] = []string{"REJECT"}
		} else {
			aggregate["proxies"] = sourceNames
		}
	}
	if len(sourceNames) > 0 {
		primary := "PROXY"
		if policy.Enabled && !legacy {
			primary = policy.PrimaryGroup
		}
		for _, g := range groups {
			if g["name"] == primary {
				members := stringList(g["proxies"])
				if !slices.Contains(members, ExternalGroup) {
					g["proxies"] = append(members, ExternalGroup)
				}
			}
		}
	}
	for _, g := range groups {
		ref, exists := projection.GroupMembers[g["name"].(string)]
		if !exists || g["name"] == ExternalGroup {
			continue
		}
		members := []string{}
		for _, r := range ref {
			if r == "external" && aggregate != nil {
				members = append(members, ExternalGroup)
			} else {
				members = append(members, inboundNames[r]...)
			}
		}
		g["proxies"] = uniqueNames(members)
		for _, key := range []string{"include-all", "include-all-proxies", "include-all-providers", "filter", "exclude-filter", "exclude-type", "use"} {
			delete(g, key)
		}
	}
	for _, g := range groups {
		if len(stringList(g["proxies"])) == 0 {
			g["proxies"] = []string{"REJECT"}
		}
	}
	if policy.Enabled && !legacy && len(policy.GroupOrder) > 0 {
		ordered := []map[string]any{}
		for _, name := range policy.GroupOrder {
			for _, g := range groups {
				if g["name"] == name {
					ordered = append(ordered, g)
				}
			}
		}
		for _, g := range groups {
			if g["name"] == ExternalGroup {
				ordered = append(ordered, g)
			}
		}
		groups = ordered
	}
	groups = append(groups, externalGroups...)
	config["proxy-groups"] = groups
	config["proxies"] = append(local, helperNodes...)
	if policy.Enabled && !legacy && policy.SecureDNS && !s.SuppressSecureDNS {
		var settings model.PortalSettings
		if e := s.DB.First(&settings, 1).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		SecureDNS(config, policy.PrimaryGroup, settings.PublicURL)
	}
	presentIPv6(config)
	return nil
}

func uniqueNames(names []string) []string {
	out := []string{}
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func legacyExternal(p map[string]any) bool {
	typ, _ := p["type"].(string)
	if !slices.Contains([]string{"http", "snell", "socks5", "ss", "ssr", "trojan", "vmess"}, typ) {
		return false
	}
	if p["network"] == "xhttp" || p["reality-opts"] != nil {
		return false
	}
	return true
}

func usesIPv6(p map[string]any) bool {
	addresses := []any{p["server"]}
	if x, ok := p["xhttp-opts"].(map[string]any); ok {
		if d, ok := x["download-settings"].(map[string]any); ok {
			addresses = append(addresses, d["server"])
		}
	}
	for _, v := range addresses {
		if s, ok := v.(string); ok {
			if ip, e := netip.ParseAddr(strings.Trim(s, "[]")); e == nil && ip.Is6() {
				return true
			}
		}
	}
	return false
}

func presentIPv6(config map[string]any) {
	proxies := mapList(config["proxies"])
	renames := map[string]string{}
	used := map[string]bool{}
	for _, p := range proxies {
		used[p["name"].(string)] = true
	}
	for _, p := range proxies {
		if usesIPv6(p) {
			config["ipv6"] = true
			name := p["name"].(string)
			if !strings.HasPrefix(name, "[ipv6] ") && !strings.HasPrefix(name, "MGMA-HOP") {
				newName := allocateName("[ipv6] "+name, name, used)
				renames[name] = newName
				p["name"] = newName
			}
		}
	}
	slices.SortStableFunc(proxies, func(a, b map[string]any) int {
		if usesIPv6(a) == usesIPv6(b) {
			return 0
		}
		if usesIPv6(a) {
			return -1
		}
		return 1
	})
	config["proxies"] = proxies
	for _, p := range proxies {
		if n, ok := p["dialer-proxy"].(string); ok && renames[n] != "" {
			p["dialer-proxy"] = renames[n]
		}
	}
	for _, g := range mapList(config["proxy-groups"]) {
		members := stringList(g["proxies"])
		for i, name := range members {
			if n := renames[name]; n != "" {
				members[i] = n
			}
		}
		g["proxies"] = members
	}
}
