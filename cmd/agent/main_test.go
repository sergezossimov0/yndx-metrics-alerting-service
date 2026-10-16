package main

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestServerURL(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    string
		wantErr bool
	}{
		{name: "bare host and port", addr: "localhost:8080", want: "http://localhost:8080"},
		{name: "http kept", addr: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "https kept", addr: "https://localhost:8080", want: "https://localhost:8080"},
		{name: "uppercase scheme", addr: "HTTP://localhost:8080", want: "http://localhost:8080"},
		{name: "trailing slash trimmed", addr: "http://localhost:8080/", want: "http://localhost:8080"},
		{name: "path kept", addr: "http://localhost:8080/metrics", want: "http://localhost:8080/metrics"},
		{name: "port only", addr: ":8080", want: "http://:8080"},
		{name: "empty", addr: "", wantErr: true},
		{name: "scheme without host", addr: "http://", wantErr: true},
		{name: "unsupported scheme", addr: "ftp://localhost:8080", wantErr: true},
		{name: "space in host", addr: "local host:8080", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := serverURL(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tt.addr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
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

// resolvedConfig flattens agentConfig into comparable plain values.
type resolvedConfig struct {
	serverAddress  string
	reportInterval time.Duration
	pollInterval   time.Duration
}

func resolved(c *agentConfig) resolvedConfig {
	return resolvedConfig{
		serverAddress:  c.serverAddress,
		reportInterval: c.reportInterval.Duration,
		pollInterval:   c.pollInterval.Duration,
	}
}

func TestResolveConfig_AllEnvVarsTakePrecedenceOverFlags(t *testing.T) {
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	t.Setenv("REPORT_INTERVAL", "20")
	t.Setenv("POLL_INTERVAL", "5")
	withAgentArgs(t, []string{"-a", "localhost:1234", "-r", "15", "-p", "3"})

	got, err := resolveConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := resolvedConfig{serverAddress: "192.168.1.1:9090", reportInterval: 20 * time.Second, pollInterval: 5 * time.Second}
	if !reflect.DeepEqual(resolved(got), want) {
		t.Fatalf("expected %+v, got %+v", want, resolved(got))
	}
}

func TestResolveConfig_FlagsAreParsedEvenWhenAllEnvSet(t *testing.T) {
	// previously a fully populated env skipped flag parsing, so a bad flag went unnoticed
	t.Setenv("ADDRESS", "192.168.1.1:9090")
	t.Setenv("REPORT_INTERVAL", "20")
	t.Setenv("POLL_INTERVAL", "5")
	withAgentArgs(t, []string{"-unknown-flag"})

	if _, err := resolveConfig(); err == nil {
		t.Fatal("expected error for unknown flag, got nil")
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
	// a declared but empty ADDRESS counts as unset and keeps the flag value
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

// Interval parsing rules are tested in internal/config; here we only check
// that a rejected value surfaces from each variable and flag.
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

func TestResolveConfig_ZeroIntervalFlagReturnsError(t *testing.T) {
	// unlike the server's store interval, agent intervals do not allow 0
	for _, args := range [][]string{{"-r", "0"}, {"-p", "0"}} {
		t.Run(args[0]+"="+args[1], func(t *testing.T) {
			unsetAllAgentEnv(t)
			withAgentArgs(t, args)

			if _, err := resolveConfig(); err == nil {
				t.Fatalf("expected error for %v, got nil", args)
			}
		})
	}
}
