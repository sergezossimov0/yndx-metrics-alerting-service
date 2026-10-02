package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

type intervalValid struct {
	interval time.Duration
	isSet    bool
}

type restoreValid struct {
	isRestore bool
	isSet     bool
}

type config struct {
	serverAddress   string
	storeInterval   intervalValid // default 300 seconds, 0 means synchronous writes, negative is invalid
	fileStoragePath string
	restore         restoreValid
	dbConnection    string
}

func (i *intervalValid) Set(value string) error {
	intervalInt, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("error parsing interval: %w", err)
	}

	if err := i.UpdateToSecond(intervalInt); err != nil {
		return err
	}
	i.isSet = true
	return nil
}

func (i *intervalValid) UpdateToSecond(sec int) error {
	if sec < 0 {
		return fmt.Errorf("interval must be >= 0, got %d", sec)
	}
	i.interval = time.Duration(sec) * time.Second
	return nil
}

func (r *restoreValid) Set(value string) error {
	restoreBool, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("error parsing restore: %w", err)
	}
	r.isRestore = restoreBool
	r.isSet = true
	return nil
}

func (c config) validation() bool {
	return c.serverAddress != "" && c.storeInterval.isSet && c.fileStoragePath != "" && c.restore.isSet && c.dbConnection != ""
}

// ---- resolveConfig reads configuration from environment variables and command line arguments.

func resolveConfig() (*config, error) {
	cfg := &config{}

	// environment variables take precedence over command line arguments
	if envServerAddr, ok := os.LookupEnv("ADDRESS"); ok {
		cfg.serverAddress = envServerAddr
	}

	// STORE_INTERVAL: default 300 seconds, 0 means synchronous writes, negative is invalid
	storeIntervalValid := intervalValid{}
	cfg.storeInterval = storeIntervalValid
	if envStoreInterval, ok := os.LookupEnv("STORE_INTERVAL"); ok {
		err := cfg.storeInterval.Set(envStoreInterval)
		if err != nil {
			return nil, errors.New("failed to set STORE_INTERVAL:" + err.Error())
		}
	}

	if envFileStoragePath, ok := os.LookupEnv("FILE_STORAGE_PATH"); ok {
		cfg.fileStoragePath = envFileStoragePath
	}

	rv := restoreValid{}
	cfg.restore = rv
	if envRestore, ok := os.LookupEnv("RESTORE"); ok {
		err := cfg.restore.Set(envRestore)
		if err != nil {
			return nil, errors.New("failed to set RESTORE:" + err.Error())
		}
	}

	if envDBConnection, ok := os.LookupEnv("DATABASE_DSN"); ok {
		cfg.dbConnection = envDBConnection
	}

	if cfg.validation() {
		return cfg, nil
	}

	// parse command line arguments
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Server startup options:")
		fs.PrintDefaults()
	}

	serverAddr := fs.String("a", "localhost:8080", "HTTP server address")
	storeInterval := fs.Int("i", 300, "store interval in seconds (0 makes writes synchronous)")
	fileStoragePath := fs.String("f", "metrics-db.json", "file storage path")
	restoreFlag := fs.Bool("r", true, "restore previously saved metrics on startup")
	dbConnectionFlag := fs.String("d", "", "database connection string (DSN)")

	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if cfg.serverAddress == "" {
		cfg.serverAddress = *serverAddr
	}
	if !cfg.storeInterval.isSet {
		err := cfg.storeInterval.UpdateToSecond(*storeInterval)
		if err != nil {
			return nil, errors.New("failed to set STORE_INTERVAL from flag:" + err.Error())
		}
	}

	if cfg.fileStoragePath == "" {
		cfg.fileStoragePath = *fileStoragePath
	}

	if !cfg.restore.isSet {
		cfg.restore.isRestore = *restoreFlag
	}

	if cfg.dbConnection == "" {
		cfg.dbConnection = *dbConnectionFlag
	}
	/*if cfg.dbConnection == "" {
		return nil, errors.New("database connection string (DSN) must be provided via -d flag or DATABASE_DSN environment variable")
	}*/

	return cfg, nil
}
