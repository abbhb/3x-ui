package subscriptioncompat

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/link"
)

// ClientSources is safe for both the panel and the subscription owner. The
// management API never exposes provider URLs, node payloads or client secrets.
type ClientSources struct {
	Linked  bool           `json:"linked"`
	Plan    string         `json:"plan"`
	Sources []ClientSource `json:"sources"`
	Direct  []DirectSource `json:"direct"`
}

type ClientSource struct {
	ID      int                `json:"id"`
	Name    string             `json:"name"`
	Private bool               `json:"private"`
	Nodes   []ClientSourceNode `json:"nodes"`
}

type ClientSourceNode struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	ShareLink bool   `json:"share_link"`
}

type DirectSource struct {
	Kind   string `json:"kind"`
	Remark string `json:"remark"`
}

// ClientSourcesFor returns only the source selection used by the subscription
// renderer. Remote native links are shown as assignments without fetching them.
func (s *Service) ClientSourcesFor(id int, owner *int) (ClientSources, error) {
	result := ClientSources{Sources: []ClientSource{}, Direct: []DirectSource{}}
	if id == 0 {
		return result, nil
	}
	var client model.ClientRecord
	if err := s.DB.Select("id", "enable", "expiry_time", "sub_id").First(&client, id).Error; err != nil {
		return result, err
	}
	var account model.PortalAccount
	err := s.DB.Where("client_id = ?", id).First(&account).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	result.Linked = err == nil
	if owner != nil && (!result.Linked || account.ID != *owner) {
		return result, ErrNotFound
	}
	now := time.Now().UnixMilli()
	if !client.Enable || client.ExpiryTime > 0 && client.ExpiryTime <= now {
		return result, nil
	}
	var rows []model.ClientExternalLink
	if err := s.DB.Model(&model.ClientExternalLink{}).
		Select("kind, remark, CASE WHEN kind = ? AND remark = '' THEN value ELSE '' END AS value", model.ExternalLinkKindLink).
		Where("client_id = ? AND (enable IS NULL OR enable = ?) AND (expiry_time IS NULL OR expiry_time <= 0 OR expiry_time > ?)", id, true, now).
		Order("sort_index, id").Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		name := row.Remark
		if row.Value != "" {
			if parsed, e := link.ParseLink(row.Value); e == nil {
				name, _ = parsed.Outbound["tag"].(string)
			}
		}
		result.Direct = append(result.Direct, DirectSource{Kind: row.Kind, Remark: name})
	}
	if !result.Linked {
		return result, nil
	}
	projection, err := s.Projection(client.SubID)
	if err != nil {
		return result, err
	}
	if projection.Client == nil || projection.Client.Id != id {
		return result, nil
	}
	result.Plan = projection.PlanName
	for _, source := range projection.Sources {
		item := ClientSource{ID: source.ID, Name: source.SourceName, Private: source.Private}
		for _, node := range source.Nodes {
			item.Nodes = append(item.Nodes, ClientSourceNode{ID: node.ID, Name: node.Proxy["name"].(string), Type: node.Proxy["type"].(string), ShareLink: node.ShareLink != ""})
		}
		result.Sources = append(result.Sources, item)
	}
	return result, nil
}
