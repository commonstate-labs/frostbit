//go:build !linux

package updater

import (
	"os"
)

func bootstrapProcessDependencies() Dependencies {
	return Dependencies{
		Supported:     func() bool { return true },
		EUID:          func() int { return 1000 },
		ValidatePaths: func(...string) error { return nil },
		AcquireLock:   func(string) (func(), error) { return func() {}, nil },
		Exec: func(path string, args, env []string) error {
			                                                              
			                                                           
			process, err := os.StartProcess(path, args, &os.ProcAttr{Env: env, Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}})
			if err != nil {
				return err
			}
			state, err := process.Wait()
			if err == nil {
				os.Exit(state.ExitCode())
			}
			return err
		},
	}
}
