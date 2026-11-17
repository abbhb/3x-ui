package controller

import (
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

func NewSubscriptionAdminController(commerce *gin.RouterGroup) {
	newExternalSourceRoutes(commerce, true)
	group := commerce.Group("/subscriptions")
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
