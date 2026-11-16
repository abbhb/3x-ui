package controller

import (
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func portalAdminRows[T any](c *gin.Context, query *gorm.DB) {
	rows := []T{}
	page := portalPage(c)
	if page < 1 || page > 100000 {
		portalReply(c, nil, service.ErrPortalInvalid, true)
		return
	}
	err := query.Offset((page - 1) * 100).Find(&rows).Error
	portalReply(c, rows, err, true)
}

// The parent router supplies full-admin authentication and CSRF protection.
// Restricted node and monitor scopes have no commerce allowlist entries.
func NewCommerceController(api *gin.RouterGroup) {
	svc := &service.CommerceService{}
	group := api.Group("/commerce")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	})
	group.GET("/settings", func(c *gin.Context) {
		p, err := svc.Settings()
		portalReply(c, gin.H{"settings": p, "pepper_configured": len(os.Getenv("XUI_MGMA_TOKEN_PEPPER")) >= 32, "legacy_key_configured": len(os.Getenv("XUI_MARZBAN_SUBSCRIPTION_KEY")) >= 32}, err, true)
	})
	group.PUT("/settings", func(c *gin.Context) {
		var p model.PortalSettings
		if c.ShouldBindJSON(&p) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		portalReply(c, nil, svc.SaveSettings(p), true)
	})
	group.GET("/plans", func(c *gin.Context) { value, err := svc.Plans(false); portalReply(c, value, err, true) })
	group.POST("/plans", func(c *gin.Context) {
		var p model.PortalPlan
		if c.ShouldBindJSON(&p) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		value, err := svc.SavePlan(0, p)
		portalReply(c, value, err, true)
	})
	group.PUT("/plans/:id", func(c *gin.Context) {
		var p model.PortalPlan
		if c.ShouldBindJSON(&p) != nil || portalID(c) == 0 {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		value, err := svc.SavePlan(portalID(c), p)
		portalReply(c, value, err, true)
	})
	group.POST("/accounts", func(c *gin.Context) {
		var input struct {
			Username string `json:"username"`
			PlanID   int    `json:"plan_id"`
		}
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		value, err := svc.CreateManagedAccount(input.Username, input.PlanID, portalActor(c))
		portalReply(c, value, err, true)
	})
	group.GET("/accounts", func(c *gin.Context) {
		value, err := svc.Accounts(portalPage(c), c.Query("search"))
		portalReply(c, value, err, true)
	})
	group.GET("/accounts/:id", func(c *gin.Context) { value, err := svc.Account(portalID(c)); portalReply(c, value, err, true) })
	group.GET("/accounts/:id/wallet", func(c *gin.Context) {
		value, err := svc.Wallet(portalID(c), portalPage(c))
		portalReply(c, value, err, true)
	})
	group.GET("/accounts/:id/purchases", func(c *gin.Context) {
		portalAdminRows[model.PortalPurchase](c, database.GetDB().Where("account_id = ?", portalID(c)).Order("id DESC").Limit(100))
	})
	for path, kind := range map[string]string{"credit": "admin_credit", "grant": "admin_grant", "renew": "admin_renewal", "disable": "admin_disable"} {
		group.POST("/accounts/:id/"+path, func(c *gin.Context) {
			var input service.PortalMutation
			if c.ShouldBindJSON(&input) != nil {
				portalReply(c, nil, service.ErrPortalInvalid, true)
				return
			}
			input.Kind = kind
			input.Actor = portalActor(c)
			value, err := svc.RunMutation(portalID(c), c.GetHeader("Idempotency-Key"), input)
			portalReply(c, value, err, true)
		})
	}
	group.POST("/accounts/:id/enabled", func(c *gin.Context) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Enabled == nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		portalReply(c, nil, svc.SetAccountEnabled(portalID(c), *input.Enabled), true)
	})
	group.POST("/accounts/:id/reset-password", func(c *gin.Context) {
		password, err := service.NewPortalPassword()
		if err == nil {
			err = svc.ChangePassword(portalID(c), "", password, true)
		}
		portalReply(c, gin.H{"password": password}, err, true)
	})
	group.GET("/invitations", func(c *gin.Context) {
		portalAdminRows[model.PortalInvitation](c, database.GetDB().Order("id DESC").Limit(100))
	})
	group.POST("/invitations", func(c *gin.Context) {
		var input model.PortalInvitation
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		input.ActorAdmin = portalActor(c)
		value, err := svc.CreateInvitation(input)
		portalReply(c, value, err, true)
	})
	group.POST("/invitations/:id/disable", func(c *gin.Context) {
		err := database.GetDB().Model(&model.PortalInvitation{}).Where("id = ?", portalID(c)).Update("disabled_at", time.Now().UnixMilli()).Error
		portalReply(c, nil, err, true)
	})
	group.GET("/security/blocks", func(c *gin.Context) {
		portalAdminRows[model.PortalIPBlock](c, database.GetDB().Order("id DESC").Limit(100))
	})
	group.POST("/security/blocks", func(c *gin.Context) {
		var row model.PortalIPBlock
		if c.ShouldBindJSON(&row) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		row.ActorAdmin = portalActor(c)
		value, err := svc.AddIPBlock(row)
		portalReply(c, value, err, true)
	})
	group.POST("/security/blocks/:id/revoke", func(c *gin.Context) { portalReply(c, nil, svc.RevokeIPBlock(portalID(c)), true) })
	group.GET("/clients/:id/next-plan", func(c *gin.Context) {
		var p model.PortalNextPlan
		err := database.GetDB().First(&p, "client_id = ?", portalID(c)).Error
		portalReply(c, p, err, true)
	})
	group.PUT("/clients/:id/next-plan", func(c *gin.Context) {
		var p model.PortalNextPlan
		if c.ShouldBindJSON(&p) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, true)
			return
		}
		p.ClientID = portalID(c)
		portalReply(c, nil, svc.SaveNextPlan(p), true)
	})
	group.DELETE("/clients/:id/next-plan", func(c *gin.Context) {
		portalReply(c, nil, database.GetDB().Where("client_id = ?", portalID(c)).Delete(&model.PortalNextPlan{}).Error, true)
	})
	group.POST("/clients/:id/mgma", func(c *gin.Context) { value, err := svc.IssueMGMA(portalID(c)); portalReply(c, value, err, true) })
	group.DELETE("/clients/:id/mgma", func(c *gin.Context) { portalReply(c, nil, svc.RevokeMGMA(portalID(c)), true) })
	group.POST("/clients/:id/regenerate", func(c *gin.Context) { portalReply(c, nil, svc.RegenerateSubscription(portalID(c), 0), true) })
	group.GET("/runtime", func(c *gin.Context) {
		portalAdminRows[model.PortalRuntimeChange](c, database.GetDB().Order("id DESC").Limit(100))
	})
	group.POST("/runtime/retry", func(c *gin.Context) { portalReply(c, nil, svc.Tick(), true) })
	NewSubscriptionAdminController(group)
}
