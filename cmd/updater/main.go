package main

import (
	"context"
	"fmt"
	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"github.com/kiemlicz/kubevirt-charts/internal/updater"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"log"
	"os"
	"sync"
	"time"
)

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	Releases []common.Release `mapstructure:"githubReleases"`
}

func main() {
	config, err := setupConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
		return
	}

	common.Setup(config.Log.Level)

	var wg sync.WaitGroup
	mainCtx := context.Background()

	for _, release := range config.Releases {
		ctx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
		defer cancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := HandleRelease(ctx, &release)
			if err != nil {
				common.Log.Errorf("Error handling release %s: %v", release.Repo, err)
			} else {
				common.Log.Infof("Successfully handled release %s", release.Repo)
			}
		}()
	}

	wg.Wait()
}

func HandleRelease(ctx context.Context, releaseConfig *common.Release) error {
	client := github.NewClient(nil)
	releaseData, err := updater.DownloadReleaseMeta(ctx, client, releaseConfig)
	if err != nil {
		common.Log.Errorf("Failed to download release metadata for %s: %v", releaseConfig.Repo, err)
		return err
	}
	releaseVersion := releaseData.TagName
	common.Log.Infof("Latest release for %s: %s", releaseConfig.Repo, *releaseVersion)

	chart, err := updater.NewHelmChart(releaseConfig.HelmChart)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart for %s: %v", releaseConfig.HelmChart, err)
		return err
	}
	if chart.AppVersion() == *releaseVersion {
		common.Log.Infof("Chart %s is up to date with version %s", releaseConfig.HelmChart, chart.AppVersion())
		return nil
	}

	manifests, crds, err := updater.CollectManifests(ctx, client, releaseConfig, releaseData)
	if err != nil {
		common.Log.Errorf("Failed to collect manifests for release %s: %v", releaseConfig.Repo, err)
		return err
	}

	if len(*crds) > 0 {
		crdsChartPath := fmt.Sprintf("%s-crds", releaseConfig.HelmChart)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(*crds), crdsChartPath)
		crdsChart, err := updater.NewHelmChart(crdsChartPath)
		if err != nil {
			common.Log.Errorf("Failed to load CRDs Helm chart for %s: %v", crdsChartPath, err)
			return err
		}
		err = crdsChart.UpdateManifests(crds)
		if err != nil {
			common.Log.Errorf("Failed to update CRDs in chart %s: %v", crdsChartPath, err)
			return err
		}
		crdsChart.Save()
	}
	err = chart.UpdateManifests(manifests)
	if err != nil {
		common.Log.Errorf("Failed to update manifests in chart %s: %v", releaseConfig, err)
		return err
	}
	chart.Save()

	return nil
}

func setupConfig() (*Config, error) {
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
