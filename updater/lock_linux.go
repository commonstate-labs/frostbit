//go:build linux

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func lockState(path string) (func(), error) {
	fd, e := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("another updater holds cache lock: %w", e)
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}
func syncDirectory(f *os.File) error { return f.Sync() }
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		e := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if e == unix.ESRCH {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.WaitDelay = 5 * time.Second
}

func validateOwnedPaths(paths ...string) error {
	uid := uint32(os.Geteuid())
	for _, p := range paths {
		for {
			info, e := os.Lstat(p)
			if e != nil {
				return e
			}
			s, ok := info.Sys().(*syscall.Stat_t)
			if !ok || s.Uid != 0 && s.Uid != uid || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
				return fmt.Errorf("updater path must be owned by root/node and not writable by others: %s", p)
			}
			parent := filepath.Dir(p)
			if parent == p {
				break
			}
			p = parent
		}
	}
	return nil
}
func cacheOwnedByProcess(path string) error {
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("cache must be owned exclusively by the node user")
	}
	return nil
}
func platformExec(path string, args, env []string) error {
	if os.Geteuid() == 0 {
		return ErrUnsupported
	}
	return syscall.Exec(path, args, env)
}
func preserveSecretFDs(fds []int) (func(), error) {
	flags := map[int]int{}
	restore := func() {
		for fd, v := range flags {
			unix.FcntlInt(uintptr(fd), unix.F_SETFD, v)
		}
	}
	for _, fd := range fds {
		if fd < 3 {
			restore()
			return nil, fmt.Errorf("secret descriptor must be >=3")
		}
		if _, ok := flags[fd]; ok {
			continue
		}
		v, e := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if e != nil {
			restore()
			return nil, fmt.Errorf("declared secret descriptor is unavailable: %w", e)
		}
		flags[fd] = v
		if _, e = unix.FcntlInt(uintptr(fd), unix.F_SETFD, v&^unix.FD_CLOEXEC); e != nil {
			restore()
			return nil, e
		}
	}
	return restore, nil
}
