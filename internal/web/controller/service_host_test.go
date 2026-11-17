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

func TestServiceHostDoesNotExposeAnnotationAsForwardingControl(t *testing.T) {
	engine := gin.New()
	NewServiceHostController(engine.Group("/panel/api"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panel/api/serviceHosts/line/3", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired annotation control is still writable: %d", response.Code)
	}
}

func TestCommerceRestrictedScopeBoundary(t *testing.T) {
	for _, scope := range []string{model.ApiScopeMonitor, model.ApiScopeNodeSync, model.ApiScopeNodeAdmin} {
		for _, test := range []struct{ method, path string }{{"GET", "/commerce/accounts"}, {"GET", "/commerce/settings"}, {"POST", "/commerce/accounts/5/credit"}, {"POST", "/commerce/clients/7/mgma"}, {"PUT", "/commerce/settings"}} {
			engine := gin.New()
			api := &APIController{}
			g := engine.Group("/panel/api")
			g.Use(func(c *gin.Context) { c.Set("api_token_scope", scope) }, api.enforceTokenScope)
			g.Handle(test.method, test.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(test.method, "/panel/api"+test.path, nil))
			if response.Code != http.StatusForbidden {
				t.Fatalf("restricted scope %s reached commerce: %s %s", scope, test.method, test.path)
			}
		}
	}
}
