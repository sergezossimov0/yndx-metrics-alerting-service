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

func TestResolveServerAddr_EnvVarTakesPrecedenceOverFlag(t *testing.T) {
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	withArgs(t, []string{"-a", "localhost:1234"})

	got, err := resolveServerAddr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "192.168.1.1:9090" {
		t.Fatalf("expected %q, got %q", "192.168.1.1:9090", got)
	}
}

func TestResolveServerAddr_UsesFlagWhenEnvNotSet(t *testing.T) {
	orig, wasSet := os.LookupEnv("ADDRESS")
	os.Unsetenv("ADDRESS")
	t.Cleanup(func() {
		if wasSet {
			os.Setenv("ADDRESS", orig)
		}
	})
	withArgs(t, []string{"-a", "localhost:1234"})

	got, err := resolveServerAddr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "localhost:1234" {
		t.Fatalf("expected %q, got %q", "localhost:1234", got)
	}
}

func TestResolveServerAddr_UsesDefaultWhenNeitherEnvNorFlagSet(t *testing.T) {
	orig, wasSet := os.LookupEnv("ADDRESS")
	os.Unsetenv("ADDRESS")
	t.Cleanup(func() {
		if wasSet {
			os.Setenv("ADDRESS", orig)
		}
	})
	withArgs(t, []string{})

	got, err := resolveServerAddr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "localhost:8080" {
		t.Fatalf("expected %q, got %q", "localhost:8080", got)
	}
}

func TestResolveServerAddr_EmptyEnvVarTreatedAsUnset(t *testing.T) {
	t.Setenv("ADDRESS", "")
	withArgs(t, []string{"-a", "localhost:1234"})

	got, err := resolveServerAddr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "localhost:1234" {
		t.Fatalf("expected %q, got %q", "localhost:1234", got)
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
	orig, wasSet := os.LookupEnv("LOG_LEVEL")
	os.Unsetenv("LOG_LEVEL")
	t.Cleanup(func() {
		if wasSet {
			os.Setenv("LOG_LEVEL", orig)
		}
	})

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
