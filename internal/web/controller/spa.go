package controller

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"

	"github.com/gin-gonic/gin"
)

// XUIController is the main controller for the X-UI panel, serving the SPA shell.
type XUIController struct {
	BaseController
}

// NewXUIController creates a new XUIController and initializes its routes.
func NewXUIController(g *gin.RouterGroup) *XUIController {
	a := &XUIController{}
	a.initRouter(g)
	return a
}

// Admin documents have their own namespace. Legacy /panel document links
// redirect there while existing panel API and CSRF paths keep their contracts.
func (a *XUIController) initRouter(g *gin.RouterGroup) {
	g.GET("/admin", a.adminPage)
	g.GET("/admin/*page", a.adminPage)

	g = g.Group("/panel")
	g.Use(middleware.CSRFMiddleware(getRemoteIp))

	g.GET("/", a.legacyPanelPage)
	g.GET("/inbounds", a.legacyPanelPage)
	g.GET("/clients", a.legacyPanelPage)
	g.GET("/groups", a.legacyPanelPage)
	g.GET("/nodes", a.legacyPanelPage)
	g.GET("/settings", a.legacyPanelPage)
	g.GET("/xray", a.legacyPanelPage)
	g.GET("/outbound", a.legacyPanelPage)
	g.GET("/routing", a.legacyPanelPage)
	g.GET("/api-docs", a.legacyPanelPage)

	// SPA pages built by Vite don't have a server-rendered <meta name="csrf-token">,
	// so they fetch the session token via this endpoint at startup and replay it
	// on subsequent unsafe requests.
	g.GET("/csrf-token", a.checkLogin, a.csrfToken)
}

// Serve the login form at the requested admin URL so signing in can resume a deep link.
func (a *XUIController) adminPage(c *gin.Context) {
	page := c.Param("page")
	if isStaticAssetPath(page) || !acceptsHTML(c.GetHeader("Accept")) {
		c.Status(http.StatusNotFound)
		return
	}
	if !session.IsLogin(c) {
		if isAjax(c) {
			pureJsonMsg(c, http.StatusUnauthorized, false, I18nWeb(c, "pages.login.loginAgain"))
			return
		}
		serveDistPage(c, "login.html")
		return
	}
	serveDistPage(c, "index.html")
}

func (a *XUIController) legacyPanelPage(c *gin.Context) {
	if isAjax(c) && !session.IsLogin(c) {
		pureJsonMsg(c, http.StatusUnauthorized, false, I18nWeb(c, "pages.login.loginAgain"))
		return
	}
	base := normalizeWebBasePath(c.GetString("base_path"))
	suffix := strings.TrimPrefix(c.Request.URL.Path, base+"panel")
	target := url.URL{Path: base + "admin" + suffix, RawQuery: c.Request.URL.RawQuery}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusTemporaryRedirect, target.RequestURI())
}

// Redirect unregistered legacy document routes without swallowing API or asset misses.
func (a *XUIController) HandleNoRoutePanelSPA(c *gin.Context) bool {
	if !isPanelSPAFallbackRequest(c) {
		return false
	}

	a.legacyPanelPage(c)
	return true
}

func isPanelSPAFallbackRequest(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}
	if !acceptsHTML(c.GetHeader("Accept")) {
		return false
	}

	basePath := c.GetString("base_path")
	if basePath == "" {
		basePath = "/"
	}
	panelPath := strings.TrimRight(basePath, "/") + "/panel"

	reqPath := c.Request.URL.Path
	if reqPath != panelPath && !strings.HasPrefix(reqPath, panelPath+"/") {
		return false
	}

	if reqPath == panelPath+"/csrf-token" || strings.HasPrefix(reqPath, panelPath+"/csrf-token/") {
		return false
	}
	if reqPath == panelPath+"/api" || strings.HasPrefix(reqPath, panelPath+"/api/") {
		return false
	}
	if isStaticAssetPath(reqPath) {
		return false
	}
	return true
}

var staticAssetExts = map[string]struct{}{
	".js": {}, ".mjs": {}, ".cjs": {}, ".css": {}, ".map": {}, ".json": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".ico": {},
	".webp": {}, ".avif": {}, ".woff": {}, ".woff2": {}, ".ttf": {}, ".eot": {},
	".otf": {}, ".wasm": {}, ".txt": {}, ".xml": {}, ".webmanifest": {},
}

func isStaticAssetPath(reqPath string) bool {
	ext := strings.ToLower(path.Ext(reqPath))
	if ext == "" {
		return false
	}
	_, ok := staticAssetExts[ext]
	return ok
}

func acceptsHTML(accept string) bool {
	if accept == "" {
		return true
	}
	accept = strings.ToLower(accept)
	return strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
}

// csrfToken returns the session CSRF token to authenticated SPA clients.
// The endpoint is GET (a safe method) so it bypasses CSRFMiddleware itself,
// but checkLogin still gates the response — anonymous callers get 401/redirect.
func (a *XUIController) csrfToken(c *gin.Context) {
	token, err := session.EnsureCSRFToken(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, entity.Msg{Success: false, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, entity.Msg{Success: true, Obj: token})
}
