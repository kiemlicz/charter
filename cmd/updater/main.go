package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

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
				chart, err := UpdateChart(ctx, &release, &config.Helm)
				if err != nil {
					common.Log.Errorf("Error generating Chart for release %s: %v", release.Repo, err)
					return
				} else if chart == nil {
					return
				} else {
					common.Log.Infof("Successfully generated Chart for release: %s", release.Repo)
				}
				// todo commit there in parallel
				err = CreateBranch(fmt.Sprintf("update/%s-%s", release.ChartName, chart.AppVersion()))
				if err != nil {
					return
				}
				CreatePr(ctx, &release, &config.Helm)
			} else if config.ModeOfOperation == common.ModePublish {
				PublishCharts(ctx, &release, &config.Helm)
			}
		}()
	}

	wg.Wait()
}

func CreateBranch(branchName string) error {
	repo, err := git.PlainOpen(".")
	if err != nil {
		common.Log.Errorf("Failed to open git repo: %v", err)
		return err
	}

	headRef, err := repo.Head()
	if err != nil {
		common.Log.Errorf("Failed to get HEAD: %v", err)
		return err
	}

	refName := plumbing.NewBranchReferenceName(branchName)
	err = repo.Storer.SetReference(plumbing.NewHashReference(refName, headRef.Hash()))
	if err != nil {
		common.Log.Errorf("Failed to create branch: %v", err)
		return err
	}

	common.Log.Infof("Created branch: %s (no checkout)", branchName)

	return nil
}

func UpdateChart(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) (*packager.HelmChart, error) {
	common.Log.Infof("Updating release: %s", releaseConfig.Repo)

	currentAppVersion, err := packager.PeekAppVersion(helmSettings.SrcDir, releaseConfig.ChartName)
	if err != nil {
		common.Log.Errorf("Failed to get app version from Helm chart %s: %v", releaseConfig.ChartName, err)
		return nil, err
	}
	manifests, err := ghup.FetchManifests(ctx, releaseConfig, currentAppVersion)
	if err != nil {
		return nil, err
	}
	if manifests == nil {
		common.Log.Infof("No updates for release %s, skipping", releaseConfig.Repo)
		return nil, nil
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
		return nil, err
	}
	chart, err := packager.NewHelmChart(helmSettings, releaseConfig.ChartName, modifiedManifests, extractedValues, false)
	if err != nil {
		return nil, err
	}

	if modifiedManifests.ContainsCrds() {
		crdsChartName := fmt.Sprintf("%s-crds", releaseConfig.ChartName)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(modifiedManifests.Crds), crdsChartName)
		_, err := packager.NewHelmChart(helmSettings, crdsChartName, modifiedManifests, new(map[string]any), true)
		if err != nil {
			return nil, err
		}
	}

	return chart, nil
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
