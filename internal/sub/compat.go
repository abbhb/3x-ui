package sub

import (
	"slices"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func (s *SubService) loadCompatibilityHosts() error {
	s.compatHosts = map[int]model.SubscriptionHost{}
	ids := []int{}
	for _, hosts := range s.hostsByInbound {
		for _, h := range hosts {
			ids = append(ids, h.Id)
		}
	}
	for start := 0; start < len(ids); start += 400 {
		var rows []model.SubscriptionHost
		if err := database.GetDB().Where("host_id IN ?", ids[start:min(start+400, len(ids))]).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			s.compatHosts[row.HostID] = row
		}
	}
	return nil
}

func (s *SubService) checkCapabilities(inbounds []*model.Inbound, subID, format string) error {
	if format == "clash" {
		return nil
	}
	for _, ib := range inbounds {
		if ib.ExcludeFromSub {
			continue
		}
		for _, client := range s.matchingClients(ib, subID) {
			if !client.Enable {
				continue
			}
			state, bound := s.clientHostBinding(ib, client)
			if state == hostBindingClosed {
				continue
			}
			hosts := s.hostsByInbound[ib.Id]
			if state == hostBindingBound {
				hosts = bound
			}
			for _, h := range hosts {
				if h.IsDisabled || slices.Contains(h.ExcludeFromSubTypes, format) {
					continue
				}
				route := s.compatHosts[h.Id]
				if route.CascadeMode != "" && route.CascadeMode != "off" {
					return subscriptioncompat.ErrMihomoRequired
				}
			}
		}
	}
	return nil
}

func (s *SubService) excludeProfile(h *model.Host, format string) bool {
	route := s.compatHosts[h.Id]
	return format != "clash" && (route.MihomoOnly || route.ClientStream != "" || (route.CascadeMode != "" && route.CascadeMode != "off"))
}

type compatHostGrant struct {
	ids     []int
	managed bool
	err     error
}
