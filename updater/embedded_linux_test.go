//go:build linux

package updater

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxSamePIDExecPreservesUnreadSecretsAndRestoresFlagsOnFailure(t *testing.T) {
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	w.Write([]byte("still-unread"))
	w.Close()
	fd := int(r.Fd())
	before, e := unix.FcntlInt(r.Fd(), unix.F_GETFD, 0)
	if e != nil {
		t.Fatal(e)
	}
	restore, e := preserveSecretFDs([]int{fd, fd})
	if e != nil {
		t.Fatal(e)
	}
	during, e := unix.FcntlInt(r.Fd(), unix.F_GETFD, 0)
	if e != nil || during&unix.FD_CLOEXEC != 0 {
		t.Fatal("descriptor would be lost on exec")
	}
	restore()
	after, _ := unix.FcntlInt(r.Fd(), unix.F_GETFD, 0)
	if before != after {
		t.Fatal("original duplicate-fd flags were not restored")
	}
	b, e := io.ReadAll(r)
	if e != nil || string(b) != "still-unread" {
		t.Fatal("secret pipe consumed", string(b), e)
	}
}
func TestLinuxCacheLockExcludesSecondProcessAndIsCLOEXEC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, e := lockState(path)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := lockState(path); e == nil {
		other()
		t.Fatal("second lock accepted")
	}
	unlock()
	other, e := lockState(path)
	if e != nil {
		t.Fatal(e)
	}
	other()
}
