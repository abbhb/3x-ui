package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

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

func TestPrivateSourcesSharePanelAndPortalOwnership(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "subscriptions.sqlite"))
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-portal-token-pepper-for-integration")
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "integration-credential-key-at-least-thirty-two-bytes")
	db := database.GetDB()
	commerce := &service.CommerceService{}
	settings, err := commerce.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.PublicURL = "https://portal.example.test"
	if err := commerce.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	makeOwner := func(name string) (model.PortalAccount, string) {
		t.Helper()
		c := model.ClientRecord{Email: name, SubID: "sub-" + name, Enable: true}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		a := model.PortalAccount{Username: name, IsActive: true, ClientID: &c.Id}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		token, err := commerce.LoginToken(a)
		if err != nil {
			t.Fatal(err)
		}
		return a, token
	}
	a, token := makeOwner("owner")
	b, otherToken := makeOwner("other")
	r := gin.New()
	NewCommerceController(r.Group("/panel/api"))
	NewPortalController(r.Group(""))
	request := func(method, path, auth, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		if out.Code != status {
			t.Fatalf("%s %s: got %d want %d", method, path, out.Code, status)
		}
		return out.Body.Bytes()
	}
	panel := fmt.Sprintf("/panel/api/commerce/subscriptions/clients/%d", *a.ClientID)
	portal := "/api/portal/external-subscriptions"
	created := request("POST", panel+"/external-subscriptions", "", "{\"name\":\"My private feed\",\"enabled\":true}", 200)
	var envelope struct{ Obj model.SubscriptionSource }
	if err := json.Unmarshal(created, &envelope); err != nil || envelope.Obj.OwnerAccountID == nil || *envelope.Obj.OwnerAccountID != a.ID {
		t.Fatalf("incorrect owner: %v", err)
	}
	path := fmt.Sprintf("/%d", envelope.Obj.ID)
	request("POST", portal+path+"/snapshot", token, `{"yaml":"trojan://synthetic-secret@private.test:443#My%20node"}`, 200)
	got := request("GET", panel+"/external-inventory", "", "", 200)
	var adminView struct {
		Obj subscriptioncompat.ClientSources
	}
	if err := json.Unmarshal(got, &adminView); err != nil {
		t.Fatal(err)
	}
	got = request("GET", "/api/portal/external-inventory", token, "", 200)
	var portalView subscriptioncompat.ClientSources
	if err := json.Unmarshal(got, &portalView); err != nil || !reflect.DeepEqual(adminView.Obj, portalView) || len(portalView.Sources) != 1 || strings.Contains(string(got), "synthetic-secret") {
		t.Fatal("panel and portal inventory disagree or expose credentials")
	}
	request("GET", portal+path, otherToken, "", 404)
	request("DELETE", fmt.Sprintf("/panel/api/commerce/subscriptions/clients/%d/external-subscriptions", *b.ClientID)+path, "", "", 404)
	request("GET", "/api/portal/external-inventory", "", "", 401)
	request("DELETE", panel+"/external-subscriptions"+path, "", "", 200)
	request("GET", portal+path, token, "", 404)
}
