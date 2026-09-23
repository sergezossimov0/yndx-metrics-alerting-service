package main

import (
	"os"
	"reflect"
	"testing"
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

func TestResolveConfig_UsesDefaultsWhenNeitherEnvNorFlagsSet(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{serverAddress: "localhost:8080", storeInterval: 300, fileStoragePath: "metrics-db.json", restore: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
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
	want := &config{
		serverAddress:   "192.168.1.1:9090",
		storeInterval:   60,
		fileStoragePath: "/data/env-metrics.json",
		restore:         false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_UsesFlagsWhenEnvNotSet(t *testing.T) {
	unsetAllServerEnv(t)
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120", "-f", "/data/flag-metrics.json", "-r=false"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{
		serverAddress:   "localhost:1234",
		storeInterval:   120,
		fileStoragePath: "/data/flag-metrics.json",
		restore:         false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_EmptyEnvVarsTreatedAsUnset(t *testing.T) {
	t.Setenv("ADDRESS", "")
	t.Setenv("STORE_INTERVAL", "")
	t.Setenv("FILE_STORAGE_PATH", "")
	t.Setenv("RESTORE", "")
	withArgs(t, []string{"-a", "localhost:1234", "-i", "120", "-f", "/data/flag-metrics.json", "-r=false"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{
		serverAddress:   "localhost:1234",
		storeInterval:   120,
		fileStoragePath: "/data/flag-metrics.json",
		restore:         false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_ZeroStoreIntervalEnvIsHonoredNotOverriddenByFlagDefault(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	t.Setenv("STORE_INTERVAL", "0")
	unsetEnv(t, "FILE_STORAGE_PATH")
	unsetEnv(t, "RESTORE")
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.storeInterval != 0 {
		t.Fatalf("expected STORE_INTERVAL=0 (synchronous writes) to be honored, got %d", got.storeInterval)
	}
}

func TestResolveConfig_FalseRestoreEnvIsHonoredNotOverriddenByFlagDefault(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "STORE_INTERVAL")
	unsetEnv(t, "FILE_STORAGE_PATH")
	t.Setenv("RESTORE", "false")
	withArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.restore != false {
		t.Fatalf("expected RESTORE=false to be honored (default flag is true), got %v", got.restore)
	}
}

func TestResolveConfig_InvalidStoreIntervalEnvReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	t.Setenv("STORE_INTERVAL", "not-a-number")
	unsetEnv(t, "FILE_STORAGE_PATH")
	unsetEnv(t, "RESTORE")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid STORE_INTERVAL, got nil")
	}
}

func TestResolveConfig_NegativeStoreIntervalEnvReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	t.Setenv("STORE_INTERVAL", "-1")
	unsetEnv(t, "FILE_STORAGE_PATH")
	unsetEnv(t, "RESTORE")
	withArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for negative STORE_INTERVAL, got nil")
	}
}

func TestResolveConfig_NegativeStoreIntervalFlagReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "STORE_INTERVAL")
	unsetEnv(t, "FILE_STORAGE_PATH")
	unsetEnv(t, "RESTORE")
	withArgs(t, []string{"-i", "-5"})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for negative -i flag, got nil")
	}
}

func TestResolveConfig_InvalidRestoreEnvReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "STORE_INTERVAL")
	unsetEnv(t, "FILE_STORAGE_PATH")
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

func TestResolveLogLevel_EmptyEnvVarTreatedAsUnset(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")

	got := resolveLogLevel()
	if got != "INFO" {
		t.Fatalf("expected %q, got %q", "INFO", got)
	}
}
