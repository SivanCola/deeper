package desktopinstance

import (
	"path/filepath"
	"strings"
)

// Stable launchers coordinate an installation but do not own an Electron
// profile or publish shell status. The caller separately verifies PE identity.
func stableLauncherImage(root, image string) bool {
	return strings.EqualFold(image, filepath.Join(root, "Deepzora.exe")) ||
		strings.EqualFold(image, filepath.Join(root, "deepzora-launcher.exe"))
}

type shellObservation struct {
	pid, parent uint32
	image       string
	created     int64
	alive       bool
	status      *Status // Already decoded and bound to this PID by readStatus.
}

// shellOwners associates statusless Chromium children only with a verified
// current shell. Same-image, creation-time and liveness fences reject unrelated
// roots, stale/reused parent PIDs and unrooted process trees.
func shellOwners(observations []shellObservation) map[uint32]uint32 {
	byPID := make(map[uint32]shellObservation, len(observations))
	owners := make(map[uint32]uint32, len(observations))
	for _, p := range observations {
		byPID[p.pid] = p
		if p.alive && p.status != nil {
			owners[p.pid] = p.pid
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range observations {
			if _, known := owners[p.pid]; known || !p.alive {
				continue
			}
			owner, known := owners[p.parent]
			parent := byPID[p.parent]
			if !known || !parent.alive || parent.created > p.created || !strings.EqualFold(parent.image, p.image) {
				continue
			}
			owners[p.pid] = owner
			changed = true
		}
	}
	return owners
}
