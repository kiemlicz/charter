package updater

import (
	"fmt"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/lint"
	"os"
	"strings"
)

type HelmChart struct {
	path  string
	chart *chart.Chart
}

func NewHelmChart(path string) (*HelmChart, error) {
	common.Log.Infof("Loading Helm chart from: %s", path)
	chartObj, err := loader.Load(path)
	if err != nil {
		return nil, err
	}
	return &HelmChart{
			path:  path,
			chart: chartObj,
		},
		nil
}

func (hc *HelmChart) AppVersion() string {
	return hc.chart.AppVersion()
}

func (hc *HelmChart) UpdateManifests(newManifests *[]*map[string]interface{}) error {
	common.Log.Debugf("Updating: %d Helm Chart manifests in: %s", len(*newManifests), hc.path)
	templates := make([]*chart.File, len(*newManifests))

	for i, manifest := range *newManifests {
		manifestYAML, err := yaml.Marshal(manifest)
		if err != nil {
			return err
		}
		kind, ok := (*manifest)["kind"].(string)
		if !ok {
			common.Log.Errorf("Broken manifest: %s", string(manifestYAML))
			return fmt.Errorf("manifest %d does not have a valid 'kind' field", i)
		}

		templates[i] = &chart.File{
			Name: fmt.Sprintf("templates/%s-%d.yaml", strings.ToLower(kind), i),
			Data: manifestYAML,
		}
	}

	hc.chart.Templates = templates
	return nil
}

func (hc *HelmChart) clearTemplates() error {
	templatesDir := fmt.Sprintf("%s/templates", hc.path)
	files, err := os.ReadDir(templatesDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		err := os.Remove(fmt.Sprintf("%s/%s", templatesDir, file.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

func (hc *HelmChart) UpdateAppVersion(appVersion string) {
	hc.chart.Metadata.AppVersion = appVersion
}

func (hc *HelmChart) Build() error {
	err := hc.clearTemplates()
	if err != nil {
		common.Log.Errorf("Failed to clear templates directory: %v", err)
		return err
	}
	dir := strings.SplitN(hc.path, "/", 2)[0]
	common.Log.Infof("Saving Helm chart to: %s", dir)
	return chartutil.SaveDir(hc.chart, dir)
}

func (hc *HelmChart) Lint() error {
	k8sVersionString := "1.30.0"
	lintNamespace := "lint-namespace"
	common.Log.Infof("Linting Helm chart in: %s against: %s", hc.path, k8sVersionString)
	lintK8sVersion := chartutil.KubeVersion{
		Version: k8sVersionString,
		Major:   "1",
		Minor:   "30",
	}

	linter := lint.AllWithKubeVersion(hc.path, hc.chart.Values, lintNamespace, &lintK8sVersion)

	if len(linter.Messages) > 0 {
		for _, lintMsg := range linter.Messages {
			if lintMsg.Severity > 1 {
				common.Log.Warnf("%s", lintMsg)
			} else {
				common.Log.Infof("%s", lintMsg)
			}
		}
	}
	if linter.HighestSeverity >= 2 {
		return fmt.Errorf("chart %s has linting errors", hc.path)
	}

	return nil
}

func (hc *HelmChart) Package() error {
	destDir := "target"
	if err := os.MkdirAll(destDir, 0755); err != nil {
		common.Log.Errorf("failed to create target directory: %v", err)
		return err
	}

	client := action.NewPackage()
	client.Destination = destDir

	common.Log.Infof("Packaging chart %s", hc.path)
	path, err := client.Run(hc.path, nil)
	if err != nil {
		common.Log.Errorf("failed to package chart: %v", err)
		return err
	}

	common.Log.Infof("Successfully packaged chart to %s", path)
	return nil
}
