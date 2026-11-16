package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/hostops"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func NewServiceHostController(api *gin.RouterGroup) {
	svc := &service.ServiceHostService{}
	group := api.Group("/serviceHosts")
	group.GET("/list", func(c *gin.Context) { value, err := svc.List(); jsonObj(c, value, err) })
	group.GET("/get/:id", func(c *gin.Context) { value, err := svc.Get(hostParam(c, "id")); jsonObj(c, value, err) })
	group.POST("/save/:id", func(c *gin.Context) {
		var input service.ServiceHostInput
		if err := c.ShouldBindJSON(&input); err != nil {
			jsonObj(c, nil, errors.New("invalid host settings"))
			return
		}
		value, err := svc.Save(hostParam(c, "id"), input)
		jsonObj(c, value, err)
	})
	group.POST("/del/:id", func(c *gin.Context) { jsonObj(c, nil, svc.Delete(hostParam(c, "id"))) })
	group.POST("/line/:id", func(c *gin.Context) {
		var input model.ServiceLine
		if err := c.ShouldBindJSON(&input); err != nil {
			jsonObj(c, nil, errors.New("invalid line settings"))
			return
		}
		jsonObj(c, nil, svc.SaveLine(hostParam(c, "id"), input))
	})
	group.GET("/runtime/:id", func(c *gin.Context) {
		value, err := svc.Runtime(c.Request.Context(), hostParam(c, "id"))
		jsonObj(c, value, err)
	})
	group.POST("/policy/:id/:node", func(c *gin.Context) {
		policy, ok := bindHostPolicy(c)
		if !ok {
			return
		}
		jsonObj(c, nil, svc.ApplyPolicy(c.Request.Context(), hostParam(c, "id"), hostParam(c, "node"), policy))
	})
	api.GET("/server/hostRuntime", func(c *gin.Context) { jsonObj(c, service.LocalHostReport(), nil) })
	api.POST("/server/hostEgress", func(c *gin.Context) {
		policy, ok := bindHostPolicy(c)
		if !ok {
			return
		}
		jsonObj(c, nil, service.SaveLocalHostPolicy(policy))
	})
}

func hostParam(c *gin.Context, name string) int {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id < 0 {
		return -1
	}
	return id
}

func bindHostPolicy(c *gin.Context) (hostops.Policy, bool) {
	var input struct {
		hostops.Policy
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		jsonObj(c, nil, errors.New("invalid egress policy; explicit enabled flag required"))
		return hostops.Policy{}, false
	}
	input.Policy.Enabled = *input.Enabled
	return input.Policy, true
}
