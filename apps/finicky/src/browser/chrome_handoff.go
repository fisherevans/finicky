package browser

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"finicky/timing"
	"finicky/util"
)

const chromeExecutable = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

type handoffResult struct {
	status string
	reason string
}

func (r handoffResult) mayFallback() bool { return r.status == "not_sent" || r.status == "rejected" }

func handoffEligible(config BrowserConfig, profile string, background, dryRun bool) bool {
	return !dryRun && !background && len(config.Args) == 0 &&
		(config.Name == "Google Chrome" || config.Name == "com.google.Chrome") &&
		strings.HasPrefix(profile, "--profile-directory=") && os.Getenv("FINICKY_CHROME_HANDOFF") != "0"
}

// Chromium's internal singleton protocol forwards the profile and URL to running Chrome.
// A write without an acknowledgement has an unknown outcome and must never be retried.
func tryChromeHandoff(profile, url string, trace *timing.Trace) handoffResult {
	start := time.Now()
	home, err := util.UserHomeDir()
	result := handoffResult{"not_sent", "home_unavailable"}
	if err == nil {
		result = sendChromeHandoff(filepath.Join(home, "Library/Application Support/Google/Chrome"),
			strings.TrimPrefix(profile, "--profile-directory="), url, 2*time.Second, verifyChromeSignature)
	}
	trace.Stage("chrome_handoff", start, result.status == "unknown")
	trace.Record(timing.Event{Kind: "chrome_handoff_" + result.status})
	if result.mayFallback() {
		trace.Record(timing.Event{Kind: "chrome_handoff_fallback_" + result.reason})
	}
	return result
}

func ownedChromePath(path string, kind os.FileMode) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeType != kind {
		return nil, errors.New("unexpected owner or type")
	}
	return info, nil
}

func chromeLink(path string) (string, error) {
	if _, err := ownedChromePath(path, os.ModeSymlink); err != nil {
		return "", err
	}
	value, err := os.Readlink(path)
	if value == "" && err == nil {
		err = errors.New("empty link")
	}
	return value, err
}

func sendChromeHandoff(root, profile, url string, timeout time.Duration, verify func(int, time.Duration) bool) handoffResult {
	notSent := func(reason string) handoffResult { return handoffResult{"not_sent", reason} }
	if profile == "" || profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return notSent("invalid_message")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return notSent("cwd_unavailable")
	}
	parts := []string{"START", cwd, chromeExecutable, "--profile-directory=" + profile, url}
	for _, part := range parts {
		if !utf8.ValidString(part) || strings.ContainsRune(part, 0) || part != strings.TrimSpace(part) {
			return notSent("invalid_message")
		}
	}
	message := strings.Join(parts, "\x00")
	if url == "" || len(message) >= 32768 {
		return notSent("invalid_message")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return notSent("session_unavailable")
	}
	info, err := ownedChromePath(root, os.ModeDir)
	if err != nil || info.Mode().Perm()&0022 != 0 {
		return notSent("unsafe_directory")
	}
	target, err := chromeLink(filepath.Join(root, "SingletonSocket"))
	if err != nil {
		return notSent("session_unavailable")
	}
	if !filepath.IsAbs(target) || len(target) >= 104 {
		return notSent("unsupported_socket")
	}
	parent := filepath.Dir(target)
	info, err = ownedChromePath(parent, os.ModeDir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return notSent("unsafe_directory")
	}
	if _, err = ownedChromePath(target, os.ModeSocket); err != nil {
		return notSent("session_unavailable")
	}
	cookie, err := chromeLink(filepath.Join(root, "SingletonCookie"))
	if err != nil {
		return notSent("session_unavailable")
	}
	lock, err := chromeLink(filepath.Join(root, "SingletonLock"))
	if err != nil {
		return notSent("session_unavailable")
	}
	separator := strings.LastIndex(lock, "-")
	hostname, hostErr := os.Hostname()
	if separator < 1 || hostErr != nil || lock[:separator] != hostname {
		return notSent("invalid_lock")
	}
	pid, err := strconv.Atoi(lock[separator+1:])
	if err != nil || pid <= 0 {
		return notSent("invalid_lock")
	}
	checkSession := func() bool {
		for path, expected := range map[string]string{
			filepath.Join(root, "SingletonCookie"):   cookie,
			filepath.Join(parent, "SingletonCookie"): cookie,
			filepath.Join(root, "SingletonSocket"):   target,
			filepath.Join(root, "SingletonLock"):     lock,
		} {
			actual, err := chromeLink(path)
			if err != nil || actual != expected {
				return false
			}
		}
		return true
	}
	if !checkSession() {
		return notSent("session_changed")
	}
	deadline := time.Now().Add(timeout)
	connRaw, err := net.DialTimeout("unix", target, timeout)
	if err != nil {
		return notSent("connect_failed")
	}
	conn := connRaw.(*net.UnixConn)
	defer conn.Close()
	if chromePeer(conn) != pid {
		return notSent("peer_mismatch")
	}
	if !verify(pid, time.Until(deadline)) {
		return notSent("identity_unverified")
	}
	if !checkSession() {
		return notSent("session_changed")
	}
	if time.Until(deadline) <= 0 {
		return notSent("deadline_expired")
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return notSent("deadline_failed")
	}
	// Any write attempt can leave a partial message. Do not fall back afterward.
	n, err := io.WriteString(conn, message)
	if err != nil || n != len(message) {
		return handoffResult{"unknown", "write_failed"}
	}
	if conn.CloseWrite() != nil {
		return handoffResult{"unknown", "shutdown_failed"}
	}
	reply, err := io.ReadAll(io.LimitReader(conn, 9))
	if err != nil {
		return handoffResult{"unknown", "ack_failed"}
	}
	switch string(reply) {
	case "ACK":
		return handoffResult{"acknowledged", ""}
	case "SHUTDOWN":
		return handoffResult{"rejected", "browser_shutdown"}
	default:
		return handoffResult{"unknown", "unexpected_reply"}
	}
}
