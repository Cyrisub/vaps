package appconfig

import (
	"flag"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Addr                string
	DataDir             string
	MetadataDB          string
	LogDir              string
	LogRetentionDays    int
	CacheBytes          int64
	CacheMaxObjectBytes int64
	StatusLogInterval   time.Duration
}

func FromArgs(args []string) (Config, error) {
	executable, err := os.Executable()
	if err != nil {
		return Config{}, err
	}
	return FromArgsWithBaseDir(args, filepath.Dir(executable))
}

func FromArgsWithBaseDir(args []string, baseDir string) (Config, error) {
	flags := flag.NewFlagSet("vaps", flag.ContinueOnError)
	cfg := Config{}
	metadataDB := ""
	flags.StringVar(&cfg.Addr, "addr", ":8588", "HTTP listen address")
	flags.StringVar(&cfg.DataDir, "data-dir", "data", "payload data directory")
	flags.StringVar(&metadataDB, "metadata-db", "", "metadata database path")
	flags.StringVar(&cfg.LogDir, "log-dir", "logs", "log directory")
	flags.IntVar(&cfg.LogRetentionDays, "log-retention-days", 7, "number of daily log files to keep; use 0 to disable cleanup")
	flags.Int64Var(&cfg.CacheBytes, "cache-bytes", 64*1024*1024, "in-memory LRU cache capacity in bytes")
	flags.Int64Var(&cfg.CacheMaxObjectBytes, "cache-max-object-bytes", 4*1024*1024, "maximum payload size cached in memory")
	flags.DurationVar(&cfg.StatusLogInterval, "status-log-interval", time.Minute, "interval between periodic status logs; use 0 to disable")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if metadataDB == "" {
		metadataDB = filepath.Join(cfg.DataDir, "metadata.db")
	}
	cfg.DataDir = resolvePath(baseDir, cfg.DataDir)
	cfg.LogDir = resolvePath(baseDir, cfg.LogDir)
	metadataDB = resolvePath(baseDir, metadataDB)
	cfg.MetadataDB = metadataDB
	return cfg, nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
