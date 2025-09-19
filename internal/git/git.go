package git

import (
	"fmt"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
)

type Client struct {
	Repository *gogit.Repository
}

func NewClient(repoPath string) (*Client, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		common.Log.Errorf("Failed to open git repo at %s: %v", repoPath, err)
		return nil, err
	}
	return &Client{
		Repository: repo,
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
		common.Log.Errorf("Failed to create branch: %v", err)
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
			common.Log.Errorf("Failed to checkout branch: worktree contains unstaged changes in files: %v", files)
		} else {
			common.Log.Errorf("Failed to checkout branch: %v", err)
		}
		return err
	}

	common.Log.Infof("Created and checked out branch: %s", branchName)
	return nil
}

// Commit commits all charts from charts.Path/{charts.Chart.Metadata.Name} and charts.Path/crds/{charts.CrdChart.Metadata.Name}
func (g *Client) Commit(charts *packager.HelmizedManifests) error {
	wt, err := g.Repository.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Add all chart files
	chartPath := fmt.Sprintf("%s/%s", charts.Path, charts.Chart.Metadata.Name)
	_, err = wt.Add(chartPath)
	if err != nil {
		return fmt.Errorf("failed to add chart %s: %w", chartPath, err)
	}

	// Add all CRD chart files
	if charts.CrdChart != nil {
		crdPath := fmt.Sprintf("%s/%s", charts.Path, charts.CrdChart.Metadata.Name)
		_, err = wt.Add(crdPath)
		if err != nil {
			return fmt.Errorf("failed to add CRD chart %s: %w", crdPath, err)
		}
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

	return nil
}

// Push pushes
func (g *Client) Push(branch string) error {
	// fixme test existing logic first then finish this method
	//refName := gogitplumbing.NewBranchReferenceName(branch)
	//err := g.Repository.Push(&gogit.PushOptions{
	//	RemoteName: "origin",
	//	RefSpecs: []gogitplumbing.RefSpec{
	//		gogitplumbing.RefSpec(fmt.Sprintf("%s:%s", refName.String(), refName.String())),
	//	},
	//})
	//if err != nil && err != gogit.NoErrAlreadyUpToDate {
	//	common.Log.Errorf("Failed to push branch %s: %v", branch, err)
	//	return err
	//}
	common.Log.Infof("Pushed branch: %s", branch)
	return nil
}
