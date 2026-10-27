package bus

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// tempSock returns a short socket path under /tmp to stay within the 104-byte
// macOS limit for Unix socket paths.
func tempSock(t *testing.T, suffix string) string {
	t.Helper()
	path := fmt.Sprintf("/tmp/orch-test-%s.sock", suffix)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func startServer(t *testing.T, b *Bus, path string) chan struct{} {
	t.Helper()
	stop := make(chan struct{})
	if err := b.ServeUnix(path, stop); err != nil {
		t.Fatalf("ServeUnix: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // let Accept goroutine start
	return stop
}

func TestServeDialUnix_RoundTrip(t *testing.T) {
	b := New()
	sock := tempSock(t, "rt")
	stop := startServer(t, b, sock)
	defer close(stop)

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = client.Close() }()

	incoming := client.Subscribe()
	time.Sleep(10 * time.Millisecond) // let subscription be registered

	want := NewMessage(RoleQA, RoleCoder, MsgEvent, "hello")
	b.Publish(want)

	select {
	case got := <-incoming:
		if got.ID != want.ID {
			t.Errorf("ID mismatch: got %q, want %q", got.ID, want.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message from bus")
	}
}

func TestDialPublish_ForwardsToServerBus(t *testing.T) {
	b := New()
	sock := tempSock(t, "pub")
	stop := startServer(t, b, sock)
	defer close(stop)

	serverSub := b.Subscribe()

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = client.Close() }()

	time.Sleep(10 * time.Millisecond) // let connection establish

	want := NewMessage(RoleCoder, RoleQA, MsgEvent, "from-client")
	if err := client.Publish(want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-serverSub:
			if got.ID == want.ID {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for client message on server bus")
		}
	}
}

func TestHandleSocketConn_NoGoroutineLeak(t *testing.T) {
	b := New()
	sock := tempSock(t, "leak")
	stop := startServer(t, b, sock)
	defer close(stop)

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	_ = client.Close() // close immediately

	// Publish after close — writer goroutine must not block indefinitely.
	b.Publish(NewMessage(RoleSystem, "", MsgEvent, "post-close"))
	time.Sleep(50 * time.Millisecond) // give handler time to notice the close
}

func (b *Bus) subCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	other := b.Subscribe()
	b.Unsubscribe(ch)
	if n := b.subCount(); n != 1 {
		t.Fatalf("subs = %d, want 1", n)
	}
	b.Publish(NewMessage(RoleSystem, "", MsgEvent, "x"))
	select {
	case <-ch:
		t.Fatal("unsubscribed channel received message")
	default:
	}
	select {
	case <-other:
	default:
		t.Fatal("remaining subscriber missed message")
	}
	b.Unsubscribe(ch) // idempotent
}

func TestHandleSocketConn_UnsubscribesOnDisconnect(t *testing.T) {
	b := New()
	sock := tempSock(t, "unsub")
	stop := startServer(t, b, sock)
	defer close(stop)

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	waitFor(t, "subscribe", func() bool { return b.subCount() == 1 })
	_ = client.Close()
	waitFor(t, "unsubscribe", func() bool { return b.subCount() == 0 })
}

func TestHandleSocketConn_RejectsOversizedMessage(t *testing.T) {
	b := New()
	sock := tempSock(t, "big")
	stop := startServer(t, b, sock)
	defer close(stop)
	ch := b.Subscribe()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// One JSON value larger than the cap.
	payload := `{"content":"` + strings.Repeat("a", maxSocketMessageSize+1024) + `"}` + "\n"
	go func() { _, _ = conn.Write([]byte(payload)) }()

	// Server must drop the connection (read returns EOF/error) without publishing.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected connection to be closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection not closed for oversized message")
	}
	select {
	case m := <-ch:
		t.Fatalf("oversized message was published: %v", m.Type)
	default:
	}
}
