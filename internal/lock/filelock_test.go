package lock

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWithLockTimesOutWhenHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	lock1 := New(path)
	lock2 := New(path)

	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = lock1.WithLock(func() error {
			<-release
			return nil
		})
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	err := lock2.WithLock(func() error { return nil })
	elapsed := time.Since(start)
	close(release)
	<-done

	if err == nil {
		t.Fatal("expected timeout lock error")
	}
	// Assert it did retry (not fail fast).
	if elapsed < 9*time.Second {
		t.Fatalf("expected lock wait close to timeout, got %s", elapsed)
	}
}

