package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRemoteXrayUsesNativeFormAndStripsRecursiveScope(t *testing.T) {
	var got url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panel/api/xray/update" || r.URL.Query().Has("nodeId") || r.URL.Query().Get("other") != "kept" {
			t.Errorf("wrong destination: %s", r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		got = r.PostForm
		_, _ = w.Write([]byte(`{"success":true,"msg":"applied","obj":null}`))
	}))
	defer server.Close()
	remote := NewRemote(nodeForPlainServer(t, server, "verify", "synthetic-token"), nil)
	query := url.Values{"nodeId": {"8"}, "other": {"kept"}}
	raw, err := remote.XrayRequest(context.Background(), http.MethodPost, "panel/api/xray/update", query, url.Values{"xraySetting": {`{"outbounds":[]}`}})
	if err != nil {
		t.Fatal(err)
	}
	var response envelope
	if json.Unmarshal(raw, &response) != nil || !response.Success || got.Get("xraySetting") != `{"outbounds":[]}` || query.Get("nodeId") != "8" {
		t.Fatalf("request/response drift: %s %v", raw, got)
	}
	for _, endpoint := range []string{"panel/api/settings/all", "panel/api/xray/../settings/all", "panel/api/xray/%2e%2e/settings", "https://example.test/"} {
		if _, err := remote.XrayRequest(context.Background(), http.MethodGet, endpoint, nil, nil); err == nil {
			t.Fatalf("forwarded arbitrary path: %s", endpoint)
		}
	}
}
