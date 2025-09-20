package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
	createdCharts := make(chan *packager.HelmizedManifests)
	errChannel := make(chan error, len(config.Releases))
	defer close(createdCharts)
	defer close(errChannel)

	go func() {
		gitRepo, err := git.NewClient(".")
		if err != nil {
			return
		}

		for charts := range createdCharts {
			if charts == nil {
				continue
			}
			// naming by main chartl
			branch := fmt.Sprintf("update/%s-%s", charts.Chart.Metadata.Name, charts.AppVersion())

			err = gitRepo.CreateBranch(config.PullRequest.DefaultBranch, branch)
			if err != nil {
				errChannel <- err
				return
			}
			err = gitRepo.Commit(charts)
			if err != nil {
				errChannel <- err
				return
			}
			err = gitRepo.Push(branch)
			if err != nil {
				errChannel <- err
				return
			}

			errChannel <- nil
		}
	}()

	for _, release := range config.Releases {
		ctx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
		defer cancel()
		go func() {
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

	results := 0
	for results < len(config.Releases) {
		select {
		case err := <-errChannel:
			if err != nil {
				common.Log.Errorf("Error during update: %v", err)
				return err
			}
			results++
		case <-time.After(30 * time.Second):
			common.Log.Errorf("Timeout waiting for chart updates")
			return fmt.Errorf("timeout waiting for chart updates")
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
