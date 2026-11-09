//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

                                                                         
                                                                             
func managedNodeUpdateRestart() bool {
	if os.Getenv("INVOCATION_ID") == "" {
		return false
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	unit := nodeServiceFromCgroup(string(cgroup))
	if unit == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", unit, "--property=MainPID", "--property=Restart", "--property=RestartPreventExitStatus", "--property=SuccessExitStatus")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output, err := cmd.Output()
	return err == nil && len(output) <= 8192 && nodeServiceRestartsForUpdate(string(output), os.Getpid())
}
