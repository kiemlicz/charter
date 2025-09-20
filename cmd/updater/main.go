package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/git"
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

	if config.ModeOfOperation == common.ModeUpdate {
		err = UpdateMode(config)
	} else {
		err = PublishMode(config)
	}
	if err != nil {
		common.Log.Fatalf("Operation %s failed: %v", config.ModeOfOperation, err)
		os.Exit(1)
	}
}

func UpdateMode(config *common.Config) error {
	mainCtx := context.Background()
	var wg sync.WaitGroup
	createdCharts := make(chan *packager.HelmizedManifests, len(config.Releases))

	gitRepo, err := git.NewClient(".")
	if err != nil {
		return err
	}

	for _, release := range config.Releases {
		ctx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
		defer cancel()
		wg.Add(1)
		go func() {
			defer wg.Done()
			modifiedManifests, err := ProcessManifests(ctx, &release, &config.Helm)
			if err != nil {
				common.Log.Errorf("Error generating Chart for release %s: %v", release.Repo, err)
				createdCharts <- nil
				return
			} else if modifiedManifests == nil {
				createdCharts <- nil
				return
			}

			charts, err := packager.NewHelmCharts(&config.Helm, release.ChartName, modifiedManifests)
			if err != nil {
				createdCharts <- nil
				return
			}
			common.Log.Infof("Successfully created Helm chart for release: %s", release.Repo)
			createdCharts <- charts
		}()
	}

	wg.Wait()
	close(createdCharts)

	//commit starts once we receive all charts and workdir is not externally modified
	for charts := range createdCharts {
		if charts == nil {
			continue
		}
		// naming by main chart
		branch := fmt.Sprintf("update/%s-%s", charts.Chart.Metadata.Name, charts.AppVersion())

		err = gitRepo.CreateBranch(config.PullRequest.DefaultBranch, branch)
		if err != nil {
			return err
		}
		err = gitRepo.Commit(charts)
		if err != nil {
			return err
		}
		err = gitRepo.Push(branch)
		if err != nil {
			return err
		}
	}

	return nil
}

// PublishMode publishes the charts to the chart repository
// iterates over all charts/ and releases them
func PublishMode(config *common.Config) error {
	common.Log.Infof("Publishing Charts")
	return nil
}

func ProcessManifests(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) (*common.Manifests, error) {
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

	modifiedManifests, err := packager.ChartModifier.ParametrizeManifests(
		packager.ChartModifier.FilterManifests(
			manifests,
			releaseConfig.Drop,
		),
		&releaseConfig.Modifications,
	)
	if err != nil {
		return nil, err
	}

	return modifiedManifests, nil
}
