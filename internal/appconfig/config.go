package appconfig

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Duration time.Duration

type Config struct {
	Addr              string       `json:"addr"`
	DataDir           string       `json:"data_dir"`
	MetadataDB        string       `json:"metadata_db"`
	LogDir            string       `json:"log_dir"`
	LogRetentionDays  int          `json:"log_retention_days"`
	StatusLogInterval Duration     `json:"status_log_interval"`
	Cache             CacheConfig  `json:"cache"`
	Backup            BackupConfig `json:"backup"`
}

type CacheConfig struct {
	Bytes          int64 `json:"bytes"`
	MaxObjectBytes int64 `json:"max_object_bytes"`
}

type BackupConfig struct {
	Backend       string          `json:"backend"`
	FlushInterval Duration        `json:"flush_interval"`
	MaxPending    int             `json:"max_pending"`
	SVN           SVNBackupConfig `json:"svn"`
}

type SVNBackupConfig struct {
	URL     string `json:"url"`
	Bin     string `json:"bin"`
	MuccBin string `json:"mucc_bin" flag_alias:"backup-svnmucc-bin"`
}

func FromArgs(args []string) (Config, error) {
	if args == nil {
		args = os.Args[1:]
	}
	executable, err := os.Executable()
	if err != nil {
		return Config{}, err
	}
	return FromArgsWithBaseDir(args, filepath.Dir(executable))
}

func FromArgsWithBaseDir(args []string, baseDir string) (Config, error) {
	cfg := defaultConfig()
	configPath, err := findConfigPath(args)
	if err != nil {
		return Config{}, err
	}
	if configPath != "" {
		if err := loadJSONConfig(resolvePath(baseDir, configPath), &cfg); err != nil {
			return Config{}, err
		}
	}
	flags := flag.NewFlagSet("vaps", flag.ContinueOnError)
	flags.String("config", configPath, "JSON config file path")
	if err := registerFlags(flags, &cfg); err != nil {
		return Config{}, err
	}
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	normalize(&cfg)
	if cfg.MetadataDB == "" {
		cfg.MetadataDB = filepath.Join(cfg.DataDir, "metadata.db")
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	cfg.DataDir = resolvePath(baseDir, cfg.DataDir)
	cfg.LogDir = resolvePath(baseDir, cfg.LogDir)
	cfg.MetadataDB = resolvePath(baseDir, cfg.MetadataDB)
	return cfg, nil
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d *Duration) Set(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return d.Set(text)
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return errors.New("duration must be a string like \"1m\" or a number of nanoseconds")
	}
	*d = Duration(time.Duration(nanos))
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func defaultConfig() Config {
	return Config{
		Addr:              ":8588",
		DataDir:           "data",
		LogDir:            "logs",
		LogRetentionDays:  7,
		StatusLogInterval: Duration(time.Minute),
		Cache: CacheConfig{
			Bytes:          64 * 1024 * 1024,
			MaxObjectBytes: 4 * 1024 * 1024,
		},
		Backup: BackupConfig{
			FlushInterval: Duration(time.Minute),
			MaxPending:    100,
			SVN: SVNBackupConfig{
				Bin:     "svn",
				MuccBin: "svnmucc",
			},
		},
	}
}

func loadJSONConfig(path string, cfg *Config) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func findConfigPath(args []string) (string, error) {
	for index, arg := range args {
		if arg == "-config" || arg == "--config" {
			if index+1 >= len(args) {
				return "", errors.New("config flag requires a value")
			}
			return args[index+1], nil
		}
		if strings.HasPrefix(arg, "-config=") {
			return strings.TrimPrefix(arg, "-config="), nil
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config="), nil
		}
	}
	return "", nil
}

func registerFlags(flags *flag.FlagSet, cfg *Config) error {
	return registerValueFlags(flags, nil, reflect.ValueOf(cfg).Elem())
}

func registerValueFlags(flags *flag.FlagSet, prefix []string, value reflect.Value) error {
	valueType := value.Type()
	if valueType == reflect.TypeOf(Duration(0)) || value.Kind() != reflect.Struct {
		registerFlag(flags, strings.Join(prefix, "-"), "override "+strings.Join(prefix, "."), value)
		return nil
	}
	for index := 0; index < value.NumField(); index++ {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		name := jsonFieldName(field)
		if name == "" {
			continue
		}
		child := value.Field(index)
		childPrefix := append(prefix, strings.ReplaceAll(name, "_", "-"))
		if err := registerValueFlags(flags, childPrefix, child); err != nil {
			return err
		}
		for _, alias := range splitAliases(field.Tag.Get("flag_alias")) {
			registerFlag(flags, alias, "override "+strings.Join(childPrefix, "."), child)
		}
	}
	return nil
}

func registerFlag(flags *flag.FlagSet, name, usage string, value reflect.Value) {
	if value.Type() == reflect.TypeOf(Duration(0)) {
		flags.Var(durationFlag{value: value}, name, usage)
		return
	}
	flags.Var(scalarFlag{value: value}, name, usage)
}

func splitAliases(value string) []string {
	aliases := []string{}
	for _, alias := range strings.Split(value, ",") {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func jsonFieldName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "-" {
		return ""
	}
	if name != "" {
		return name
	}
	return strings.ToLower(field.Name)
}

type durationFlag struct {
	value reflect.Value
}

func (f durationFlag) String() string {
	return time.Duration(f.value.Int()).String()
}

func (f durationFlag) Set(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	f.value.SetInt(int64(parsed))
	return nil
}

type scalarFlag struct {
	value reflect.Value
}

func (f scalarFlag) String() string {
	switch f.value.Kind() {
	case reflect.String:
		return f.value.String()
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(f.value.Int(), 10)
	default:
		return ""
	}
}

func (f scalarFlag) Set(value string) error {
	switch f.value.Kind() {
	case reflect.String:
		f.value.SetString(value)
		return nil
	case reflect.Int:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 0)
		if err != nil {
			return err
		}
		f.value.SetInt(parsed)
		return nil
	case reflect.Int64:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return err
		}
		f.value.SetInt(parsed)
		return nil
	default:
		return fmt.Errorf("unsupported config field type %s", f.value.Type())
	}
}

func normalize(cfg *Config) {
	cfg.Backup.Backend = strings.ToLower(strings.TrimSpace(cfg.Backup.Backend))
	cfg.Backup.SVN.URL = strings.TrimSpace(cfg.Backup.SVN.URL)
	cfg.Backup.SVN.Bin = strings.TrimSpace(cfg.Backup.SVN.Bin)
	cfg.Backup.SVN.MuccBin = strings.TrimSpace(cfg.Backup.SVN.MuccBin)
}

func validate(cfg Config) error {
	if time.Duration(cfg.Backup.FlushInterval) < 0 {
		return errors.New("backup-flush-interval must not be negative")
	}
	if cfg.Backup.MaxPending < 0 {
		return errors.New("backup-max-pending must not be negative")
	}
	switch cfg.Backup.Backend {
	case "", "none":
		return nil
	case "svn":
		if cfg.Backup.SVN.URL == "" {
			return errors.New("backup-svn-url is required when backup backend is svn")
		}
		if cfg.Backup.SVN.Bin == "" {
			return errors.New("backup-svn-bin must not be empty when backup backend is svn")
		}
		if cfg.Backup.SVN.MuccBin == "" {
			return errors.New("backup-svnmucc-bin must not be empty when backup backend is svn")
		}
		return nil
	default:
		return errors.New("backup backend must be one of none, svn")
	}
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
