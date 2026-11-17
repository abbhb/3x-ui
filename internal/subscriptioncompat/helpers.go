package subscriptioncompat

import (
	"encoding/json"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func decryptedNode(node model.SubscriptionNode) (map[string]any, error) {
	raw, err := Decrypt("proxy", node.PayloadCiphertext)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrEncryptedUnavailable
	}
	payload, err := normalizeNode(value)
	if err != nil {
		return nil, ErrEncryptedUnavailable
	}
	return payload, nil
}

func allowedKeys(v map[string]any, keys string) bool {
	allowed := map[string]bool{}
	for _, k := range strings.Fields(keys) {
		allowed[k] = true
	}
	for key := range v {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func number(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	}
	return 0
}

func cloneMap(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
