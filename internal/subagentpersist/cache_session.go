package subagentpersist

// Child and parent catalogs have separate namespaces even when a caller
// chooses the same session ID. Continuation retains the child's incarnation.
func (t *transcript) ProviderCacheSessionIdentity() string {
	ref := t.runtime.Ref()
	return "subagent\x00" + ref.HostID + "\x00" + ref.SessionID + "\x00" + t.runtime.Session().StorageGeneration()
}
