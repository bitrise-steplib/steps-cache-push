package main

import (
	"os"

	"github.com/bitrise-io/go-steputils/stepconf"
)

// Config stores the step inputs
type Config struct {
	Paths               string `env:"cache_paths"`
	IgnoredPaths        string `env:"ignore_check_on_paths"`
	CacheAPIURL         string `env:"cache_api_url,required"`
	FingerprintMethodID string `env:"fingerprint_method,opt[file-content-hash,file-mod-time]"`
	CompressArchive     string `env:"compress_archive,opt[true,false]"`
	DebugMode           bool   `env:"is_debug_mode"`
	StackID             string `env:"BITRISEIO_STACK_ID"`
}

// ParseConfig expands the step inputs from the current environment
func ParseConfig() (c Config, err error) {
	err = stepconf.Parse(&c)
	if err == nil {
		c.Paths += "\n" + os.Getenv("bitrise_cache_include_paths")
		c.IgnoredPaths += "\n" + os.Getenv("bitrise_cache_exclude_paths")
	}
	return
}

// Print prints the config
func (c Config) Print() {
	// TODO: update stepconf.Print to receive the output writer
	// and write test for this method
	stepconf.Print(c)
}
