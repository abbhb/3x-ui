package sub

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func mgmaFixture(t *testing.T) (*service.CommerceService, model.ClientRecord, *gin.Engine) {
	t.Helper()
	seedSubDB(t)
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-only-mgma-pepper-32-bytes-minimum")
	if err := database.GetDB().Model(&model.Setting{}).Where("key = ?", "subPath").Update("value", "/sub/").Error; err != nil {
		t.Fatal(err)
	}
	svc := &service.CommerceService{}
	p, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	p.PublicURL = "https://panel.example.test"
	p.Mode = "ephemeral"
	if err := svc.SaveSettings(p); err != nil {
		t.Fatal(err)
	}
	ib := model.Inbound{Port: 26511, Enable: true, Tag: "MGMA_TEST", Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`}
	if err := database.GetDB().Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	host := model.Host{InboundId: ib.Id, Address: "198.51.100.8"}
	if err := database.GetDB().Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	client := model.ClientRecord{Email: "7.synthetic", SubID: strings.Repeat("s", 43), UUID: "11111111-1111-4111-8111-111111111111", Enable: true, StrictHostBindings: true, ClientHostRuleIds: []int{host.Id}}
	if err := database.GetDB().Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&xray.ClientTraffic{Email: client.Email, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	return svc, client, newSubscriptionTestRouter(subscriptionTestRouterConfig{})
}

func mgmaGet(router *gin.Engine, path, peer, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = peer
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func mgmaIssuedPath(t *testing.T, issue service.MGMAIssued) string {
	t.Helper()
	u, err := url.Parse(issue.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

func TestMGMANativeFormatsRequireLatestBearer(t *testing.T) {
	svc, client, router := mgmaFixture(t)
	first, err := svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	if mgmaGet(router, mgmaIssuedPath(t, first), "192.0.2.1:1234", "").Code != 404 {
		t.Fatal("previous bearer survived issuance")
	}
	u, _ := url.Parse(second.URL)
	token := u.Query().Get("token")
	for _, path := range []string{"/sub/" + client.SubID, "/sub/" + client.SubID + "/", "/json/" + client.SubID, "/clash/" + client.SubID, subMihomoPath + client.SubID, subClashLegacyPath + client.SubID, "/sub/" + client.SubID + "/mihomo", "/sub/" + client.SubID + "/clash-meta", "/sub/" + client.SubID + "/shadowrocket"} {
		if w := mgmaGet(router, path, "192.0.2.1:1234", ""); w.Code != 404 {
			t.Fatalf("bare managed URL exposed %s: %d", path, w.Code)
		}
		w := mgmaGet(router, path+"?token="+token, "192.0.2.1:1234", "")
		if w.Code != 200 && (path != subClashLegacyPath+client.SubID || w.Code != 422) {
			t.Fatalf("valid grant %s status=%d", path, w.Code)
		}
		if w.Header().Get("Profile-Web-Page-Url") != "" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("MGMA response leaked a reusable profile URL or cache")
		}
	}
	w := mgmaGet(router, mgmaIssuedPath(t, second), "192.0.2.1:1234", "")
	decoded, err := base64.StdEncoding.DecodeString(w.Body.String())
	if err != nil || !strings.Contains(string(decoded), client.UUID) {
		t.Fatal("native credentials were not rendered under the valid grant")
	}
	if w := mgmaGet(router, mgmaIssuedPath(t, second), "192.0.2.1:1234", "text/html"); w.Code != 404 {
		t.Fatal("bearer rendered a browser subscription page")
	}
	if w := mgmaGet(router, "/sub/"+client.SubID+"?token=", "192.0.2.1:1234", ""); w.Code != 404 {
		t.Fatal("empty token fell back to a permanent URL")
	}
	if err := svc.RevokeMGMA(client.Id); err != nil {
		t.Fatal(err)
	}
	if w := mgmaGet(router, mgmaIssuedPath(t, second), "192.0.2.1:1234", ""); w.Code != 404 {
		t.Fatal("revoked token remained usable")
	}
}

func TestMGMAPermanentSingleUseExpiryAndPeerBoundary(t *testing.T) {
	svc, client, router := mgmaFixture(t)
	p, _ := svc.Settings()
	p.TTLSeconds = 0
	p.SourceMode = "custom"
	p.CustomCIDRs = []string{"192.0.2.0/24"}
	if err := svc.SaveSettings(p); err != nil {
		t.Fatal(err)
	}
	issue, err := svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	if issue.ExpiresAt != nil {
		t.Fatal("permanent grant has an expiry")
	}
	path := mgmaIssuedPath(t, issue)
	persisted, settingsErr := svc.Settings()
	if settingsErr != nil || persisted.SourceMode != "custom" || len(persisted.CustomCIDRs) != 1 {
		t.Fatalf("source policy persistence failed: %v mode=%s count=%d", settingsErr, persisted.SourceMode, len(persisted.CustomCIDRs))
	}
	u, _ := url.Parse(issue.URL)
	guard, authErr := svc.AuthorizeSubscription(client.SubID, u.Query().Get("token"), "192.0.2.1", true, false)
	if authErr != nil || guard == nil {
		t.Fatalf("direct authorization: %v", authErr)
	}
	if !svc.CheckSubscriptionGeneration(guard) {
		t.Fatal("generation check rejected valid permanent grant")
	}
	for range 2 {
		if w := mgmaGet(router, path, "192.0.2.1:1234", ""); w.Code != 200 {
			t.Fatalf("permanent grant could not be retried: status=%d", w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("X-Real-IP", "192.0.2.1")
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("forged forwarding headers bypassed the source policy")
	}
	p.SingleUse = true
	if err := svc.SaveSettings(p); err != nil {
		t.Fatal(err)
	}
	issue, err = svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	path = mgmaIssuedPath(t, issue)
	if w := mgmaGet(router, path, "192.0.2.1:1234", "text/html"); w.Code != 404 {
		t.Fatal("HTML grant was not rejected")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Go(func() { codes <- mgmaGet(router, path, "192.0.2.1:1234", "").Code })
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code != 404 {
			t.Fatalf("unexpected code %d", code)
		}
	}
	if success != 1 {
		t.Fatalf("single-use downloads=%d", success)
	}
	p.SingleUse = false
	if err := svc.SaveSettings(p); err != nil {
		t.Fatal(err)
	}
	if w := mgmaGet(router, path, "192.0.2.1:1234", ""); w.Code != 404 {
		t.Fatal("relaxed settings revived a consumed token")
	}
	issue, err = svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&model.PortalAccess{}).Where("client_id = ?", client.Id).Update("expires_at", time.Now().Add(-time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if w := mgmaGet(router, mgmaIssuedPath(t, issue), "192.0.2.1:1234", ""); w.Code != 404 {
		t.Fatal("expired token downloaded credentials")
	}
}

func TestMGMARegenerationDuringRenderingDiscardsAllNewCredentials(t *testing.T) {
	svc, client, _ := mgmaFixture(t)
	issue, err := svc.IssueMGMA(client.Id)
	if err != nil {
		t.Fatal(err)
	}
	controller := newTestSUBController()
	router := gin.New()
	router.GET("/sub/:subid", controller.mgmaAccess, func(c *gin.Context) {
		if err := svc.RegenerateSubscription(client.Id, 0); err != nil {
			t.Fatal(err)
		}
		var current model.ClientRecord
		if err := database.GetDB().First(&current, client.Id).Error; err != nil {
			t.Fatal(err)
		}
		c.Header("Profile-Web-Page-Url", "https://should-not-leak.example.test/")
		c.String(200, "new credentials: %s", current.UUID)
	})
	u, _ := url.Parse(issue.URL)
	u.Path = strings.TrimSuffix(u.Path, "/")
	w := mgmaGet(router, u.RequestURI(), "192.0.2.1:1234", "")
	if w.Code != 404 || strings.Contains(w.Body.String(), "new credentials") || w.Header().Get("Profile-Web-Page-Url") != "" {
		t.Fatal("older bearer received the new subscription generation")
	}
}

func TestMGMANodeSnapshotCannotExposeUnbackedPermanentSubscription(t *testing.T) {
	_, client, router := mgmaFixture(t)
	var link model.ClientInbound
	if err := database.GetDB().Where("client_id = ?", client.Id).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	incoming := client.ToClient()
	incoming.RequireMGMA = true
	if err := (&service.ClientService{}).SyncInbound(database.GetDB(), link.InboundId, []model.Client{*incoming}); err != nil {
		t.Fatal(err)
	}
	incoming.RequireMGMA = false
	if err := (&service.ClientService{}).SyncInbound(database.GetDB(), link.InboundId, []model.Client{*incoming}); err != nil {
		t.Fatal(err)
	}
	var current model.ClientRecord
	if err := database.GetDB().First(&current, client.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !current.RequireMGMA {
		t.Fatal("older node snapshot removed the subscription fence")
	}
	for _, prefix := range []string{"/sub/", "/json/", "/clash/", "/mihomo/"} {
		if w := mgmaGet(router, prefix+client.SubID, "192.0.2.1:1234", ""); w.Code != 404 {
			t.Fatal("node without local MGMA state served a permanent subscription")
		}
	}
}

func TestMGMASharedSubIDCannotBypassManagedIdentity(t *testing.T) {
	svc, client, router := mgmaFixture(t)
	if _, err := svc.IssueMGMA(client.Id); err != nil {
		t.Fatal(err)
	}
	other := model.ClientRecord{Email: "8.synthetic", SubID: client.SubID, Enable: true, UUID: "22222222-2222-4222-8222-222222222222"}
	if err := database.GetDB().Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "?token=" + strings.Repeat("G", 43)} {
		if w := mgmaGet(router, "/sub/"+client.SubID+suffix, "192.0.2.1:1234", ""); w.Code != 404 {
			t.Fatal("shared subscription path bypassed managed access")
		}
	}
	if _, err := svc.IssueMGMA(other.Id); err == nil {
		t.Fatal("shared subscription ID received a bearer")
	}
}

func TestMGMAImportedSourceCredentialsAndLegacyPaths(t *testing.T) {
	seedPersistedSubscriptions(t)
	const legacyKey = "synthetic-legacy-key-at-least-thirty-two-bytes"
	svc := &service.CommerceService{}
	settings, _ := svc.Settings()
	settings.Enabled = true
	settings.PublicURL = "https://portal.example.test"
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate("SAMPLE_A", "synthetic-password"); err != nil {
		t.Fatal("source bcrypt password or case-insensitive login was not preserved")
	}
	router := newSubscriptionTestRouter(subscriptionTestRouterConfig{})
	stable := strings.Repeat("A", 43)
	path := "/sub/" + stable + "?token=" + strings.Repeat("G", 43)
	w := mgmaGet(router, path, "192.0.2.1:1234", "")
	decoded, err := base64.StdEncoding.DecodeString(w.Body.String())
	if w.Code != 200 || err != nil || !strings.Contains(string(decoded), "11111111-1111-4111-8111-111111111111") || !strings.Contains(string(decoded), "synthetic-hy2-a") {
		t.Fatal("source bearer did not preserve the actual native VLESS and HY2 subscription")
	}
	body := base64.RawURLEncoding.EncodeToString([]byte("sample_a," + strconv.FormatInt(time.Now().Unix(), 10)))
	hash := sha256.Sum256([]byte(body + legacyKey))
	legacy := body + base64.URLEncoding.EncodeToString(hash[:])[:10]
	if w := mgmaGet(router, "/sub/"+legacy, "192.0.2.1:1234", ""); w.Code != 200 {
		t.Fatal("old signed source URL was not resolved")
	}
	if w := mgmaGet(router, "/sub/"+stable, "192.0.2.1:1234", ""); w.Code != 404 {
		t.Fatal("legacy mode permitted a bare stable MGMA path")
	}
	if err := svc.RegenerateSubscription(7, 5); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{path, "/sub/" + legacy} {
		if w := mgmaGet(router, old, "192.0.2.1:1234", ""); w.Code != 404 {
			t.Fatal("full regeneration retained an old source subscription")
		}
	}
}
