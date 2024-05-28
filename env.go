package cpuproc

type EnvKeyType string

// EnvKey is a context key that can be used to set programmatically the environment
// gopsutil relies on to perform calls against the OS.
// Example of use:
//
//	ctx := context.WithValue(context.Background(), common.EnvKey, EnvMap{common.HostProcEnvKey: "/myproc"})
//	avg, err := load.AvgWithContext(ctx)
var EnvKey = EnvKeyType("env")

type EnvMap map[EnvKeyType]string
