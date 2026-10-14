//go:build windows

package desktopinstance

func requireShellOwners(root string, list []*process) error {
	var observations []shellObservation
	for _, p := range list {
		if ImageRole(root, p.image) == "shell" {
			observations = append(observations, shellObservation{p.pid, p.parent, p.image, p.created.Nanoseconds(), p.alive(), p.status})
		}
	}
	owners := shellOwners(observations)
	for _, p := range list {
		if p.status == nil && ImageRole(root, p.image) == "shell" && p.alive() && owners[p.pid] == 0 {
			return outcome(UnknownOwner, "current shell status is required for PID %d", p.pid)
		}
	}
	return nil
}

func selectProfileProcesses(list []*process, homeKey string, all bool) []*process {
	if all {
		return list
	}
	selected := list[:0]
	for _, p := range list {
		if p.status != nil && p.status.HomeKey == homeKey {
			selected = append(selected, p)
		} else {
			p.close()
		}
	}
	return selected
}
