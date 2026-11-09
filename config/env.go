package config

import (
	"path/filepath"
	"strings"
)

const (
	EnvVarName = "DEX_ENV"
	DefaultEnv = "dev"
)

                                                           
                                                                                                  
func ResolveConfigDir(env, explicitDir string) string {
	if dir := strings.TrimSpace(explicitDir); dir != "" {
		return filepath.Clean(dir)
	}
	env = strings.TrimSpace(env)
	if env == "" {
		env = DefaultEnv
	}
	return filepath.Join("config", "env", env)
}

func ConfigPath(dir, name string) string {
	if strings.TrimSpace(dir) == "" {
		return filepath.Join("config", name)
	}
	return filepath.Join(dir, name)
}
