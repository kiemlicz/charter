package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"github.com/kiemlicz/kubevirt-charts/internal/packager"
	ghup "github.com/kiemlicz/kubevirt-charts/internal/updater/github"
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

func HandleRelease(ctx context.Context, releaseConfig *common.GithubRelease) error {
	currentAppVersion, err := packager.PeekAppVersion(releaseConfig.HelmChart)
	if err != nil {
		common.Log.Errorf("Failed to get app version from Helm chart %s: %v", releaseConfig.HelmChart, err)
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

	common.Log.Infof("Creating or updating Helm chart %s with %d manifests", releaseConfig.HelmChart, len(manifests.Manifests))

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
	_, err = packager.NewHelmChart(releaseConfig.HelmChart, modifiedManifests, extractedValues, false)
	if err != nil {
		return err
	}

	if modifiedManifests.ContainsCrds() {
		crdsChartPath := fmt.Sprintf("%s-crds", releaseConfig.HelmChart)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(modifiedManifests.Crds), crdsChartPath)
		_, err := packager.NewHelmChart(crdsChartPath, modifiedManifests, new(map[string]any), true)
		if err != nil {
			return err
		}
	}

	return nil
}
