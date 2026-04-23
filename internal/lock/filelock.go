package lock

import (
	"errors"
	"time"

	"github.com/gofrs/flock"
)

type FileLock struct {
	lock *flock.Flock
}

func New(path string) *FileLock {
	return &FileLock{lock: flock.New(path)}
}

func (l *FileLock) WithLock(fn func() error) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		locked, err := l.lock.TryLock()
		if err != nil {
			return err
		}
		if locked {
			defer func() { _ = l.lock.Unlock() }()
			return fn()
		}
		if time.Now().After(deadline) {
			return errors.New("failed to acquire lock - another instance may be running")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
