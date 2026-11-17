package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

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
	group.GET("/runtime/:id", func(c *gin.Context) {
		value, err := svc.Runtime(c.Request.Context(), hostParam(c, "id"))
		jsonObj(c, value, err)
	})
	api.GET("/server/hostRuntime", func(c *gin.Context) { jsonObj(c, service.LocalHostReport(), nil) })
}

func hostParam(c *gin.Context, name string) int {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id < 0 {
		return -1
	}
	return id
}
