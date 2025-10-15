package common

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	kyaml "github.com/knadh/koanf/parsers/yaml"
	kfile "github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
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
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.String("mode", "", "update|publish mode (overrides yaml file)")
	f.Bool("offline", false, "skip git operations, useful for development")
	f.String("log.level", "", "log level (overrides yaml file)")
	f.String("pr.authToken", "", "user token for auth")
	if err := f.Parse(os.Args[1:]); err != nil {
		log.Fatalf("error parsing flags: %v", err)
	}

	k := koanf.NewWithConf(koanf.Conf{
		Delim:       ".",
		StrictMerge: true,
	})
	parser := kyaml.Parser()
	files := []string{"config.yaml", ".local/config.yaml"}

	for _, file := range files {
		if fileExists(file) {
			if err := k.Load(kfile.Provider(file), parser); err != nil {
				log.Fatalf("error loading config: %v", err)
			}
		}
	}
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		log.Fatalf("error loading config: %v", err)
	}

	var config Config
	err := k.Unmarshal("", &config)
	if err != nil {
		log.Fatalf("error unmarshalling config: %v", err)
	}

	// Fallback: if pr.authToken still empty, use GITHUB_TOKEN env
	if config.PullRequest.AuthToken == "" {
		if envTok := os.Getenv("GITHUB_TOKEN"); envTok != "" {
			config.PullRequest.AuthToken = envTok
		}
	}

	if config.ModeOfOperation == "" {
		log.Fatalf("No operation specified, use --mode=publish or --mode=update")
	}

	return &config, nil
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}
