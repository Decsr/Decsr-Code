package automation

import (
	"os"
	"testing"
	"time"
)

func testLocker(t *testing.T, now time.Time, alive bool) Locker {
	t.Helper()
	return Locker{
		Root: t.TempDir(), Timeout: time.Hour, PID: 4242,
		Now: func() time.Time { return now }, Alive: func(int) bool { return alive },
	}
}

func TestLockAcquireAndActive(t *testing.T) {
	l := testLocker(t, time.Now(), true)
	if l.Active("a") {
		t.Fatal("active before acquire")
	}
	release, ok, err := l.Acquire("a")
	if err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if !l.Active("a") {
		t.Error("not active while held")
	}
	if _, ok, err := l.Acquire("a"); err != nil || ok {
		t.Errorf("second acquire = %v, %v; want refused", ok, err)
	}
	if _, ok, _ := l.Acquire("b"); !ok {
		t.Error("another name must not conflict")
	}
	release()
	if l.Active("a") {
		t.Error("active after release")
	}
	if _, err := os.Stat(LockPath(l.Root, "a")); !os.IsNotExist(err) {
		t.Errorf("lock file left: %v", err)
	}
}

func TestLockStaleByDeadPID(t *testing.T) {
	l := testLocker(t, time.Now(), true)
	if _, ok, _ := l.Acquire("a"); !ok {
		t.Fatal("acquire")
	}
	l.Alive = func(int) bool { return false }
	if l.Active("a") {
		t.Error("a dead pid still counts as active")
	}
	l.PID = 7
	if _, ok, err := l.Acquire("a"); !ok || err != nil {
		t.Errorf("stale lock not replaced: %v %v", ok, err)
	}
}

func TestLockStaleByAge(t *testing.T) {
	now := time.Now()
	l := testLocker(t, now, true)
	if _, ok, _ := l.Acquire("a"); !ok {
		t.Fatal("acquire")
	}
	l.Now = func() time.Time { return now.Add(l.Timeout + StaleGrace - time.Minute) }
	if !l.Active("a") {
		t.Error("lock inside timeout+10m must stay active")
	}
	l.Now = func() time.Time { return now.Add(l.Timeout + StaleGrace + time.Minute) }
	if l.Active("a") {
		t.Error("lock older than timeout+10m must be stale")
	}
	if _, ok, _ := l.Acquire("a"); !ok {
		t.Error("stale lock not replaced")
	}
}

func TestLockGarbageFileIsStale(t *testing.T) {
	l := testLocker(t, time.Now(), true)
	if err := os.MkdirAll(Dir(l.Root), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(l.Root, "a"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l.Active("a") {
		t.Error("garbage lock is active")
	}
	if _, ok, _ := l.Acquire("a"); !ok {
		t.Error("garbage lock not replaced")
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Error("own pid is not alive")
	}
}
