package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
	"github.com/kiemlicz/charter/internal/updater/git"
	ghup "github.com/kiemlicz/charter/internal/updater/github"
)

func main() {
	config, err := common.SetupConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
		return
	}
	common.Setup(config.Log.Level)

	if config.ModeOfOperation == common.ModeUpdate {
		err = UpdateMode(config)
	} else {
		err = PublishMode(config)
	}
	if err != nil {
		common.Log.Fatalf("Mode %s failed: %v", config.ModeOfOperation, err)
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
			charts, err := packager.FetchAndUpdate(ctx, &release, &config.Helm)
			if err != nil {
				common.Log.Errorf("Error generating Chart for release %s: %v", release.Repo, err)
				createdCharts <- nil
			} else {
				common.Log.Infof("Successfully created Helm chart for release: %s", release.Repo)
				createdCharts <- charts
			}
		}()
	}

	wg.Wait()
	close(createdCharts) //so that range below completes

	if config.Offline {
		common.Log.Infof("Offline mode, skipping git operations")
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
	defer cancel()
	//commit starts once we receive all charts and workdir is not externally modified
	for charts := range createdCharts {
		if charts == nil {
			continue
		}
		// naming by main chart
		branch := fmt.Sprintf("update/%s-%s", charts.Chart.Metadata.Name, charts.AppVersion())

		exists, err := gitRepo.BranchExists(branch)
		if err != nil {
			return err
		}
		if exists {
			common.Log.Infof("Branch %s already exists: close it or merge it, then re-try, skipping", branch)
			continue
		}
		err = gitRepo.CreateBranch(config.PullRequest.DefaultBranch, branch)
		if err != nil {
			return err
		}
		err = gitRepo.Commit(charts)
		if err != nil {
			return err
		}
		err = gitRepo.Push(timeoutCtx, &config.PullRequest, branch)
		if err != nil {
			return err
		}

		err = ghup.CreatePr(timeoutCtx, &config.PullRequest, branch)
		if err != nil {
			return err
		}
	}

	return nil
}

// PublishMode publishes the charts to the chart repository
// iterates over all charts/* and releases them
func PublishMode(config *common.Config) error {
	common.Log.Infof("Publishing Charts")
	files, err := os.ReadDir(config.Helm.SrcDir)
	if err != nil {
		return fmt.Errorf("failed to read charts directory: %w", err)
	}
	for _, file := range files {
		if file.IsDir() {
			chartPath := filepath.Join(config.Helm.SrcDir, file.Name())
			common.Log.Infof("Found chart directory: %s", chartPath)
			packagedPath, err := packager.Package(chartPath, &config.Helm)
			if err != nil {
				return err
			}
			ref, err := packager.Push(packagedPath, config.Helm.Remote)
			if err != nil {
				return err
			}
			common.Log.Infof("Chart %s published to %s", file.Name(), ref)
		}
	}
	return nil
}
