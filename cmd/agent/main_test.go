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
	args := []string{"-a", "localhost:8080", "-r", "10"}
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

func TestNormalizeServerAddr_PrependsHTTPSchemeToBareAddress(t *testing.T) {
	got := normalizeServerAddr("localhost:8080")
	if got != "http://localhost:8080" {
		t.Fatalf("expected http://localhost:8080, got %s", got)
	}
}

func TestNormalizeServerAddr_LeavesHTTPAddressUnchanged(t *testing.T) {
	got := normalizeServerAddr("http://localhost:8080")
	if got != "http://localhost:8080" {
		t.Fatalf("expected unchanged address, got %s", got)
	}
}

func TestNormalizeServerAddr_LeavesHTTPSAddressUnchanged(t *testing.T) {
	got := normalizeServerAddr("https://localhost:8080")
	if got != "https://localhost:8080" {
		t.Fatalf("expected unchanged address, got %s", got)
	}
}

func withAgentArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = append([]string{"agent"}, args...)
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

func TestResolveConfig_AllEnvVarsTakePrecedenceOverFlags(t *testing.T) {
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	t.Setenv("REPORT_INTERVAL", "20")
	t.Setenv("POLL_INTERVAL", "5")
	// an invalid flag would fail parsing, proving env short-circuits flag parsing entirely
	withAgentArgs(t, []string{"-unknown-flag"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{serverAddress: "192.168.1.1:9090", reportInterval: 20, pollInterval: 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_UsesFlagsWhenEnvNotSet(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "REPORT_INTERVAL")
	unsetEnv(t, "POLL_INTERVAL")
	withAgentArgs(t, []string{"-a", "localhost:1234", "-r", "15", "-p", "3"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{serverAddress: "localhost:1234", reportInterval: 15, pollInterval: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_UsesDefaultsWhenNeitherEnvNorFlagsSet(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "REPORT_INTERVAL")
	unsetEnv(t, "POLL_INTERVAL")
	withAgentArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{serverAddress: "localhost:8080", reportInterval: 10, pollInterval: 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_PartialEnvMergesWithFlags(t *testing.T) {
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	unsetEnv(t, "REPORT_INTERVAL")
	unsetEnv(t, "POLL_INTERVAL")
	withAgentArgs(t, []string{"-r", "15", "-p", "3"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ADDRESS comes from env, report/poll interval come from flags
	want := &config{serverAddress: "192.168.1.1:9090", reportInterval: 15, pollInterval: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_EmptyEnvVarsTreatedAsUnset(t *testing.T) {
	t.Setenv("ADDRESS", "")
	t.Setenv("REPORT_INTERVAL", "")
	t.Setenv("POLL_INTERVAL", "")
	withAgentArgs(t, []string{"-a", "localhost:1234", "-r", "15", "-p", "3"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &config{serverAddress: "localhost:1234", reportInterval: 15, pollInterval: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestResolveConfig_InvalidReportIntervalEnvReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	t.Setenv("REPORT_INTERVAL", "not-a-number")
	unsetEnv(t, "POLL_INTERVAL")
	withAgentArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid REPORT_INTERVAL, got nil")
	}
}

func TestResolveConfig_InvalidPollIntervalEnvReturnsError(t *testing.T) {
	unsetEnv(t, "ADDRESS")
	unsetEnv(t, "REPORT_INTERVAL")
	t.Setenv("POLL_INTERVAL", "not-a-number")
	withAgentArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid POLL_INTERVAL, got nil")
	}
}

func TestResolveConfig_ZeroReportIntervalEnvIsOverriddenByFlagDefault(t *testing.T) {
	// documents current behavior: REPORT_INTERVAL="0" parses successfully but,
	// since cfg.reportInterval == 0, it is then overwritten by the flag's default.
	unsetEnv(t, "ADDRESS")
	t.Setenv("REPORT_INTERVAL", "0")
	unsetEnv(t, "POLL_INTERVAL")
	withAgentArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.reportInterval != 10 {
		t.Fatalf("expected reportInterval to fall back to flag default 10, got %d", got.reportInterval)
	}
}
