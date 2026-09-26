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

func unsetAllAgentEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ADDRESS", "REPORT_INTERVAL", "POLL_INTERVAL"} {
		unsetEnv(t, key)
	}
}

// resolvedConfig holds only the resolved values, so expectations do not
// depend on the internal isSet markers of intervalValid.
type resolvedConfig struct {
	serverAddress  string
	reportInterval time.Duration
	pollInterval   time.Duration
}

func resolved(c *config) resolvedConfig {
	return resolvedConfig{
		serverAddress:  c.serverAddress,
		reportInterval: c.reportInterval.interval,
		pollInterval:   c.pollInterval.interval,
	}
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
	want := resolvedConfig{serverAddress: "192.168.1.1:9090", reportInterval: 20 * time.Second, pollInterval: 5 * time.Second}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_UsesFlagsWhenEnvNotSet(t *testing.T) {
	unsetAllAgentEnv(t)
	withAgentArgs(t, []string{"-a", "localhost:1234", "-r", "15", "-p", "3"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{serverAddress: "localhost:1234", reportInterval: 15 * time.Second, pollInterval: 3 * time.Second}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_UsesDefaultsWhenNeitherEnvNorFlagsSet(t *testing.T) {
	unsetAllAgentEnv(t)
	withAgentArgs(t, []string{})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{serverAddress: "localhost:8080", reportInterval: 10 * time.Second, pollInterval: 2 * time.Second}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
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
	want := resolvedConfig{serverAddress: "192.168.1.1:9090", reportInterval: 15 * time.Second, pollInterval: 3 * time.Second}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_EmptyAddressEnvFallsBackToFlag(t *testing.T) {
	// documents current behavior: a declared but empty ADDRESS is replaced by the flag value
	unsetAllAgentEnv(t)
	t.Setenv("ADDRESS", "")
	withAgentArgs(t, []string{"-a", "localhost:1234"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.serverAddress != "localhost:1234" {
		t.Fatalf("expected serverAddress to fall back to flag value, got %q", got.serverAddress)
	}
}

func TestResolveConfig_EmptyIntervalEnvReturnsError(t *testing.T) {
	for _, key := range []string{"REPORT_INTERVAL", "POLL_INTERVAL"} {
		t.Run(key, func(t *testing.T) {
			unsetAllAgentEnv(t)
			t.Setenv(key, "")
			withAgentArgs(t, []string{})

			if _, err := resolveConfig(); err == nil {
				t.Fatalf("expected error for empty %s, got nil", key)
			}
		})
	}
}

func TestResolveConfig_InvalidReportIntervalEnvReturnsError(t *testing.T) {
	unsetAllAgentEnv(t)
	t.Setenv("REPORT_INTERVAL", "not-a-number")
	withAgentArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid REPORT_INTERVAL, got nil")
	}
}

func TestResolveConfig_InvalidPollIntervalEnvReturnsError(t *testing.T) {
	unsetAllAgentEnv(t)
	t.Setenv("POLL_INTERVAL", "not-a-number")
	withAgentArgs(t, []string{})

	_, err := resolveConfig()
	if err == nil {
		t.Fatal("expected error for invalid POLL_INTERVAL, got nil")
	}
}

func TestResolveConfig_NonPositiveIntervalEnvReturnsError(t *testing.T) {
	for _, key := range []string{"REPORT_INTERVAL", "POLL_INTERVAL"} {
		for _, value := range []string{"0", "-1"} {
			t.Run(key+"="+value, func(t *testing.T) {
				unsetAllAgentEnv(t)
				t.Setenv(key, value)
				withAgentArgs(t, []string{})

				if _, err := resolveConfig(); err == nil {
					t.Fatalf("expected error for %s=%s, got nil", key, value)
				}
			})
		}
	}
}

func TestResolveConfig_NonPositiveIntervalFlagReturnsError(t *testing.T) {
	for _, args := range [][]string{{"-r", "0"}, {"-r", "-1"}, {"-p", "0"}, {"-p", "-1"}} {
		t.Run(args[0]+"="+args[1], func(t *testing.T) {
			unsetAllAgentEnv(t)
			withAgentArgs(t, args)

			if _, err := resolveConfig(); err == nil {
				t.Fatalf("expected error for %v, got nil", args)
			}
		})
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
