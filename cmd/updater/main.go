package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
	"github.com/kiemlicz/charter/internal/updater/chart"
	"github.com/kiemlicz/charter/internal/updater/git"
	ghup "github.com/kiemlicz/charter/internal/updater/github"
)

func main() {
	config, err := common.SetupConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	common.Setup(config.Log.Level)

	switch config.ModeOfOperation {
	case common.ModeUpdate:
		err = UpdateMode(config)
	case common.ModePublish:
		err = PublishMode(config)
	default:
		err = fmt.Errorf("unsupported mode: %s", config.ModeOfOperation)
	}
	if err != nil {
		common.Log.Errorf("Mode %s failed: %v", config.ModeOfOperation, err)
		os.Exit(1)
	}
}

func UpdateMode(config *common.Config) error {
	mainCtx := context.Background()

	sources, err := buildSources(config)
	if err != nil {
		return fmt.Errorf("failed to build manifest sources: %w", err)
	}
	if len(sources) == 0 {
		common.Log.Warnf("No sources configured, nothing to update")
		return nil
	}

	gitRepo, err := git.NewClient(".")
	if err != nil {
		return err
	}

	// Phase 1: fetch + prepare charts in parallel.
	createdCharts := make(chan *packager.HelmizedManifests, len(sources))
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
			defer cancel()
			charts, err := packager.FetchAndUpdate(ctx, src, &config.Helm)
			if err != nil {
				common.Log.Errorf("Error generating chart for %s: %v", src.ChartName(), err)
				createdCharts <- nil
			} else {
				common.Log.Infof("Successfully created Helm chart: %s", src.ChartName())
				createdCharts <- charts
			}
		}()
	}
	wg.Wait()
	close(createdCharts)

	if config.Offline {
		common.Log.Infof("Offline mode, skipping git operations")
		return nil
	}

	// Phase 2: commit / push / PR - must be serial to avoid git state conflicts.
	timeoutCtx, cancel := context.WithTimeout(mainCtx, 30*time.Second)
	defer cancel()
	for charts := range createdCharts {
		if charts == nil {
			continue
		}
		branch := fmt.Sprintf("update/%s-%s", charts.Chart.Metadata.Name, charts.AppVersion())

		exists, err := gitRepo.BranchExists(branch)
		if err != nil {
			return err
		}
		if exists {
			common.Log.Warnf("Branch %s already exists: close it or merge it, then re-try, skipping", branch)
			continue
		}
		if err = gitRepo.CreateBranch(config.PullRequest.DefaultBranch, branch); err != nil {
			return err
		}
		if err = gitRepo.Commit(charts); err != nil {
			return err
		}
		if err = gitRepo.Push(timeoutCtx, &config.PullRequest, branch); err != nil {
			return err
		}
		if err = ghup.CreatePr(timeoutCtx, &config.PullRequest, branch); err != nil {
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
				if errors.Is(err, packager.ErrVersionExists) {
					common.Log.Infof("Chart %s not published, already exists in desired version", file.Name())
					continue
				}
				return err
			}
			common.Log.Infof("Chart %s published to %s", file.Name(), ref)
		}
	}
	return nil
}

// buildSources converts the sources[] config into ManifestSource implementations.
func buildSources(config *common.Config) ([]common.ManifestSource, error) {
	sources := make([]common.ManifestSource, 0, len(config.Sources))

	for i := range config.Sources {
		spec := &config.Sources[i]
		switch spec.Type {
		case common.SourceTypeGithub:
			if spec.Github == nil {
				return nil, fmt.Errorf("source %d has type 'github' but no 'github' block", i)
			}
			sources = append(sources, ghup.NewGithubSource(spec.Github, &spec.Helm))
		case common.SourceTypeHelmChart:
			if spec.HelmChart == nil {
				return nil, fmt.Errorf("source %d has type 'helmChart' but no 'helmChart' block", i)
			}
			sources = append(sources, chart.NewHelmChartSource(spec.HelmChart, &spec.Helm))
		default:
			return nil, fmt.Errorf("source %d has unknown type: %q", i, spec.Type)
		}
	}

	return sources, nil
}
