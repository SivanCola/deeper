package sessiontemp

import filelock "deepzora/internal/identitylock"

func tryLockForTest(path string) (func(), error) {
	return filelock.Acquire(nilContext(), path)
}
