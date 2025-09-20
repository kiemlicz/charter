package git

import (
	"fmt"
	"strings"
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
			common.Log.Errorf("Failed to checkout branch: %v", err)
		}
		return err
	}

	common.Log.Infof("Switched branch from source: %s to: %s", defaultBranch, branchName)
	g.status(wt)

	return nil
}

// Commit commits all charts from charts.Path/{charts.Chart.Metadata.Name} and charts.Path/crds/{charts.CrdChart.Metadata.Name}
func (g *Client) Commit(charts *packager.HelmizedManifests) error {
	wt, err := g.Repository.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	chartPath := fmt.Sprintf("%s/%s", charts.Path, charts.Chart.Metadata.Name)
	crdsChartPath := fmt.Sprintf("%s/%s", charts.Path, charts.CrdChart.Metadata.Name)

	for filePath, _ := range status {
		if strings.HasPrefix(filePath, chartPath) || strings.HasPrefix(filePath, crdsChartPath) {
			_, err = wt.Add(filePath)
			if err != nil {
				return fmt.Errorf("failed to add file %s: %w", filePath, err)
			}
		} else {
			idx, err := g.Repository.Storer.Index()
			if err != nil {
				return fmt.Errorf("failed to get index: %w", err)
			}

			for i, e := range idx.Entries {
				if e.Name == filePath {
					idx.Entries = append(idx.Entries[:i], idx.Entries[i+1:]...)
					break
				}
			}
			//
			//_, err = wt.Remove(filePath) // doesn't belong to this chart, not tracking them as will be added in next iter
			//if err != nil {
			//	return fmt.Errorf("failed to remove file %s: %w", filePath, err)
			//}
		}
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

func (g *Client) status(wt *gogit.Worktree) {
	status, err := wt.Status()
	if err != nil {
		common.Log.Debugf("failed to get status: %w", err)
		return
	}
	headRef, _ := g.Repository.Head()
	common.Log.Debugf("Branch: %s status:\n%s", headRef.Name().Short(), status)
}
