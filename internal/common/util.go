package common

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	glog "gopkg.in/op/go-logging.v1"
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

	lvl, _ := glog.LogLevel(logLevel)
	if lvl == glog.DEBUG {
		lvl = glog.INFO // map debug to info as yq-lib debug is too verbose
	}
	glog.SetLevel(lvl, "yq-lib")
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

func DeepMerge(first *map[string]any, second *map[string]any) *map[string]any {
	out := make(map[string]any)

	for k, v1 := range *first {
		out[k] = v1
	}
	for k, v2 := range *second {
		if v1, ok := out[k]; ok {
			mapV1, ok1 := v1.(map[string]any)
			mapV2, ok2 := v2.(map[string]any)
			if ok1 && ok2 {
				out[k] = *DeepMerge(&mapV1, &mapV2)
			} else {
				// overwrite with second, regardless if list or scalar
				out[k] = v2
			}
		} else {
			out[k] = v2
		}
	}

	return &out
}

func ExtractYamls(assetData []byte) (*[]map[string]any, error) {
	reader := bytes.NewReader(assetData)
	decoder := yaml.NewDecoder(reader)

	var documents []map[string]any
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			Log.Errorf("Failed to decode YAML document for asset: %v", err)
			return nil, err
		}
		documents = append(documents, doc)
	}

	Log.Infof("Successfully unmarshalled %d documents", len(documents))
	return &documents, nil
}
