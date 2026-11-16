package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// StaleGrace is added to the timeout before a lock of a still living process
// counts as stale.
const StaleGrace = 10 * time.Minute

type lockInfo struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// Locker takes the per-automation lock. Alive and Now are replaced in tests.
type Locker struct {
	Root    string
	Timeout time.Duration
	PID     int
	Now     func() time.Time
	Alive   func(pid int) bool
}

// NewLocker is a Locker for the real process and clock.
func NewLocker(root string, timeout time.Duration) Locker {
	return Locker{Root: root, Timeout: timeout, PID: os.Getpid(), Now: time.Now, Alive: ProcessAlive}
}

// stale reports whether the lock file at path no longer guards a live run.
func (l Locker) stale(path string) (info lockInfo, stale bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- path built from the project root and a validated name
	if err != nil {
		return info, errors.Is(err, os.ErrNotExist)
	}
	if json.Unmarshal(data, &info) != nil || info.PID <= 0 {
		return info, true
	}
	if l.Now().Sub(info.Started) > l.Timeout+StaleGrace {
		return info, true
	}
	return info, !l.Alive(info.PID)
}

// Active reports whether a live run holds the lock of name.
func (l Locker) Active(name string) bool {
	path := LockPath(l.Root, name)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	_, stale := l.stale(path)
	return !stale
}

// Acquire takes the lock. ok is false when a live run holds it. The returned
// release removes the lock.
func (l Locker) Acquire(name string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(Dir(l.Root), 0o750); err != nil {
		return nil, false, err
	}
	path := LockPath(l.Root, name)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- see above
		if err == nil {
			data, _ := json.Marshal(lockInfo{PID: l.PID, Started: l.Now()})
			_, werr := f.Write(data)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, false, fmt.Errorf("write lock: %w", errors.Join(werr, cerr))
			}
			return func() { _ = os.Remove(path) }, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		if _, stale := l.stale(path); !stale {
			return nil, false, nil
		}
		_ = os.Remove(path)
	}
	return nil, false, nil
}
