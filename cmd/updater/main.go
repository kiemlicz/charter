package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
	ghup "github.com/kiemlicz/charter/internal/updater/github"
)

func main() {
	config, err := common.SetupConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
		return
	}

	common.Setup(config.Log.Level)

	if config.ModeOfOperation == "" {
		common.Log.Info("No operation specified, use --mode=publish or --mode=update")
		return
	}

	var wg sync.WaitGroup
	mainCtx := context.Background()

	for _, release := range config.Releases {
		ctx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
		defer cancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if config.ModeOfOperation == common.ModeUpdate {
				err := UpdateChart(ctx, &release, &config.Helm)
				if err != nil {
					common.Log.Errorf("Error generating Chart for release %s: %v", release.Repo, err)
					return
				} else {
					common.Log.Infof("Successfully generated Chart for release: %s", release.Repo)
				}
				CreatePr(ctx, &release, &config.Helm)
			} else if config.ModeOfOperation == common.ModePublish {
				PublishCharts(ctx, &release, &config.Helm)
			}
		}()
	}

	wg.Wait()
}

func UpdateChart(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) error {
	common.Log.Infof("Updating release: %s", releaseConfig.Repo)

	currentAppVersion, err := packager.PeekAppVersion(helmSettings.SrcDir, releaseConfig.ChartName)
	if err != nil {
		common.Log.Errorf("Failed to get app version from Helm chart %s: %v", releaseConfig.ChartName, err)
		return err
	}
	manifests, err := ghup.FetchManifests(ctx, releaseConfig, currentAppVersion)
	if err != nil {
		return err
	}
	if manifests == nil {
		common.Log.Infof("No updates for release %s, skipping", releaseConfig.Repo)
		return nil
	}

	common.Log.Infof("Creating or updating Helm chart %s with %d manifests", releaseConfig.ChartName, len(manifests.Manifests))

	modifiedManifests, extractedValues, err := packager.ChartModifier.ParametrizeManifests(
		packager.ChartModifier.FilterManifests(
			manifests,
			releaseConfig.Drop,
		),
		&releaseConfig.Modifications,
	)
	if err != nil {
		return err
	}
	_, err = packager.NewHelmChart(helmSettings, releaseConfig.ChartName, modifiedManifests, extractedValues, false)
	if err != nil {
		return err
	}

	if modifiedManifests.ContainsCrds() {
		crdsChartName := fmt.Sprintf("%s-crds", releaseConfig.ChartName)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(modifiedManifests.Crds), crdsChartName)
		_, err := packager.NewHelmChart(helmSettings, crdsChartName, modifiedManifests, new(map[string]any), true)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreatePr creates a PR with the updated charts
// creates branch for release
// commits and pushes changes
// creates PR against main branch
func CreatePr(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) {

}

func PublishCharts(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) {
	common.Log.Infof("Publishing Chart for release: %s", releaseConfig.Repo)

}
