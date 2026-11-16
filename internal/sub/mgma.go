package sub

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Nothing reaches the wire until the grant generation has been rechecked.
// This prevents concurrent regeneration from exposing new credentials to an
// older bearer while the native subscription renderer performs its reads.
type mgmaResponseBuffer struct {
	gin.ResponseWriter
	HeaderMap http.Header
	Body      bytes.Buffer
	Code      int
	Overflow  bool
}

func (w *mgmaResponseBuffer) Header() http.Header { return w.HeaderMap }
func (w *mgmaResponseBuffer) WriteHeader(code int) {
	if w.Code == 0 {
		w.Code = code
	}
}

func (w *mgmaResponseBuffer) WriteHeaderNow() {
	if w.Code == 0 {
		w.Code = http.StatusOK
	}
}

func (w *mgmaResponseBuffer) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	if w.Body.Len()+len(body) > 32<<20 {
		w.Overflow = true
		return len(body), nil
	}
	return w.Body.Write(body)
}
func (w *mgmaResponseBuffer) WriteString(body string) (int, error) { return w.Write([]byte(body)) }
func (w *mgmaResponseBuffer) Status() int {
	if w.Code == 0 {
		return http.StatusOK
	}
	return w.Code
}
func (w *mgmaResponseBuffer) Size() int     { return w.Body.Len() }
func (w *mgmaResponseBuffer) Written() bool { return w.Code != 0 }
func (w *mgmaResponseBuffer) Flush()        { w.WriteHeaderNow() }

func mgmaHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")
	c.Writer.Header().Del("Profile-Web-Page-Url")
}

func (a *SUBController) mgmaAccess(c *gin.Context) {
	svc := &service.CommerceService{}
	query, queryErr := url.ParseQuery(c.Request.URL.RawQuery)
	values, hasToken := query["token"]
	token := ""
	if len(values) == 1 {
		token = values[0]
	}
	source, sourceErr := service.PortalRealClientIP(c.Request)
	wantsHTML := strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/html") || c.Query("html") == "1" || strings.EqualFold(c.Query("view"), "html")
	format := c.Param("format")
	knownFormat := format == "" || format == "clash" || format == "clash-meta" || format == "shadowrocket" || format == "mihomo" || format == "v2ray-json" || format == "xray-json" || format == "json" || format == "v2ray" || format == "v2rayn" || format == "v2rayng" || format == "base64"
	consume := knownFormat && c.Request.Method == http.MethodGet && sourceErr == nil && queryErr == nil && !wantsHTML
	guard, err := svc.AuthorizeSubscription(c.Param("subid"), token, source, hasToken, consume)
	if err != nil || (hasToken && (queryErr != nil || len(values) != 1)) {
		mgmaHeaders(c)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if guard == nil {
		c.Next()
		return
	}
	mgmaHeaders(c)
	if sourceErr != nil || queryErr != nil || wantsHTML {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	for i := range c.Params {
		if c.Params[i].Key == "subid" {
			c.Params[i].Value = guard.SubID
		}
	}
	c.Set("mgma_authorization", guard)
	original := c.Writer
	buffer := &mgmaResponseBuffer{ResponseWriter: original, HeaderMap: original.Header().Clone()}
	c.Writer = buffer
	c.Next()
	c.Writer = original
	if buffer.Overflow || !svc.CheckSubscriptionGeneration(guard) {
		mgmaHeaders(c)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	for key := range original.Header() {
		original.Header().Del(key)
	}
	for key, values := range buffer.HeaderMap {
		original.Header()[key] = values
	}
	mgmaHeaders(c)
	original.WriteHeader(buffer.Status())
	if c.Request.Method != http.MethodHead {
		_, _ = original.Write(buffer.Body.Bytes())
	}
}

func (a *SUBController) portalUsageInfo(c *gin.Context) {
	value, ok := c.Get("mgma_authorization")
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	guard := value.(*service.MGMAAuthorization)
	usage, err := (&service.CommerceService{}).MGMAUsage(guard.ClientID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, usage)
}

func (a *SUBController) mgmaExplicitFormat(c *gin.Context) {
	switch c.Param("format") {
	case "clash":
		a.subClashLegacy(c)
	case "clash-meta", "mihomo", "shadowrocket":
		a.subClashs(c)
	case "v2ray-json", "xray-json", "json":
		a.subJsons(c)
	case "v2ray", "v2rayn", "v2rayng", "base64":
		old := c.Request.Header.Get("User-Agent")
		c.Request.Header.Set("User-Agent", "v2ray")
		defer c.Request.Header.Set("User-Agent", old)
		a.subs(c)
	default:
		c.AbortWithStatus(http.StatusNotFound)
	}
}
