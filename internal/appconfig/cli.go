package appconfig

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"vaps/internal/version"
)

const defaultConfigPath = "config.toml"

// ErrHelp indicates that the user requested help.
var ErrHelp = errors.New("help requested")

type cliOptions struct {
	ConfigPath string `short:"c" name:"config" default:"config.toml" help:"TOML config file path"`
	Settings   Config `embed:""`
}

func parseCLI(args []string, cfg Config, configPath string) (Config, string, error) {
	cli := cliOptions{
		ConfigPath: configPath,
		Settings:   cfg,
	}
	helpShown := false
	parser, err := kong.New(&cli,
		kong.Name("vaps"),
		kong.Exit(func(code int) {
			if code == 0 {
				helpShown = true
				return
			}
			os.Exit(code)
		}),
	)
	if err != nil {
		return Config{}, "", err
	}
	if _, err := parser.Parse(args); err != nil {
		return Config{}, "", err
	}
	if helpShown {
		return Config{}, "", ErrHelp
	}
	mergeConfigOverrides(&cfg, cli.Settings)
	return cfg, cli.ConfigPath, nil
}

func mergeConfigOverrides(base *Config, parsed Config) {
	mergeNonZeroFields(reflect.ValueOf(base).Elem(), reflect.ValueOf(parsed))
}

func mergeNonZeroFields(base, parsed reflect.Value) {
	baseType := base.Type()
	for index := 0; index < base.NumField(); index++ {
		field := baseType.Field(index)
		if !field.IsExported() {
			continue
		}
		baseField := base.Field(index)
		parsedField := parsed.Field(index)
		if !parsedField.IsValid() {
			continue
		}
		switch {
		case parsedField.Kind() == reflect.Struct && parsedField.Type() != reflect.TypeOf(Duration(0)):
			mergeNonZeroFields(baseField, parsedField)
		case isNonZeroValue(parsedField):
			baseField.Set(parsedField)
		}
	}
}

func isNonZeroValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return value.String() != ""
	case reflect.Int, reflect.Int64:
		return value.Int() != 0
	case reflect.Slice:
		return value.Len() > 0
	default:
		if value.Type() == reflect.TypeOf(Duration(0)) {
			return time.Duration(value.Int()) != 0
		}
		return !value.IsZero()
	}
}

func parseConfigPath(args []string) (string, error) {
	configPath := defaultConfigPath
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-c", arg == "--config":
			value, nextIndex, err := readFlagValue(args, index, "config")
			if err != nil {
				return "", err
			}
			configPath = value
			index = nextIndex
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-c="):
			configPath = strings.TrimPrefix(arg, "-c=")
		}
	}
	return configPath, nil
}

func readFlagValue(args []string, index int, name string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("%s flag requires a value", name)
	}
	return args[index+1], index + 1, nil
}

func configFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func maybePrintVersion(args []string) (bool, error) {
	for _, arg := range args {
		switch arg {
		case "-v", "--version":
			fmt.Println(version.String())
			return true, nil
		}
	}
	return false, nil
}
