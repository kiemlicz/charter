// Package chart provides a ManifestSource that extracts CRDs from an existing
// Helm chart's crds/ directory and packages them into a separate chart.
package chart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	"github.com/kiemlicz/charter/internal/common"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// HelmChartSource implements common.ManifestSource by reading CRD YAML files
// from the crds/ sub-directory of an existing Helm chart.
// The resulting Manifests contain only the CRD documents as regular manifests
// (not in the Crds split), so that Prepare creates a single flat CRD chart
// using the helmOps.ChartName (e.g. "kubevirt-crds").
type HelmChartSource struct {
	cfg  *common.HelmChartSourceConfig
	helm *common.HelmOps
}

// NewHelmChartSource constructs a HelmChartSource.
func NewHelmChartSource(cfg *common.HelmChartSourceConfig, helm *common.HelmOps) *HelmChartSource {
	return &HelmChartSource{cfg: cfg, helm: helm}
}

func (s *HelmChartSource) ChartName() string        { return s.helm.ChartName }
func (s *HelmChartSource) HelmOps() *common.HelmOps { return s.helm }

// Fetch loads CRD manifests from the source chart's crds/ directory.
// Returns (nil, nil) when the existing generated chart already matches the
// source chart's AppVersion (or Version when AppVersion is empty).
func (s *HelmChartSource) Fetch(_ context.Context, _, existingAppVersion string) (*common.Manifests, error) {
	srcChart, err := loader.Load(s.cfg.SrcDir)
	if err != nil {
		return nil, fmt.Errorf("helmChartSource: failed to load source chart at %s: %w", s.cfg.SrcDir, err)
	}

	// Use AppVersion as the upstream identity string; fall back to Version.
	remoteAppVersion := srcChart.Metadata.AppVersion
	if remoteAppVersion == "" {
		remoteAppVersion = srcChart.Metadata.Version
	}
	if existingAppVersion == remoteAppVersion {
		common.Log.Infof("HelmChart source %s is already at version %s, skipping", s.helm.ChartName, remoteAppVersion)
		return nil, nil
	}

	chartVersion, err := semver.NewVersion(srcChart.Metadata.Version)
	if err != nil {
		return nil, fmt.Errorf("helmChartSource: source chart version %q is not valid SemVer: %w", srcChart.Metadata.Version, err)
	}

	crdsDir := filepath.Join(s.cfg.SrcDir, "crds")
	entries, err := os.ReadDir(crdsDir)
	if err != nil {
		return nil, fmt.Errorf("helmChartSource: cannot read crds/ directory %s: %w", crdsDir, err)
	}

	manifests := make([]map[string]any, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ext := filepath.Ext(name); ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(crdsDir, name))
		if err != nil {
			return nil, fmt.Errorf("helmChartSource: failed to read %s: %w", name, err)
		}
		docs, err := common.ExtractYamls(data)
		if err != nil {
			return nil, fmt.Errorf("helmChartSource: failed to parse %s: %w", name, err)
		}
		manifests = append(manifests, *docs...)
	}
	common.Log.Infof("HelmChart source %s: read %d CRD documents from %s", s.helm.ChartName, len(manifests), crdsDir)

	addValues := s.helm.AddValues
	if addValues == nil {
		addValues = map[string]any{}
	}

	return &common.Manifests{
		// Place docs in Manifests (not Crds) so Prepare builds them as
		// regular templates in the named chart without an additional split.
		Manifests:  manifests,
		Crds:       []map[string]any{},
		Version:    *chartVersion,
		AppVersion: remoteAppVersion,
		Values:     addValues,
		CrdsValues: map[string]any{},
	}, nil
}
