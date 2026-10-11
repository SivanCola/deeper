package remote

import "deepzora/internal/config"

// defaultManagedKnownHosts is the Deepzora-managed known_hosts path. It is a
// thin indirection over config so tests can leave HostKeyPolicy.ManagedPath
// empty and still get an isolated file under DEEPZORA_HOME.
func defaultManagedKnownHosts() string {
	return config.RemoteKnownHostsPath()
}
