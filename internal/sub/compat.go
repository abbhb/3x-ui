package sub

// compatHostGrant caches the portal plan's native Host entitlements per request.
type compatHostGrant struct {
	ids     []int
	managed bool
	err     error
}
