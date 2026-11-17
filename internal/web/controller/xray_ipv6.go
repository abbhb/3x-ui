package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func (a *XraySettingController) getIPv6Pool(c *gin.Context) {
	value, err := service.NativeIPv6PoolReport(c.Request.Context())
	jsonObj(c, value, err)
}

func (a *XraySettingController) configureIPv6Pool(c *gin.Context) {
	var pool service.NativeIPv6Pool
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&pool); err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, nil, service.ConfigureNativeIPv6Pool(c.Request.Context(), pool))
}

func (a *XraySettingController) allocateIPv6(c *gin.Context) {
	var input service.NativeIPv6AllocationInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&input); err != nil {
		jsonObj(c, nil, err)
		return
	}
	value, err := service.AllocateNativeIPv6(c.Request.Context(), input.ClientIDs)
	jsonObj(c, value, err)
}
