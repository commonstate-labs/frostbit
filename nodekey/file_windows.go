//go:build windows

package nodekey

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openSafe(path string, flags int, perm os.FileMode) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Windows 的目录 ACL 由操作者管理；拒绝现有路径中的 reparse points。
	for current := abs; ; current = filepath.Dir(current) {
		p, e := windows.UTF16PtrFromString(current)
		if e != nil {
			return nil, e
		}
		attrs, e := windows.GetFileAttributes(p)
		if e == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, errors.New("key path must not contain reparse points")
		}
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if flags&os.O_CREATE != 0 {
		return os.OpenFile(abs, flags, perm)
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("key file must not be a reparse point")
	}
	return os.NewFile(uintptr(h), abs), nil
}

// Unix mode bits 不代表 Windows ACL；密文文件继承目标目录 ACL。
func checkFilePermissions(os.FileMode, bool) error { return nil }
