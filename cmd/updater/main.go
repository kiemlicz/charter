package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"github.com/kiemlicz/kubevirt-charts/internal/updater"
)

func main() {
	config, err := common.SetupConfig()
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

	assetsData, err := updater.DownloadAssets(ctx, client, releaseConfig, releaseData)
	if err != nil {
		common.Log.Errorf("Failed to download assets for release %s: %v", releaseConfig.Repo, err)
		return err
	}
	manifests, crds, err := updater.ParseAssets(assetsData)
	if err != nil {
		common.Log.Errorf("Failed to collect manifests for release %s: %v", releaseConfig.Repo, err)
		return err
	}

	if len(*crds) > 0 {
		crdsChartPath := fmt.Sprintf("%s-crds", releaseConfig.HelmChart)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(*crds), crdsChartPath)

		crdsChart, err := updater.NewHelmChart(crdsChartPath)
		if err != nil {
			return err
		}
		err = crdsChart.CreateTemplates(crds)
		if err != nil {
			return err
		}
		err = crdsChart.UpdateVersions(*releaseVersion, true)
		if err != nil {
			return err
		}
		err = crdsChart.Build()
		if err != nil {
			return err
		}
		err = crdsChart.Lint()
		if err != nil {
			return err
		}
		err = crdsChart.Package()
		if err != nil {
			return err
		}
	}

	common.Log.Infof("Creating or updating Helm chart %s with %d manifests", releaseConfig.HelmChart, len(*manifests))

	modifiedManifests, _, err := updater.Parametrize(
		updater.FilterManifests(
			manifests,
			releaseConfig.Filter,
		),
		&releaseConfig.Modifications,
	)
	if err != nil {
		return err
	}

	err = chart.CreateTemplates(modifiedManifests)
	if err != nil {
		return err
	}
	err = chart.UpdateVersions(*releaseVersion, false)
	if err != nil {
		return err
	}
	err = chart.Build()
	if err != nil {
		return err
	}
	err = chart.Lint()
	if err != nil {
		return err
	}
	err = chart.Package()
	if err != nil {
		return err
	}

	return nil
}
