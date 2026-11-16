package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

func portalError(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrPortalAuth):
		return http.StatusUnauthorized, "Invalid credentials or account unavailable"
	case errors.Is(err, subscriptioncompat.ErrInvalid), errors.Is(err, subscriptioncompat.ErrInvalidSnapshot), errors.Is(err, subscriptioncompat.ErrBudget), errors.Is(err, service.ErrPortalInvalid):
		return http.StatusBadRequest, "Invalid request"
	case errors.Is(err, subscriptioncompat.ErrNotFound), errors.Is(err, service.ErrPortalUnavailable), errors.Is(err, service.ErrMGMARejected), errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound, "Resource unavailable"
	case errors.Is(err, subscriptioncompat.ErrConflict):
		return http.StatusConflict, "Configuration changed or is still in use; reload before saving"
	case errors.Is(err, service.ErrPortalConflict):
		return http.StatusConflict, "Idempotency key conflicts with the original request"
	case errors.Is(err, service.ErrPortalBalance):
		return http.StatusPaymentRequired, "Insufficient wallet balance"
	default:
		return http.StatusServiceUnavailable, "Operation unavailable; retry with the same idempotency key"
	}
}

func portalReply(c *gin.Context, value any, err error, admin bool) {
	c.Header("Cache-Control", "private, no-store")
	if err == nil {
		if admin {
			jsonObj(c, value, nil)
		} else {
			c.JSON(http.StatusOK, value)
		}
		return
	}
	status, message := portalError(err)
	if admin {
		c.JSON(status, gin.H{"success": false, "msg": message})
	} else {
		c.JSON(status, gin.H{"detail": message})
	}
}

func portalID(c *gin.Context) int {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func portalPage(c *gin.Context) int {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		return 0
	}
	return page
}

func portalActor(c *gin.Context) string {
	if name := c.GetString("api_token_name"); name != "" {
		return "api:" + name
	}
	if user := session.GetLoginUser(c); user != nil {
		return user.Username
	}
	return ""
}

// Portal JWTs never authorize panel APIs. Panel cookies never authorize portal APIs.
func NewPortalController(g *gin.RouterGroup) {
	svc := &service.CommerceService{}
	g.GET("/portal", func(c *gin.Context) { serveDistPage(c, "portal.html") })
	g.GET("/portal/*path", func(c *gin.Context) { serveDistPage(c, "portal.html") })
	portal := g.Group("/api/portal")
	portal.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Robots-Tag", "noindex, nofollow")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	})
	portal.GET("/config", func(c *gin.Context) {
		p, err := svc.Settings()
		portalReply(c, gin.H{"enabled": p.Enabled}, err, false)
	})
	portal.POST("/register", func(c *gin.Context) {
		ip, err := service.PortalRealClientIP(c.Request)
		if err != nil {
			portalReply(c, nil, service.ErrPortalAuth, false)
			return
		}
		if !service.PortalAllowAttempt("register:"+ip, 5, time.Minute) {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		var input struct {
			Username   string `json:"username"`
			Password   string `json:"password"`
			Invitation string `json:"invitation_code"`
		}
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalAuth, false)
			return
		}
		account, err := svc.Register(input.Username, input.Password, input.Invitation, ip)
		if err != nil {
			portalReply(c, nil, err, false)
			return
		}
		view, err := svc.Account(account.ID)
		portalReply(c, view, err, false)
	})
	portal.POST("/token", func(c *gin.Context) {
		ip, err := service.PortalRealClientIP(c.Request)
		if err != nil {
			portalReply(c, nil, service.ErrPortalAuth, false)
			return
		}
		var input struct {
			Username string `json:"username" form:"username"`
			Password string `json:"password" form:"password"`
		}
		if c.ShouldBind(&input) != nil || len(input.Username) > 128 {
			portalReply(c, nil, service.ErrPortalAuth, false)
			return
		}
		key := "login-user:" + ip + ":" + strings.ToLower(strings.TrimSpace(input.Username))
		if !service.PortalAllowAttempt("login-ip:"+ip, 60, 5*time.Minute) || !service.PortalAllowAttempt(key, 5, 5*time.Minute) {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		account, err := svc.Authenticate(input.Username, input.Password)
		if err != nil {
			portalReply(c, nil, err, false)
			return
		}
		token, err := svc.LoginToken(*account)
		if err != nil {
			portalReply(c, nil, err, false)
			return
		}
		service.PortalClearAttempt(key)
		portalReply(c, gin.H{"access_token": token, "token_type": "bearer"}, nil, false)
	})
	authed := portal.Group("")
	authed.Use(func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		account, err := svc.VerifyLoginToken(token)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("portal_account", account)
	})
	account := func(c *gin.Context) *model.PortalAccount { return c.MustGet("portal_account").(*model.PortalAccount) }
	newExternalSourceRoutes(authed, false)
	authed.GET("/me", func(c *gin.Context) { value, err := svc.Account(account(c).ID); portalReply(c, value, err, false) })
	authed.GET("/plans", func(c *gin.Context) { value, err := svc.Plans(true); portalReply(c, value, err, false) })
	authed.GET("/wallet/transactions", func(c *gin.Context) {
		value, err := svc.Wallet(account(c).ID, portalPage(c))
		portalReply(c, value, err, false)
	})
	authed.POST("/purchase", func(c *gin.Context) {
		var input struct {
			PlanID int `json:"plan_id"`
		}
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, false)
			return
		}
		value, err := svc.RunMutation(account(c).ID, c.GetHeader("Idempotency-Key"), service.PortalMutation{Kind: "self_purchase", PlanID: input.PlanID})
		portalReply(c, value, err, false)
	})
	authed.POST("/password/change", func(c *gin.Context) {
		var input struct {
			Current string `json:"current_password"`
			Next    string `json:"new_password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			portalReply(c, nil, service.ErrPortalInvalid, false)
			return
		}
		if err := svc.ChangePassword(account(c).ID, input.Current, input.Next, false); err != nil {
			portalReply(c, nil, err, false)
			return
		}
		c.Status(http.StatusNoContent)
	})
	authed.POST("/mgma", func(c *gin.Context) {
		a := account(c)
		if a.ClientID == nil {
			portalReply(c, nil, service.ErrPortalUnavailable, false)
			return
		}
		value, err := svc.IssueMGMA(*a.ClientID)
		portalReply(c, value, err, false)
	})
	authed.DELETE("/mgma", func(c *gin.Context) {
		a := account(c)
		if a.ClientID == nil {
			portalReply(c, nil, service.ErrPortalUnavailable, false)
			return
		}
		portalReply(c, nil, svc.RevokeMGMA(*a.ClientID), false)
	})
	authed.POST("/subscription/regenerate", func(c *gin.Context) {
		a := account(c)
		if a.ClientID == nil {
			portalReply(c, nil, service.ErrPortalUnavailable, false)
			return
		}
		portalReply(c, nil, svc.RegenerateSubscription(*a.ClientID, a.ID), false)
	})
}

// Enforced before body parsing; full-admin recovery APIs remain reachable.
func PortalSecurityGuard() gin.HandlerFunc {
	svc := &service.CommerceService{}
	return func(c *gin.Context) {
		path := strings.Trim(strings.TrimPrefix(c.Request.URL.Path, c.GetString("base_path")), "/")
		kind := ""
		if c.Request.Method == http.MethodPost {
			switch path {
			case "login":
				kind = "admin_login"
			case "api/portal/token":
				kind = "portal_login"
			case "api/portal/register":
				kind = "portal_registration"
			}
		}
		if kind == "" && !strings.HasPrefix(path, "api/portal/") {
			c.Next()
			return
		}
		settings, err := svc.Settings()
		if err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		if !settings.Enabled {
			c.Next()
			return
		}
		ip, err := service.PortalRealClientIP(c.Request)
		if err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		blocked, err := svc.IPBlocked(ip)
		if err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		if blocked {
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
		if kind != "" {
			success := c.Writer.Status() >= 200 && c.Writer.Status() < 300
			if kind == "admin_login" {
				success = c.GetBool("portal_auth_success")
			}
			if success || c.Writer.Status() == 400 || c.Writer.Status() == 401 || c.Writer.Status() == 403 || c.Writer.Status() == 409 || c.Writer.Status() == 422 || c.Writer.Status() == 429 || (kind == "admin_login" && c.Writer.Status() == 200) {
				_ = svc.RecordAuthAttempt(ip, kind, success)
			}
		}
	}
}
