package controller

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// dispatchScope extends the existing routes instead of adding a parallel
// configuration API. Only a session/admin may select a remote registry entry.
func (a *XraySettingController) dispatchScope(c *gin.Context) {
	values, present := c.Request.URL.Query()["nodeId"]
	if !present {
		c.Next()
		return
	}
	id, err := strconv.Atoi(values[0])
	if len(values) != 1 || err != nil || id < 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "msg": "invalid service nodeId"})
		return
	}
	if id == 0 {
		c.Next()
		return
	}
	if scope := c.GetString("api_token_scope"); scope != "" && scope != model.ApiScopeAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "msg": "node tokens manage only their local service"})
		return
	}
	var node model.Node
	if database.GetDB().First(&node, id).Error != nil || !node.Enable {
		jsonObj(c, nil, errors.New("service is missing or disabled"))
		c.Abort()
		return
	}
	mgr := runtime.GetManager()
	if mgr == nil {
		jsonObj(c, nil, errors.New("runtime manager unavailable"))
		c.Abort()
		return
	}
	remote, err := mgr.RemoteFor(&node)
	var body any
	if err == nil && c.Request.Body != nil && c.Request.ContentLength != 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
		switch mediaType {
		case "application/json":
			var raw []byte
			raw, err = io.ReadAll(c.Request.Body)
			if err == nil && !json.Valid(raw) {
				err = errors.New("invalid JSON request")
			}
			body = json.RawMessage(raw)
		case "application/x-www-form-urlencoded", "":
			err = c.Request.ParseForm()
			body = c.Request.PostForm
		default:
			err = errors.New("unsupported native Xray request encoding")
		}
	}
	if err == nil {
		endpoint := "panel/api" + relAPIPath(c.Request.URL.Path)
		var response json.RawMessage
		response, err = remote.XrayRequest(c.Request.Context(), c.Request.Method, endpoint, c.Request.URL.Query(), body)
		if err == nil {
			c.Data(http.StatusOK, "application/json", response)
		}
	}
	if err != nil {
		jsonObj(c, nil, err)
	}
	c.Abort()
}
