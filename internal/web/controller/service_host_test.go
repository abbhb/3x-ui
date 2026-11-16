package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestServiceHostRestrictedScopeBoundary(t *testing.T) {
	for _, test := range []struct {
		scope, method, path string
		allowed             bool
	}{
		{model.ApiScopeMonitor, http.MethodGet, "/server/hostRuntime", false},
		{model.ApiScopeNodeSync, http.MethodGet, "/server/hostRuntime", true},
		{model.ApiScopeNodeSync, http.MethodPost, "/server/hostEgress", false},
		{model.ApiScopeNodeAdmin, http.MethodPost, "/server/hostEgress", true},
		{model.ApiScopeNodeAdmin, http.MethodPost, "/serviceHosts/save/0", false},
	} {
		engine := gin.New()
		api := &APIController{}
		group := engine.Group("/panel/api")
		group.Use(func(c *gin.Context) { c.Set("api_token_scope", test.scope) }, api.enforceTokenScope)
		group.Handle(test.method, test.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(test.method, "/panel/api"+test.path, nil))
		if (response.Code == http.StatusNoContent) != test.allowed {
			t.Fatalf("scope=%s path=%s code=%d", test.scope, test.path, response.Code)
		}
	}
}
