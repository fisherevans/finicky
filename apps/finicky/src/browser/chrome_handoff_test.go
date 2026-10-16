package browser

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mockChrome struct {
	root     string
	parent   string
	listener *net.UnixListener
}

func newMockChrome(t *testing.T) *mockChrome {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "finicky-handoff-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	parent := filepath.Join(root, "socket")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "SingletonSocket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: target, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	host, _ := os.Hostname()
	for path, value := range map[string]string{
		filepath.Join(root, "SingletonSocket"):   target,
		filepath.Join(root, "SingletonCookie"):   "test-cookie",
		filepath.Join(parent, "SingletonCookie"): "test-cookie",
		filepath.Join(root, "SingletonLock"):     host + "-" + strconv.Itoa(os.Getpid()),
	} {
		if err := os.Symlink(value, path); err != nil {
			t.Fatal(err)
		}
	}
	return &mockChrome{root, parent, listener}
}

func (m *mockChrome) serve(t *testing.T, reply string, delay time.Duration) <-chan string {
	t.Helper()
	received := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := m.listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		message, _ := io.ReadAll(conn)
		received <- string(message)
		time.Sleep(delay)
		for _, char := range []byte(reply) {
			conn.Write([]byte{char})
		}
	}()
	t.Cleanup(func() {
		m.listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("mock server did not stop")
		}
	})
	return received
}

func allowMockChrome(pid int, _ time.Duration) bool { return pid == os.Getpid() }

func TestChromeHandoffProtocol(t *testing.T) {
	for _, c := range []struct {
		name, reply, status string
		delay               time.Duration
	}{
		{"ack", "ACK", "acknowledged", 0},
		{"shutdown", "SHUTDOWN", "rejected", 0},
		{"unexpected", "WRONG", "unknown", 0},
		{"empty", "", "unknown", 0},
		{"timeout", "ACK", "unknown", 120 * time.Millisecond},
	} {
		t.Run(c.name, func(t *testing.T) {
			mock := newMockChrome(t)
			received := mock.serve(t, c.reply, c.delay)
			url := "https://example.com/path?token=TEST_SECRET"
			result := sendChromeHandoff(mock.root, "Profile 1", url, 75*time.Millisecond, allowMockChrome)
			if result.status != c.status {
				t.Fatalf("got %+v, want %s", result, c.status)
			}
			cwd, _ := os.Getwd()
			want := strings.Join([]string{"START", cwd, chromeExecutable, "--profile-directory=Profile 1", url}, "\x00")
			if got := <-received; got != want {
				t.Fatal("profile or URL changed")
			}
			if result.status == "unknown" && result.mayFallback() {
				t.Fatal("uncertain delivery must not retry")
			}
		})
	}
}

func TestChromeHandoffRejectsBeforeSending(t *testing.T) {
	for _, name := range []string{"cookie", "identity", "peer", "permissions", "race"} {
		t.Run(name, func(t *testing.T) {
			mock := newMockChrome(t)
			verify := allowMockChrome
			changeLink := func(path, value string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(value, path); err != nil {
					t.Fatal(err)
				}
			}
			connects := false
			switch name {
			case "cookie":
				changeLink(filepath.Join(mock.parent, "SingletonCookie"), "wrong")
			case "identity":
				connects = true
				verify = func(int, time.Duration) bool { return false }
			case "peer":
				connects = true
				host, _ := os.Hostname()
				changeLink(filepath.Join(mock.root, "SingletonLock"), host+"-99999999")
			case "permissions":
				os.Chmod(mock.parent, 0755)
			case "race":
				connects = true
				verify = func(int, time.Duration) bool {
					changeLink(filepath.Join(mock.parent, "SingletonCookie"), "changed")
					return true
				}
			}
			var received <-chan string
			if connects {
				received = mock.serve(t, "", 0)
			}
			result := sendChromeHandoff(mock.root, "Default", "https://example.com", time.Second, verify)
			if result.status != "not_sent" || !result.mayFallback() {
				t.Fatalf("got %+v", result)
			}
			if connects && <-received != "" {
				t.Fatal("message sent to unverified session")
			}
		})
	}
}

func TestChromeHandoffMissingSessionAndInvalidMessage(t *testing.T) {
	result := sendChromeHandoff("/no-such-finicky-test-profile", "Default", "https://example.com", time.Second, allowMockChrome)
	if result.status != "not_sent" || !result.mayFallback() {
		t.Fatalf("got %+v", result)
	}
	for _, c := range []struct{ profile, url string }{
		{"../Default", "https://example.com"}, {"Default", "x\x00--incognito"},
		{"Default", strings.Repeat("x", 32768)}, {"Default", " https://example.com"}, {"", "https://example.com"},
	} {
		result := sendChromeHandoff("/no-such-finicky-test-profile", c.profile, c.url, time.Second, allowMockChrome)
		if result.reason != "invalid_message" || result.status != "not_sent" {
			t.Fatalf("got %+v", result)
		}
	}
}

func TestChromeHandoffEligibility(t *testing.T) {
	t.Setenv("FINICKY_CHROME_HANDOFF", "1")
	for _, c := range []struct {
		name                     string
		args                     []string
		background, dryRun, want bool
	}{
		{"Google Chrome", nil, false, false, true}, {"com.google.Chrome", nil, false, false, true},
		{"Google Chrome", nil, true, false, false}, {"Google Chrome", nil, false, true, false},
		{"Google Chrome", []string{"--incognito"}, false, false, false}, {"Firefox", nil, false, false, false},
	} {
		if got := handoffEligible(BrowserConfig{Name: c.name, Args: c.args}, "--profile-directory=Default", c.background, c.dryRun); got != c.want {
			t.Fatalf("eligibility differs for %+v", c)
		}
	}
	if handoffEligible(BrowserConfig{Name: "Google Chrome"}, "", false, false) {
		t.Fatal("missing profile")
	}
	t.Setenv("FINICKY_CHROME_HANDOFF", "0")
	if handoffEligible(BrowserConfig{Name: "Google Chrome"}, "--profile-directory=Default", false, false) {
		t.Fatal("disable setting ignored")
	}
}

func TestChromeSignatureRejectsTestProcess(t *testing.T) {
	if verifyChromeSignature(os.Getpid(), time.Second) {
		t.Fatal("accepted a non-Chrome process")
	}
}
