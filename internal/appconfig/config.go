package appconfig

import "flag"

type Config struct {
	Addr    string
	DataDir string
}

func FromArgs(args []string) (Config, error) {
	flags := flag.NewFlagSet("vaps", flag.ContinueOnError)
	cfg := Config{}
	flags.StringVar(&cfg.Addr, "addr", ":8080", "HTTP listen address")
	flags.StringVar(&cfg.DataDir, "data-dir", "data", "payload data directory")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
