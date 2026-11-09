//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd

package diskspace

import (
	"fmt"
	"runtime"
)

func platformUsage(string) (total, free uint64, err error) {
	return 0, 0, fmt.Errorf("unsupported operating system %s", runtime.GOOS)
}
