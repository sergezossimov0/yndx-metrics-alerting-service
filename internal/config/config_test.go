package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestInterval_Set(t *testing.T) {
	tests := []struct {
		name      string
		allowZero bool
		value     string
		want      time.Duration
		wantErr   bool
	}{
		{name: "positive", value: "10", want: 10 * time.Second},
		{name: "positive with zero allowed", allowZero: true, value: "300", want: 300 * time.Second},
		{name: "zero allowed", allowZero: true, value: "0", want: 0},
		{name: "zero not allowed", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "negative with zero allowed", allowZero: true, value: "-1", wantErr: true},
		{name: "not a number", value: "abc", wantErr: true},
		{name: "fractional", value: "1.5", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := Interval{Duration: time.Minute, AllowZero: tt.allowZero}

			err := i.Set(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.value)
				}
				if i.Duration != time.Minute {
					t.Fatalf("expected a rejected value to keep the previous duration, got %v", i.Duration)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if i.Duration != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, i.Duration)
			}
		})
	}
}

func TestInterval_StringReturnsSeconds(t *testing.T) {
	i := Interval{Duration: 300 * time.Second}
	if got := i.String(); got != "300" {
		t.Fatalf("expected %q, got %q", "300", got)
	}
}

func TestNormalizeHelpArg_ReplacesLongHelpFlagWithShort(t *testing.T) {
	got := NormalizeHelpArg([]string{"-a", "localhost:8080", "--help"})
	want := []string{"-a", "localhost:8080", "-h"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNormalizeHelpArg_LeavesOtherArgsUnchanged(t *testing.T) {
	args := []string{"-a", "localhost:8080", "-r", "10"}
	got := NormalizeHelpArg(args)

	if !reflect.DeepEqual(got, args) {
		t.Fatalf("expected %v, got %v", args, got)
	}
}

func TestNormalizeHelpArg_EmptyArgsReturnsEmptySlice(t *testing.T) {
	got := NormalizeHelpArg([]string{})

	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestLogLevel_UsesEnvVarWhenSet(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")

	if got := LogLevel(); got != "debug" {
		t.Fatalf("expected %q, got %q", "debug", got)
	}
}

func TestLogLevel_UsesDefaultWhenEnvNotSet(t *testing.T) {
	orig, wasSet := os.LookupEnv("LOG_LEVEL")
	os.Unsetenv("LOG_LEVEL")
	t.Cleanup(func() {
		if wasSet {
			os.Setenv("LOG_LEVEL", orig)
		}
	})

	if got := LogLevel(); got != "INFO" {
		t.Fatalf("expected %q, got %q", "INFO", got)
	}
}

func TestLogLevel_EmptyEnvVarIsPassedThrough(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")

	if got := LogLevel(); got != "" {
		t.Fatalf("expected %q, got %q", "", got)
	}
}
