package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
)

// XrayRequest invokes the selected panel's native controller. It does not keep
// a second template on the master. The controller checks the caller's scope;
// the destination independently requires a node-admin token.
func (r *Remote) XrayRequest(ctx context.Context, method, endpoint string, query url.Values, body any) (json.RawMessage, error) {
	const prefix = "panel/api/xray/"
	if !strings.HasPrefix(endpoint, prefix) || strings.ContainsAny(endpoint, "?%#\\") ||
		strings.TrimSuffix(path.Clean(endpoint), "/") != strings.TrimSuffix(endpoint, "/") {
		return nil, errors.New("invalid native Xray endpoint")
	}
	query = query.Clone()
	query.Del("nodeId") // Never recurse into another node's registry.
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	response, err := r.doWithTimeout(ctx, method, endpoint, body, 45*time.Second)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
