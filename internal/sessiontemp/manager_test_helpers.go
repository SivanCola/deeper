package sessiontemp

import filelock "deeper/internal/identitylock"

func tryLockForTest(path string) (func(), error) {
	return filelock.Acquire(nilContext(), path)
}
