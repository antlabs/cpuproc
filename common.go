package cpuproc

import (
	"context"
	"os"
	"path/filepath"
)

func HostProcWithContext(ctx context.Context, combineWith ...string) string {
	return GetEnvWithContext(ctx, "HOST_PROC", "/proc", combineWith...)
}

func HostEtcWithContext(ctx context.Context, combineWith ...string) string {
	return GetEnvWithContext(ctx, "HOST_ETC", "/etc", combineWith...)
}
func HostRootWithContext(ctx context.Context, combineWith ...string) string {
	return GetEnvWithContext(ctx, "HOST_ROOT", "/", combineWith...)
}

// GetEnvWithContext retrieves the environment variable key. If it does not exist it returns the default.
// The context may optionally contain a map superseding os.EnvKey.
func GetEnvWithContext(ctx context.Context, key string, dfault string, combineWith ...string) string {
	var value string
	if env, ok := ctx.Value(EnvKey).(EnvMap); ok {
		value = env[EnvKeyType(key)]
	}
	if value == "" {
		value = os.Getenv(key)
	}
	if value == "" {
		value = dfault
	}

	return combine(value, combineWith)
}

func combine(value string, combineWith []string) string {
	switch len(combineWith) {
	case 0:
		return value
	case 1:
		return filepath.Join(value, combineWith[0])
	default:
		all := make([]string, len(combineWith)+1)
		all[0] = value
		copy(all[1:], combineWith)
		return filepath.Join(all...)
	}
}

// ReadFile reads contents from a file
func ReadFile(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
