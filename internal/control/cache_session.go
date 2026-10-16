package control

// ProviderCacheSessionIdentity scopes transport caching to the bound storage
// incarnation. Runtime rebuilds retain it; imports and forks obtain a new one.
func (c *Controller) ProviderCacheSessionIdentity() string {
	_, runtime, ok := c.v3Binding()
	if !ok || runtime == nil {
		return ""
	}
	ref := runtime.Ref()
	return "session\x00" + ref.HostID + "\x00" + ref.SessionID + "\x00" + runtime.Session().StorageGeneration()
}
