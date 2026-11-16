package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type SubscriptionSnapshotInput struct {
	YAML string `json:"yaml"`
}
type SubscriptionCredentialInput struct {
	URL string `json:"url"`
}

type SubscriptionHostView struct {
	HostID    int                    `json:"host_id"`
	InboundID int                    `json:"inbound_id"`
	Remark    string                 `json:"remark"`
	Address   string                 `json:"address"`
	Network   string                 `json:"network"`
	Path      string                 `json:"path"`
	Settings  model.SubscriptionHost `json:"settings"`
}

func NewSubscriptionAdminController(commerce *gin.RouterGroup) {
	newExternalSourceRoutes(commerce, true)
	group := commerce.Group("/subscriptions")
	group.GET("/cascade-entries", func(c *gin.Context) {
		selected, _ := strconv.Atoi(c.Query("selected_id"))
		value, err := (&subscriptioncompat.Service{DB: database.GetDB()}).EntryChoices(c.Query("search"), selected)
		portalReply(c, value, err, true)
	})
	group.GET("/catalog", func(c *gin.Context) {
		value, err := (&subscriptioncompat.Service{DB: database.GetDB()}).HostCatalog()
		portalReply(c, value, err, true)
	})
	group.GET("/policy", func(c *gin.Context) {
		value, err := (&subscriptioncompat.Service{DB: database.GetDB()}).Policy()
		portalReply(c, value, err, true)
	})
	group.PUT("/policy", func(c *gin.Context) {
		var input model.SubscriptionPolicy
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		value, err := (&subscriptioncompat.Service{DB: database.GetDB()}).SavePolicy(input)
		portalReply(c, value, err, true)
	})
	group.GET("/hosts", func(c *gin.Context) {
		views := []SubscriptionHostView{}
		page := portalPage(c)
		if page < 1 || page > 100000 || len(c.Query("search")) > 256 {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		var hosts []model.Host
		err := database.GetDB().Where("remark LIKE ? OR address LIKE ?", "%"+c.Query("search")+"%", "%"+c.Query("search")+"%").Order("sort_order,id").Offset((page - 1) * 100).Limit(100).Find(&hosts).Error
		if err == nil {
			for _, host := range hosts {
				var ib model.Inbound
				if err = database.GetDB().First(&ib, host.InboundId).Error; err != nil {
					break
				}
				var stream map[string]any
				if json.Unmarshal([]byte(ib.StreamSettings), &stream) != nil {
					portalReply(c, nil, service.ErrPortalInvalid, true)
					return
				}
				network, _ := stream["network"].(string)
				transport, _ := stream["xhttpSettings"].(map[string]any)
				if transport == nil {
					transport, _ = stream["splithttpSettings"].(map[string]any)
				}
				path, _ := transport["path"].(string)
				settings, e := (&subscriptioncompat.Service{DB: database.GetDB()}).Host(host.Id)
				if e != nil {
					err = e
					break
				}
				views = append(views, SubscriptionHostView{HostID: host.Id, InboundID: host.InboundId, Remark: host.Remark, Address: host.Address, Network: network, Path: path, Settings: settings})
			}
		}
		portalReply(c, views, err, true)
	})
	group.GET("/hosts/:id", func(c *gin.Context) {
		value, err := subscriptionHostView(portalID(c))
		portalReply(c, value, err, true)
	})
	group.PUT("/hosts/:id", func(c *gin.Context) {
		var input subscriptioncompat.HostInput
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		value, err := (&subscriptioncompat.Service{DB: database.GetDB()}).SaveHost(portalID(c), input)
		portalReply(c, value, err, true)
	})
}

func newExternalSourceRoutes(parent *gin.RouterGroup, admin bool) {
	group := parent.Group("/external-subscriptions")
	owner := func(c *gin.Context) *int {
		if admin {
			return nil
		}
		a := c.MustGet("portal_account").(*model.PortalAccount)
		return &a.ID
	}
	svc := func() *subscriptioncompat.Service { return &subscriptioncompat.Service{DB: database.GetDB()} }
	group.Use(func(c *gin.Context) {
		if !admin && c.Request.Method != http.MethodGet {
			id := owner(c)
			if !service.PortalAllowAttempt("external-account:"+strconv.Itoa(*id), 20, 10*time.Minute) {
				c.AbortWithStatus(http.StatusTooManyRequests)
				return
			}
		}
		c.Header("Cache-Control", "private, no-store")
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		value, err := svc().Sources(owner(c), portalPage(c), c.Query("search"))
		portalReply(c, value, err, admin)
	})
	group.GET("/:id", func(c *gin.Context) {
		value, err := svc().Source(portalID(c), owner(c))
		portalReply(c, value, err, admin)
	})
	group.POST("", func(c *gin.Context) {
		var input subscriptioncompat.SourceInput
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, admin)
			return
		}
		value, err := svc().CreateSource(owner(c), input)
		portalReply(c, value, err, admin)
	})
	group.PUT("/:id", func(c *gin.Context) {
		var input subscriptioncompat.SourceInput
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, admin)
			return
		}
		value, err := svc().UpdateSource(portalID(c), owner(c), input)
		portalReply(c, value, err, admin)
	})
	group.DELETE("/:id", func(c *gin.Context) { portalReply(c, nil, svc().DeleteSource(portalID(c), owner(c)), admin) })
	group.POST("/:id/credential", func(c *gin.Context) {
		var input SubscriptionCredentialInput
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, admin)
			return
		}
		value, err := svc().ReplaceCredential(portalID(c), owner(c), input.URL)
		portalReply(c, value, err, admin)
	})
	group.DELETE("/:id/credential", func(c *gin.Context) {
		value, err := svc().ReplaceCredential(portalID(c), owner(c), "")
		portalReply(c, value, err, admin)
	})
	group.POST("/:id/refresh", func(c *gin.Context) {
		value, err := svc().Refresh(c.Request.Context(), portalID(c), owner(c))
		portalReply(c, value, err, admin)
	})
	group.POST("/:id/snapshot", func(c *gin.Context) {
		var input SubscriptionSnapshotInput
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, admin)
			return
		}
		value, err := svc().ImportSnapshot(portalID(c), owner(c), []byte(input.YAML))
		portalReply(c, value, err, admin)
	})
	group.GET("/:id/nodes", func(c *gin.Context) {
		value, err := svc().Nodes(portalID(c), owner(c), portalPage(c))
		portalReply(c, value, err, admin)
	})
}

func subscriptionHostView(id int) (SubscriptionHostView, error) {
	var view SubscriptionHostView
	var host model.Host
	if err := database.GetDB().First(&host, id).Error; err != nil {
		return view, err
	}
	var ib model.Inbound
	if err := database.GetDB().First(&ib, host.InboundId).Error; err != nil {
		return view, err
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(ib.StreamSettings), &stream); err != nil {
		return view, err
	}
	network, _ := stream["network"].(string)
	tr, _ := stream["xhttpSettings"].(map[string]any)
	if tr == nil {
		tr, _ = stream["splithttpSettings"].(map[string]any)
	}
	path, _ := tr["path"].(string)
	settings, err := (&subscriptioncompat.Service{DB: database.GetDB()}).Host(id)
	return SubscriptionHostView{HostID: id, InboundID: host.InboundId, Remark: host.Remark, Address: host.Address, Network: network, Path: path, Settings: settings}, err
}
