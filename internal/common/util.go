package common

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"log"
	"os"
	"strings"
)

var Log *logrus.Logger

func Setup(logLevel string) {
	Log = logrus.New()
	level, err := logrus.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		Log.Warnf("Invalid Log level in config: %s. Using 'info'.", logLevel)
		level = logrus.InfoLevel
	}

	Log.SetLevel(level)
	Log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
}

func SetupConfig() (*Config, error) {
	v := viper.New()

	v.SetConfigFile("config.yaml") // default config file full path, not adding paths as they pick single file

	pflag.String("log.level", "", "log level (overrides yaml file)")
	pflag.Parse()
	_ = v.BindPFlags(pflag.CommandLine)

	if err := v.ReadInConfig(); err != nil {
		panic(fmt.Errorf("error reading config file, %s", err))
	}

	loader := func(configFullPath string) {
		if _, err := os.Stat(configFullPath); err == nil {
			v.SetConfigFile(configFullPath)
			if err := v.MergeInConfig(); err != nil {
				panic(fmt.Errorf("error merging config file, %s", err))
			}
		}
	}

	loader(".local/config.yaml")

	var config *Config
	err := v.Unmarshal(&config)
	if err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
		return config, err
	}

	return config, nil
}
