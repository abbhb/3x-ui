package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/hostops"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

type ServiceHostInput struct {
	Name    string `json:"name"`
	Note    string `json:"note"`
	NodeIds []int  `json:"nodeIds"`
}

type HostServiceInstance struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	XrayState string `json:"xrayState"`
	Address   string `json:"address"`
}

type ServiceHostView struct {
	model.ServiceHost
	Services []HostServiceInstance `json:"services"`
}

type HostEndpoint struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Family   string `json:"family"`
	Disabled bool   `json:"disabled"`
}

type HostLineView struct {
	ID        int            `json:"id"`
	NodeID    int            `json:"nodeId"`
	Tag       string         `json:"tag"`
	Name      string         `json:"name"`
	Protocol  string         `json:"protocol"`
	Transport string         `json:"transport"`
	Listen    string         `json:"listen"`
	Port      int            `json:"port"`
	Enable    bool           `json:"enable"`
	Endpoints []HostEndpoint `json:"endpoints"`
}

type ServiceHostDetail struct {
	ServiceHostView
	Lines []HostLineView `json:"lines"`
}

type HostInstanceReport struct {
	NodeID int    `json:"nodeId"`
	Name   string `json:"name"`
	hostops.Report
}

type ServiceHostService struct{}

func (s *ServiceHostService) List() ([]ServiceHostView, error) {
	db := database.GetDB()
	var hosts []model.ServiceHost
	var members []model.ServiceHostMember
	var nodes []model.Node
	if err := db.Order("id").Find(&hosts).Error; err != nil {
		return nil, err
	}
	if err := db.Order("node_id").Find(&members).Error; err != nil {
		return nil, err
	}
	if err := db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	byID := map[int]HostServiceInstance{0: {ID: 0, Name: "Panel core", Status: "local"}}
	for _, n := range nodes {
		byID[n.Id] = HostServiceInstance{ID: n.Id, Name: n.Name, Status: n.Status, XrayState: n.XrayState, Address: n.Address}
	}
	result := make([]ServiceHostView, 0, len(hosts))
	for _, host := range hosts {
		view := ServiceHostView{ServiceHost: host, Services: []HostServiceInstance{}}
		for _, m := range members {
			if m.HostId == host.Id {
				if node, ok := byID[m.NodeId]; ok {
					view.Services = append(view.Services, node)
				}
			}
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *ServiceHostService) Save(id int, input ServiceHostInput) (*model.ServiceHost, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 128 || len(input.Note) > 2000 || len(input.NodeIds) == 0 || len(input.NodeIds) > 32 {
		return nil, errors.New("provide a name and 1-32 service instances")
	}
	seen := map[int]bool{}
	for _, node := range input.NodeIds {
		if node < 0 || seen[node] {
			return nil, errors.New("invalid or duplicate service instance")
		}
		seen[node] = true
	}
	host := &model.ServiceHost{Name: input.Name, Note: input.Note}
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		for node := range seen {
			if node != 0 {
				var count int64
				if err := tx.Model(&model.Node{}).Where("id = ?", node).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return errors.New("service instance does not exist")
				}
			}
			var linked model.ServiceHostMember
			err := tx.Where("node_id = ?", node).First(&linked).Error
			if err == nil && linked.HostId != id {
				return errors.New("service instance already belongs to another host")
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if id != 0 {
			if err := tx.First(host, id).Error; err != nil {
				return err
			}
			host.Name, host.Note = input.Name, input.Note
		}
		if err := tx.Save(host).Error; err != nil {
			return err
		}
		if err := tx.Where("host_id = ?", host.Id).Delete(&model.ServiceHostMember{}).Error; err != nil {
			return err
		}
		for _, node := range input.NodeIds {
			if err := tx.Create(&model.ServiceHostMember{NodeId: node, HostId: host.Id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return host, err
}

func (s *ServiceHostService) Delete(id int) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("host_id = ?", id).Delete(&model.ServiceHostMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.ServiceHost{}, id).Error
	})
}

func (s *ServiceHostService) Get(id int) (*ServiceHostDetail, error) {
	views, err := s.List()
	if err != nil {
		return nil, err
	}
	var selected *ServiceHostView
	for i := range views {
		if views[i].Id == id {
			selected = &views[i]
			break
		}
	}
	if selected == nil {
		return nil, gorm.ErrRecordNotFound
	}
	nodeIDs := []int{}
	for _, instance := range selected.Services {
		nodeIDs = append(nodeIDs, instance.ID)
	}
	var inbounds []model.Inbound
	db := database.GetDB()
	query := db.Where("node_id IN ?", nodeIDs)
	if slices.Contains(nodeIDs, 0) {
		query = query.Or("node_id IS NULL")
	}
	if err := query.Order("id").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	var hosts []model.Host
	ids := make([]int, 0, len(inbounds))
	for _, ib := range inbounds {
		ids = append(ids, ib.Id)
	}
	if len(ids) > 0 {
		if err := db.Where("inbound_id IN ?", ids).Order("sort_order,id").Find(&hosts).Error; err != nil {
			return nil, err
		}
	}
	detail := &ServiceHostDetail{ServiceHostView: *selected, Lines: []HostLineView{}}
	for _, ib := range inbounds {
		node := 0
		if ib.NodeID != nil {
			node = *ib.NodeID
		}
		var stream struct {
			Network string `json:"network"`
		}
		_ = json.Unmarshal([]byte(ib.StreamSettings), &stream)
		line := HostLineView{
			ID: ib.Id, NodeID: node, Tag: ib.Tag, Name: ib.Remark, Protocol: string(ib.Protocol), Transport: stream.Network,
			Listen: ib.Listen, Port: ib.Port, Enable: ib.Enable, Endpoints: []HostEndpoint{},
		}
		for _, h := range hosts {
			if h.InboundId != ib.Id {
				continue
			}
			family := "domain"
			if ip, err := netip.ParseAddr(strings.Trim(h.Address, "[]")); err == nil {
				if ip.Unmap().Is4() {
					family = "IPv4"
				} else {
					family = "IPv6"
				}
			}
			port := h.Port
			if port == 0 {
				port = ib.Port
			}
			line.Endpoints = append(line.Endpoints, HostEndpoint{ID: h.Id, Name: h.Remark, Address: h.Address, Port: port, Family: family, Disabled: h.IsDisabled})
		}
		detail.Lines = append(detail.Lines, line)
	}
	return detail, nil
}

type (
	hostReporter interface {
		HostReport(context.Context) (*hostops.Report, error)
	}
)

func LocalHostReport() *hostops.Report {
	report := hostops.ReadLocal()
	if process := currentXrayProcess(); process == nil || !process.IsRunning() {
		report.Observer.Ready = false
		report.Status.Verified = false
		report.Status.Reason = "core_stopped"
	}
	var inbounds []model.Inbound
	if err := database.GetDB().Where("node_id IS NULL").Order("id").Find(&inbounds).Error; err != nil {
		report.Error = "local inbound inventory unavailable"
		return &report
	}
	for _, ib := range inbounds {
		report.Inbounds = append(report.Inbounds, hostops.InboundRef{ID: ib.Id, Tag: ib.Tag, Protocol: string(ib.Protocol)})
	}
	return &report
}

func (s *ServiceHostService) Runtime(ctx context.Context, id int) ([]HostInstanceReport, error) {
	detail, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result := make([]HostInstanceReport, len(detail.Services))
	jobs := make(chan int, len(result))
	for i, instance := range detail.Services {
		result[i] = HostInstanceReport{NodeID: instance.ID, Name: instance.Name}
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(4, len(result)) {
		wg.Go(func() {
			for index := range jobs {
				item := &result[index]
				if item.NodeID == 0 {
					item.Report = *LocalHostReport()
					continue
				}
				mgr := runtime.GetManager()
				if mgr == nil {
					item.Error = "runtime manager unavailable"
					continue
				}
				rt, e := mgr.RuntimeFor(&item.NodeID)
				if e != nil {
					item.Error = "service unavailable"
					continue
				}
				provider, ok := rt.(hostReporter)
				if !ok {
					item.Error = "node runtime reporting is not supported"
					continue
				}
				report, e := provider.HostReport(ctx)
				if e != nil || report == nil {
					item.Error = "node runtime report unavailable; check node version and API scope"
					continue
				}
				item.Report = *report
			}
		})
	}
	wg.Wait()
	return result, nil
}
