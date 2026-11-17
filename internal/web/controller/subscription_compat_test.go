package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRetiredSubscriptionRouteEndpointsAreUnavailable(t *testing.T) {
	router := gin.New()
	NewSubscriptionAdminController(router.Group("/panel/api/commerce"))
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/subscriptions/cascade-entries"},
		{http.MethodGet, "/subscriptions/hosts"},
		{http.MethodGet, "/subscriptions/hosts/1"},
		{http.MethodPut, "/subscriptions/hosts/1"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(request.method, "/panel/api/commerce"+request.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s returned %d, want 404", request.method, request.path, response.Code)
		}
	}
}
