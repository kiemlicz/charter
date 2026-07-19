package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/packager"
	"helm.sh/helm/v3/pkg/chart"
)

func TestMain(m *testing.M) {
	common.Setup("debug")
	os.Exit(m.Run())
}

const (
	kubevirtOriginal = "apiVersion: v2\nname: kubevirt\nappVersion: v1.8.3\nversion: 1.8.3\n"
	kubevirtBumped   = "apiVersion: v2\nname: kubevirt\nappVersion: v1.8.4\nversion: 1.8.4\n"
	gatewayOriginal  = "apiVersion: v2\nname: gateway-api\nappVersion: v1.6.0\nversion: 1.6.0\n"
	gatewayBumped    = "apiVersion: v2\nname: gateway-api\nappVersion: v1.6.1\nversion: 1.6.1\n"
)

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", relPath, err)
	}
}

func initTestRepo(t *testing.T) (*gogit.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	writeFile(t, dir, "charts/kubevirt/Chart.yaml", kubevirtOriginal)
	writeFile(t, dir, "charts/gateway-api/Chart.yaml", gatewayOriginal)

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	if _, err := wt.Add("."); err != nil {
		t.Fatalf("failed to stage initial files: %v", err)
	}
	_, err = wt.Commit("initial", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to create initial commit: %v", err)
	}

	return repo, dir
}

func fileContentsAtHead(t *testing.T, repo *gogit.Repository, path string) string {
	t.Helper()
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("failed to get HEAD: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("failed to get commit: %v", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("failed to get tree: %v", err)
	}
	f, err := tree.File(path)
	if err != nil {
		t.Fatalf("failed to find file %s in tree: %v", path, err)
	}
	contents, err := f.Contents()
	if err != nil {
		t.Fatalf("failed to read contents of %s: %v", path, err)
	}
	return contents
}

// TestCommit_ScopesToOwnChartPath_WhenSourceHasNoCrdChart reproduces the bug where a source
// with no separate CRD chart (CrdChart == nil, e.g. separateCrds: false) ends up committing
// unrelated changes from other charts that happen to be modified on disk in the same working
// directory (as occurs when update mode fetches multiple sources in one run).
func TestCommit_ScopesToOwnChartPath_WhenSourceHasNoCrdChart(t *testing.T) {
	//given
	repo, dir := initTestRepo(t)
	client := &Client{Repository: repo}

	// Simulate concurrent Phase 1 writes: kubevirt was bumped by a different source's
	// goroutine in the same working directory, while gateway-api is the source actually
	// being committed here.
	writeFile(t, dir, "charts/kubevirt/Chart.yaml", kubevirtBumped)
	writeFile(t, dir, "charts/gateway-api/Chart.yaml", gatewayBumped)

	gatewayApiManifests := &packager.HelmizedManifests{
		Path: "charts",
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{Name: "gateway-api", AppVersion: "v1.6.1"},
		},
		CrdChart: nil, // gateway-api has separateCrds: false
	}

	//when
	err := client.Commit(gatewayApiManifests)

	//then
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if got := fileContentsAtHead(t, repo, "charts/gateway-api/Chart.yaml"); got != gatewayBumped {
		t.Errorf("gateway-api/Chart.yaml = %q, want %q (own chart should be committed)", got, gatewayBumped)
	}

	if got := fileContentsAtHead(t, repo, "charts/kubevirt/Chart.yaml"); got != kubevirtOriginal {
		t.Errorf("kubevirt/Chart.yaml = %q, want %q (unrelated chart must not be committed alongside gateway-api)", got, kubevirtOriginal)
	}
}
