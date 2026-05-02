package appconfig

import (
	"flag"
	"os"
	"path/filepath"
)

type Config struct {
	Addr                string
	DataDir             string
	MetadataDB          string
	CacheBytes          int64
	CacheMaxObjectBytes int64
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
	flags.Int64Var(&cfg.CacheBytes, "cache-bytes", 64*1024*1024, "in-memory LRU cache capacity in bytes")
	flags.Int64Var(&cfg.CacheMaxObjectBytes, "cache-max-object-bytes", 4*1024*1024, "maximum payload size cached in memory")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if metadataDB == "" {
		metadataDB = filepath.Join(cfg.DataDir, "metadata.db")
	}
	cfg.DataDir = resolvePath(baseDir, cfg.DataDir)
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
