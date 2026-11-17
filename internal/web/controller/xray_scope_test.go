package controller

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

func TestNativeXrayDispatchKeepsMasterAndOtherServicesUntouched(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", root)
	dbtest.InitDB(t, filepath.Join(root, "panel.db"))
	db := database.GetDB()
	if err := db.Create(&model.Setting{Key: "xrayTemplateConfig", Value: "master-original"}).Error; err != nil {
		t.Fatal(err)
	}
	var posted string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Has("nodeId") {
			t.Error("forwarded recursive scope")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		posted = r.PostForm.Get("xraySetting")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": "remote-native"})
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	portNum, _ := strconv.Atoi(port)
	node := model.Node{Name: "selected", Address: host, Port: portNum, Scheme: "http", BasePath: "/", ApiToken: "synthetic-token", Enable: true, AllowPrivateAddress: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	previous := runtime.GetManager()
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{}))
	t.Cleanup(func() { runtime.SetManager(previous) })
	engine := gin.New()
	NewXraySettingController(engine.Group("/panel/api"))
	request := httptest.NewRequest(http.MethodPost, "/panel/api/xray/update?nodeId="+strconv.Itoa(node.Id), strings.NewReader(url.Values{"xraySetting": {"remote-edited"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "remote-native") || posted != "remote-edited" || calls != 1 {
		t.Fatalf("dispatch failed: %s posted=%s calls=%d", response.Body, posted, calls)
	}
	var setting model.Setting
	if db.Where("key = ?", "xrayTemplateConfig").First(&setting).Error != nil || setting.Value != "master-original" {
		t.Fatal("remote save modified master template")
	}
	if err := db.Model(&node).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panel/api/xray/?nodeId="+strconv.Itoa(node.Id), nil))
	if calls != 1 || !strings.Contains(response.Body.String(), "disabled") {
		t.Fatalf("reached disabled service: %s", response.Body)
	}
}

func TestNativeXrayNodeTokenCannotDispatchToAnotherNode(t *testing.T) {
	engine := gin.New()
	group := engine.Group("/panel/api")
	group.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeNodeAdmin) })
	NewXraySettingController(group)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panel/api/xray/?nodeId=7", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("node token escaped local scope: %d %s", response.Code, response.Body)
	}
}
