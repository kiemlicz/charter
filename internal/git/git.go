package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
)

type Client struct {
	Repository *gogit.Repository
	usesSsh    bool
}

func NewClient(repoPath string) (*Client, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		common.Log.Errorf("Failed to open git repo at %s: %v", repoPath, err)
		return nil, err
	}

	usesSsh := false
	remotes, err := repo.Remotes()
	if err == nil {
		for _, remote := range remotes {
			for _, url := range remote.Config().URLs {
				if strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://") {
					usesSsh = true
					break
				}
			}
		}
	}

	return &Client{
		Repository: repo,
		usesSsh:    usesSsh,
	}, nil
}

func (g *Client) CreateBranch(defaultBranch, branchName string) error {
	defaultRefName := gogitplumbing.NewBranchReferenceName(defaultBranch)
	defaultRef, err := g.Repository.Reference(defaultRefName, true)
	if err != nil {
		common.Log.Errorf("Failed to get reference for branch %s: %v", defaultBranch, err)
		return err
	}

	refName := gogitplumbing.NewBranchReferenceName(branchName)
	err = g.Repository.Storer.SetReference(gogitplumbing.NewHashReference(refName, defaultRef.Hash()))
	if err != nil {
		common.Log.Errorf("Failed to create branch: %s, due to: %v", refName, err)
		return err
	}

	wt, err := g.Repository.Worktree()
	if err != nil {
		common.Log.Errorf("Failed to get worktree: %v", err)
		return err
	}

	common.Log.Infof("Switching branch to: %v", refName)
	err = wt.Checkout(&gogit.CheckoutOptions{
		Branch: refName,
		Keep:   true, // allows to checkout even if there are unstaged changes
		Create: false,
		Force:  false,
	})
	if err != nil {
		if err == gogit.ErrUnstagedChanges {
			status, _ := wt.Status()
			var files []string
			for file := range status {
				files = append(files, file)
			}
			common.Log.Errorf("Failed to checkout branch: %s, worktree contains unstaged changes in files: %v", refName, files)
		} else {
			common.Log.Errorf("Failed to checkout branch: %s, due to: %v", refName, err)
		}
		return err
	}

	common.Log.Infof("Switched branch from source: %s to: %s", defaultBranch, branchName)
	g.status(wt)

	return nil
}

// Commit commits all charts from
// charts.Path/{charts.Chart.Metadata.Name} and
// charts.Path/crds/{charts.CrdChart.Metadata.Name}
func (g *Client) Commit(charts *packager.HelmizedManifests) error {
	wt, err := g.Repository.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	chartPath := fmt.Sprintf("%s/%s", charts.Path, charts.Chart.Metadata.Name)
	crdsChartPath := fmt.Sprintf("%s/%s", charts.Path, charts.CrdChart.Metadata.Name)

	err = g.unstage(wt, chartPath, crdsChartPath)
	if err != nil {
		return fmt.Errorf("failed to unstage files irrelevant to: %s, due to: %v", charts.Path, err)
	}

	// Add all chart files
	_, err = wt.Add(chartPath)
	if err != nil {
		return fmt.Errorf("failed to add chart %s: %w", chartPath, err)
	}
	headRef, _ := g.Repository.Head()
	common.Log.Infof("Added chart files from path: %s (current branch: %s)", chartPath, headRef.Name().Short())

	// Add all CRD chart files
	if charts.CrdChart != nil {
		_, err = wt.Add(crdsChartPath)
		if err != nil {
			return fmt.Errorf("failed to add CRD chart %s: %w", crdsChartPath, err)
		}
		common.Log.Infof("Added crd-chart files from path: %s (current branch: %s)", crdsChartPath, headRef.Name().Short())
	}

	_, err = wt.Commit(
		fmt.Sprintf("Automated update to version: %s", charts.AppVersion()),
		&gogit.CommitOptions{
			Author: &object.Signature{
				Name:  "charter-bot",
				Email: "stanislaw.dev@gmail.com",
				When:  time.Now(),
			},
		})
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	g.status(wt)

	return nil
}

// Push publishes the branch to the remote named "origin"
func (g *Client) Push(ctx context.Context, prSettings *common.PullRequest, branch string) error {
	refName := gogitplumbing.NewBranchReferenceName(branch)

	// Ensure local branch exists
	if _, err := g.Repository.Reference(refName, true); err != nil {
		common.Log.Errorf("Branch %s does not exist locally: %v", branch, err)
		return err
	}

	pushOptions := &gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("%s:%s", refName.String(), refName.String())),
		},
	}

	if !g.usesSsh {
		common.Log.Infof("Using HTTPS authentication for git operations")
		pushOptions.Auth = &http.BasicAuth{
			Username: "github-actions[bot]",
			Password: prSettings.AuthToken,
		}
	}

	err := g.Repository.PushContext(ctx, pushOptions)
	if err != nil {
		if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
			common.Log.Infof("Branch %s already up-to-date on remote", branch)
			return nil
		}
		common.Log.Errorf("Failed to push branch %s: %v", branch, err)
		return err
	}

	common.Log.Infof("Pushed branch: %s", branch)
	return nil
}

func (g *Client) unstage(wt *gogit.Worktree, chartPath, crdsChartPath string) error {
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}
	unstageFiles := make([]string, 0)
	for filePath, status := range status {
		if strings.HasPrefix(filePath, chartPath) || strings.HasPrefix(filePath, crdsChartPath) {
			_, err = wt.Add(filePath)
			if err != nil {
				return fmt.Errorf("failed to add file %s: %w", filePath, err)
			}
		} else if status.Staging == gogit.Modified || status.Staging == gogit.Deleted || status.Worktree == gogit.Added || status.Worktree == gogit.Renamed {
			unstageFiles = append(unstageFiles, filePath)
		}
	}
	if len(unstageFiles) > 0 {
		common.Log.Debugf("Files to unstage: %v", unstageFiles)
		restoreOpts := &gogit.RestoreOptions{
			Files:  unstageFiles,
			Staged: true, // always unstage
		}
		if err := wt.Restore(restoreOpts); err != nil {
			return fmt.Errorf("failed to restore: %w", err)
		}
		common.Log.Debugf("Restored non-chart files")
	}
	return nil
}

func (g *Client) status(wt *gogit.Worktree) {
	status, err := wt.Status()
	if err != nil {
		common.Log.Debugf("failed to get status: %v", err)
		return
	}
	headRef, _ := g.Repository.Head()
	common.Log.Debugf("Branch: %s status:\n%s", headRef.Name().Short(), status)
}
