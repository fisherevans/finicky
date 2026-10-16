package browser

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

static int chromePeerPID(int fd) {
    uid_t uid; gid_t gid;
    pid_t pid = 0;
    socklen_t length = sizeof(pid);
    if (getpeereid(fd, &uid, &gid) || uid != geteuid()) return -1;
    if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERPID, &pid, &length)) return -1;
    return pid > 0 ? pid : -1;
}

static int validChromeSignature(int pid) {
    CFNumberRef number = CFNumberCreate(NULL, kCFNumberIntType, &pid);
    const void *keys[] = { kSecGuestAttributePid };
    const void *values[] = { number };
    CFDictionaryRef attributes = CFDictionaryCreate(NULL, keys, values, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    SecCodeRef code = NULL;
    SecRequirementRef requirement = NULL;
    OSStatus status = SecCodeCopyGuestWithAttributes(NULL, attributes, kSecCSDefaultFlags, &code);
    if (status == errSecSuccess) {
        status = SecRequirementCreateWithString(CFSTR(
            "identifier \"com.google.Chrome\" and anchor apple generic and "
            "certificate 1[field.1.2.840.113635.100.6.2.6] exists and "
            "certificate leaf[field.1.2.840.113635.100.6.1.13] exists and "
            "certificate leaf[subject.OU] = \"EQHXZ8M8AV\""),
            kSecCSDefaultFlags, &requirement);
    }
    if (status == errSecSuccess) status = SecCodeCheckValidity(code, kSecCSDefaultFlags, requirement);
    if (requirement) CFRelease(requirement);
    if (code) CFRelease(code);
    CFRelease(attributes);
    CFRelease(number);
    return status == errSecSuccess;
}
*/
import "C"

import (
	"net"
	"time"
)

// At most one signature check can remain in native code after its caller times out.
var chromeSignatureSlot = make(chan struct{}, 1)

func chromePeer(conn *net.UnixConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1
	}
	pid := -1
	if raw.Control(func(fd uintptr) { pid = int(C.chromePeerPID(C.int(fd))) }) != nil {
		return -1
	}
	return pid
}

func verifyChromeSignature(pid int, timeout time.Duration) bool {
	select {
	case chromeSignatureSlot <- struct{}{}:
	default:
		return false
	}
	result := make(chan bool, 1)
	go func() {
		defer func() { <-chromeSignatureSlot }()
		result <- C.validChromeSignature(C.int(pid)) != 0
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case valid := <-result:
		return valid
	case <-timer.C:
		return false
	}
}
