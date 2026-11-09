//go:build windows

package diskspace

import "golang.org/x/sys/windows"

func platformUsage(path string) (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var totalFree uint64
	err = windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree)
	return total, free, err
}
