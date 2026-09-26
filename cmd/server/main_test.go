package main

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeHelpArg_ReplacesLongHelpFlagWithShort(t *testing.T) {
	got := normalizeHelpArg([]string{"-a", "localhost:8080", "--help"})
	want := []string{"-a", "localhost:8080", "-h"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNormalizeHelpArg_LeavesOtherArgsUnchanged(t *testing.T) {
	args := []string{"-a", "localhost:8080"}
	got := normalizeHelpArg(args)

	if !reflect.DeepEqual(got, args) {
		t.Fatalf("expected %v, got %v", args, got)
	}
}

func TestNormalizeHelpArg_EmptyArgsReturnsEmptySlice(t *testing.T) {
	got := normalizeHelpArg([]string{})

	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func withArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = append([]string{"server"}, args...)
	t.Cleanup(func() { os.Args = orig })
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	orig, wasSet := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if wasSet {
			os.Setenv(key, orig)
		}
	})
}

func unsetAllServerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ADDRESS", "STORE_INTERVAL", "FILE_STORAGE_PATH", "RESTORE"} {
		unsetEnv(t, key)
	}
}

// resolvedConfig holds only the resolved values, so expectations do not
// depend on the internal isSet markers of intervalValid and restoreValid.
type resolvedConfig struct {
	serverAddress   string
	storeInterval   time.Duration
	fileStoragePath string
	restore         bool
}

func resolved(c *config) resolvedConfig {
	return resolvedConfig{
		serverAddress:   c.serverAddress,
		storeInterval:   c.storeInterval.interval,
		fileStoragePath: c.fileStoragePath,
		restore:         c.restore.isRestore,
	}
}

func TestResolveConfig_UsesDefaultsWhenNeitherEnvNorFlagsSet(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{serverAddress: "localhost:8080", storeInterval: 300 * time.Second, fileStoragePath: "metrics-db.json", restore: true}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_EnvVarsTakePrecedenceOverFlags(t *testing.T) {
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	t.Setenv("STORE_INTERVAL", "60")
	t.Setenv("FILE_STORAGE_PATH", "/data/env-metrics.json")
	t.Setenv("RESTORE", "false")
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120", "-f", "/data/flag-metrics.json", "-r=true"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{
		serverAddress:   "192.168.1.1:9090",
		storeInterval:   60 * time.Second,
		fileStoragePath: "/data/env-metrics.json",
		restore:         false,
	}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_UsesFlagsWhenEnvNotSet(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120", "-f", "/data/flag-metrics.json", "-r=false"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{
		serverAddress:   "localhost:1234",
		storeInterval:   120 * time.Second,
		fileStoragePath: "/data/flag-metrics.json",
		restore:         false,
	}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_EmptyStringEnvVarsFallBackToFlags(t *testing.T) {
	// documents current behavior: declared but empty ADDRESS and FILE_STORAGE_PATH
	// are replaced by flag values
	unsetAllServerEnv(t)
	t.Setenv("ADDRESS", "")
	t.Setenv("FILE_STORAGE_PATH", "")
	withArgs(t, []string{"-a", "localhost:1234", "-f", "/data/flag-metrics.json"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.serverAddress != "localhost:1234" {
		t.Fatalf("expected serverAddress to fall back to flag value, got %q", got.serverAddress)
	}
	if got.fileStoragePath != "/data/flag-metrics.json" {
		t.Fatalf("expected fileStoragePath to fall back to flag value, got %q", got.fileStoragePath)
	}
}

func TestResolveConfig_EmptyParsedEnvVarsReturnError(t *testing.T) {
	for _, key := range []string{"STORE_INTERVAL", "RESTORE"} {
		t.Run(key, func(t *testing.T) {
			unsetAllServerEnv(t)
			t.Setenv(key, "")
			withArgs(t, []string{})

			if _, err := resolveConfig(); err == nil {
				t.Fatalf("expected error for empty %s, got nil", key)
			}
		})
	}
}

func TestResolveConfig_ZeroStoreIntervalEnvIsHonoredNotOverriddenByFlagDefault(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("STORE_INTERVAL", "0")
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.storeInterval.interval != 0 {
		t.Fatalf("expected STORE_INTERVAL=0 (synchronous writes) to be honored, got %v", got.storeInterval.interval)
	}
}

func TestResolveConfig_ZeroStoreIntervalFlagIsHonored(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{"-i", "0"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.storeInterval.interval != 0 {
		t.Fatalf("expected -i 0 (synchronous writes) to be honored, got %v", got.storeInterval.interval)
	}
}

func TestResolveConfig_FalseRestoreEnvIsHonoredNotOverriddenByFlagDefault(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("RESTORE", "false")
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.restore.isRestore {
		t.Fatalf("expected RESTORE=false to be honored (default flag is true), got %v", got.restore.isRestore)
	}
}

func TestResolveConfig_InvalidStoreIntervalEnvReturnsError(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("STORE_INTERVAL", "not-a-number")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid STORE_INTERVAL, got nil")
	}
}

func TestResolveConfig_NegativeStoreIntervalEnvReturnsError(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("STORE_INTERVAL", "-1")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for negative STORE_INTERVAL, got nil")
	}
}

func TestResolveConfig_NegativeStoreIntervalFlagReturnsError(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{"-i", "-5"})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for negative -i flag, got nil")
	}
}

func TestResolveConfig_InvalidRestoreEnvReturnsError(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("RESTORE", "not-a-bool")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid RESTORE, got nil")
	}
}

func TestResolveLogLevel_UsesEnvVarWhenSet(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")

	got := resolveLogLevel()
	if got != "debug" {
		t.Fatalf("expected %q, got %q", "debug", got)
	}
}

func TestResolveLogLevel_UsesDefaultWhenEnvNotSet(t *testing.T) {
	unsetEnv(t, "LOG_LEVEL")

	got := resolveLogLevel()
	if got != "INFO" {
		t.Fatalf("expected %q, got %q", "INFO", got)
	}
}

func TestResolveLogLevel_EmptyEnvVarIsPassedThrough(t *testing.T) {
	// a declared but empty LOG_LEVEL is not replaced by the default;
	// zap parses "" as the info level
	t.Setenv("LOG_LEVEL", "")

	got := resolveLogLevel()
	if got != "" {
		t.Fatalf("expected %q, got %q", "", got)
	}
}
