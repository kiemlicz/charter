package github

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/charter/internal/common"
)

// GithubSource implements common.ManifestSource backed by a GitHub release.
type GithubSource struct {
	cfg  *common.GithubSourceConfig
	helm *common.HelmOps
}

// NewGithubSource constructs a GithubSource from the typed config blocks.
func NewGithubSource(cfg *common.GithubSourceConfig, helm *common.HelmOps) *GithubSource {
	return &GithubSource{cfg: cfg, helm: helm}
}

func (s *GithubSource) ChartName() string        { return s.helm.ChartName }
func (s *GithubSource) HelmOps() *common.HelmOps { return s.helm }
func (s *GithubSource) Fetch(ctx context.Context, currentVersion, currentAppVersion string) (*common.Manifests, error) {
	return FetchManifests(ctx, s.cfg, s.helm, currentVersion, currentAppVersion)
}

// CreatePr creates a Pull Request into default branch
func CreatePr(ctx context.Context, prSettings *common.PullRequest, srcBranch string) error {
	defaultBranch := prSettings.DefaultBranch

	if defaultBranch == "" {
		return fmt.Errorf("default branch empty")
	}
	if srcBranch == "" {
		return fmt.Errorf("source branch empty")
	}
	if srcBranch == defaultBranch {
		return fmt.Errorf("source branch equals default branch")
	}

	client := github.NewClient(nil).WithAuthToken(prSettings.AuthToken)

	newPR := &github.NewPullRequest{
		Title: github.Ptr(fmt.Sprintf(prSettings.Title, srcBranch)),
		Head:  github.Ptr(srcBranch),
		Base:  github.Ptr(defaultBranch),
		Body:  github.Ptr(prSettings.Body),
	}

	pr, resp, err := client.PullRequests.Create(ctx, prSettings.Owner, prSettings.Repo, newPR)
	if err != nil {
		// 422 often means PR already exists or branch not found
		if resp != nil {
			return fmt.Errorf("failed to create PR: status=%d err=%w", resp.StatusCode, err)
		}
		return fmt.Errorf("failed to create PR: %w", err)
	}

	common.Log.Infof("Created PR #%d: %s", pr.GetNumber(), pr.GetHTMLURL())
	return nil
}

// FetchManifests downloads the latest GitHub release assets and parses them into Manifests.
// Returns (nil, nil) when the chart is already at the latest version.
func FetchManifests(ctx context.Context, cfg *common.GithubSourceConfig, helmOps *common.HelmOps, existingVersion, existingAppVersion string) (*common.Manifests, error) {
	client := github.NewClient(nil)
	releaseData, err := downloadReleaseMeta(ctx, client, cfg.Owner, cfg.Repo)
	if err != nil {
		common.Log.Errorf("Failed to download release metadata for %s: %v", cfg.Repo, err)
		return nil, err
	}
	releaseVersion := releaseData.TagName
	common.Log.Infof("Latest release for %s: %s", cfg.Repo, *releaseVersion)

	if existingAppVersion == *releaseVersion {
		common.Log.Infof("Helm chart %s is already up to date with version %s", helmOps.ChartName, existingAppVersion)
		return nil, nil
	}

	version, err := takeNewerVersion(existingVersion, *releaseVersion)
	if err != nil {
		return nil, fmt.Errorf("version resolution failed for %s: %w", cfg.Repo, err)
	}

	assetsData, err := downloadAssets(ctx, client, cfg, releaseData)
	if err != nil {
		common.Log.Errorf("Failed to download assets for release %s: %v", cfg.Repo, err)
		return nil, err
	}
	manifests, err := common.NewManifests(assetsData, version, *releaseVersion, &helmOps.AddValues, &helmOps.AddCrdValues)
	if err != nil {
		common.Log.Errorf("Failed to collect manifests for release %s: %v", cfg.Repo, err)
		return nil, err
	}
	return manifests, nil
}

func takeNewerVersion(existingVersion, remoteVersion string) (*semver.Version, error) {
	semverExisting, _ := semver.NewVersion(existingVersion)
	semverRemote, err := semver.NewVersion(remoteVersion)
	if err != nil {
		if semverExisting == nil {
			return nil, fmt.Errorf("neither existing version %q nor remote version %q are valid SemVer", existingVersion, remoteVersion)
		}
		common.Log.Warnf("Remote version %s is not valid SemVer: %v, will use existing Chart's version: %s", remoteVersion, err, existingVersion)
		return semverExisting, nil
	}

	if semverRemote.Compare(semverExisting) < 0 {
		return semverExisting, nil
	}
	return semverRemote, nil
}

func downloadReleaseMeta(ctx context.Context, client *github.Client, owner, repo string) (*github.RepositoryRelease, error) {
	repoRelease, response, err := client.Repositories.GetLatestRelease(ctx, owner, repo)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			err = fmt.Errorf("failed to download release: %v, status: %d", err, response.StatusCode)
		}
		return nil, err
	}

	return repoRelease, nil
}

func downloadReleaseAsset(ctx context.Context, client *github.Client, owner string, repo string, asset *github.ReleaseAsset) ([]byte, error) {
	reader, _, err := client.Repositories.DownloadReleaseAsset(ctx, owner, repo, asset.GetID(), client.Client())
	if err != nil {
		common.Log.Errorf("Failed to download release asset: %v", err)
		return nil, err
	}
	defer reader.Close()

	assetData, err := io.ReadAll(reader)
	if err != nil {
		common.Log.Errorf("Failed to read release asset data: %v", err)
		return nil, err
	}

	return assetData, nil
}

func downloadAssets(ctx context.Context, client *github.Client, cfg *common.GithubSourceConfig, releaseData *github.RepositoryRelease) (*map[string][]byte, error) {
	assetsData := make(map[string][]byte)
	for _, asset := range cfg.Assets {
		assetsData[asset] = []byte{}
	}

	for _, asset := range releaseData.Assets {
		if _, ok := assetsData[asset.GetName()]; ok {
			data, err := downloadReleaseAsset(ctx, client, cfg.Owner, cfg.Repo, asset)
			if err != nil {
				common.Log.Errorf("Failed to download asset %s for release %s: %v", asset.GetName(), cfg.Repo, err)
				return nil, err
			}
			common.Log.Infof("Downloaded asset %s for release %s, size: %d bytes", asset.GetName(), cfg.Repo, len(data))

			assetsData[asset.GetName()] = data
		}
	}
	common.Log.Infof("Total assets downloaded for release %s: %d", cfg.Repo, len(assetsData))
	return &assetsData, nil
}
