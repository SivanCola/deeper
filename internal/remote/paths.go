package remote

import "deeper/internal/config"

// defaultManagedKnownHosts is the Deeper-managed known_hosts path. It is a
// thin indirection over config so tests can leave HostKeyPolicy.ManagedPath
// empty and still get an isolated file under DEEPER_HOME.
func defaultManagedKnownHosts() string {
	return config.RemoteKnownHostsPath()
}
