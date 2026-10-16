package main

import (
	"os"
	"reflect"
	"testing"
	"time"
)

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
	for _, key := range []string{"ADDRESS", "STORE_INTERVAL", "FILE_STORAGE_PATH", "RESTORE", "DATABASE_DSN"} {
		unsetEnv(t, key)
	}
}

// resolvedConfig flattens config into comparable plain values.
type resolvedConfig struct {
	serverAddress   string
	storeInterval   time.Duration
	fileStoragePath string
	restore         bool
	dbConnection    string
}

func resolved(c *serverConfig) resolvedConfig {
	return resolvedConfig{
		serverAddress:   c.serverAddress,
		storeInterval:   c.storeInterval.Duration,
		fileStoragePath: c.fileStoragePath,
		restore:         c.restore,
		dbConnection:    c.dbConnection,
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
	t.Setenv("DATABASE_DSN", "env-dsn")
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120", "-f", "/data/flag-metrics.json", "-r=true", "-d", "flag-dsn"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{
		serverAddress:   "192.168.1.1:9090",
		storeInterval:   60 * time.Second,
		fileStoragePath: "/data/env-metrics.json",
		restore:         false,
		dbConnection:    "env-dsn",
	}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_PartialEnvOverridesOnlyItsOwnFlags(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{
		serverAddress:   "192.168.1.1:9090",
		storeInterval:   120 * time.Second,
		fileStoragePath: "metrics-db.json",
		restore:         true,
	}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_FlagsAreParsedEvenWhenAllEnvSet(t *testing.T) {
	// previously a fully populated env skipped flag parsing, so a bad flag went unnoticed
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	t.Setenv("STORE_INTERVAL", "60")
	t.Setenv("FILE_STORAGE_PATH", "/data/env-metrics.json")
	t.Setenv("RESTORE", "false")
	t.Setenv("DATABASE_DSN", "env-dsn")
	withArgs(t, []string{"-unknown"})

	if _, err := resolveConfig(); err == nil {
		t.Fatal("expected error for unknown flag, got nil")
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
	// declared but empty ADDRESS and FILE_STORAGE_PATH count as unset
	// and keep the flag values
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
	if got.storeInterval.Duration != 0 {
		t.Fatalf("expected STORE_INTERVAL=0 (synchronous writes) to be honored, got %v", got.storeInterval.Duration)
	}
}

func TestResolveConfig_ZeroStoreIntervalFlagIsHonored(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{"-i", "0"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.storeInterval.Duration != 0 {
		t.Fatalf("expected -i 0 (synchronous writes) to be honored, got %v", got.storeInterval.Duration)
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
	if got.restore {
		t.Fatalf("expected RESTORE=false to be honored (default flag is true), got %v", got.restore)
	}
}

// Interval parsing rules are tested in internal/config; here we only check
// that a rejected value surfaces from each source.
func TestResolveConfig_InvalidStoreIntervalEnvReturnsError(t *testing.T) {
	unsetAllServerEnv(t)
	t.Setenv("STORE_INTERVAL", "-1")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid STORE_INTERVAL, got nil")
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
