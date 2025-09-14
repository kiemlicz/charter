package packager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/lint"
)

type HelmChart struct {
	path  string
	chart *chart.Chart
}

func (hc *HelmChart) createTemplates(newManifests *[]map[string]any) error {
	common.Log.Debugf("Updating: %d Helm Chart manifests in: %s", len(*newManifests), hc.path)
	templates := make(map[string]*chart.File, len(*newManifests))
	re := regexp.MustCompile(`'(\{\{.*?\}\})'|"(\{\{.*?\}\})"`)

	for i, manifest := range *newManifests {
		manifestYAML, err := yaml.Marshal(manifest)
		if err != nil {
			common.Log.Errorf("Failed to marshal manifest %d: %v", i, err)
			return err
		}
		manifestYAML = re.ReplaceAllFunc(manifestYAML, func(match []byte) []byte {
			// Remove the surrounding quotes that break the Helm template syntax
			return match[1 : len(match)-1]
		})
		kind, ok := manifest["kind"].(string)
		if !ok {
			common.Log.Errorf("Broken manifest: %s", string(manifestYAML))
			return fmt.Errorf("manifest %d does not have a valid 'kind' field", i)
		}

		if existingTemplate, exists := templates[kind]; exists {
			newData := append(existingTemplate.Data, []byte("\n---\n")...)
			newData = append(newData, manifestYAML...)
			existingTemplate.Data = newData
		} else {
			templates[kind] = &chart.File{
				Name: fmt.Sprintf("templates/%s.yaml", strings.ToLower(kind)),
				Data: manifestYAML,
			}
		}
	}

	hc.chart.Templates = make([]*chart.File, 0, len(templates))
	for _, tmpl := range templates {
		hc.chart.Templates = append(hc.chart.Templates, tmpl)
	}

	return nil
}

func (hc *HelmChart) updateChartManifest(appVersion string, crds bool) error {
	v, err := semver.NewVersion(appVersion)
	if err != nil {
		return fmt.Errorf("invalid appVersion (must also follow SemVer): %s, %w", appVersion, err)
	}
	if !crds {
		hc.chart.Metadata.AppVersion = appVersion
	}
	hc.chart.Metadata.Version = v.String()
	hc.chart.Metadata.Description = fmt.Sprintf("A Helm Chart for %s", hc.chart.Metadata.Name)

	return nil
}

func (hc *HelmChart) save(vals *map[string]any) error {
	err := hc.clearTemplates()
	if err != nil {
		common.Log.Errorf("Failed to clear templates directory: %v", err)
		return err
	}

	dir := filepath.Dir(hc.path)
	common.Log.Infof("Saving Helm chart to: %s", dir)
	err = chartutil.SaveDir(hc.chart, dir)
	if err != nil {
		common.Log.Errorf("Failed to save Helm chart to %s: %v", dir, err)
		return err
	}

	//clear generated values
	hc.chart.Values = map[string]any{}
	err = os.Remove(fmt.Sprintf("%s/%s", hc.path, chartutil.ValuesfileName))
	if err != nil {
		return err
	}

	// saving values separately as SaveDir doesn't respect the current hc.chart.Values
	mergedValues, err := chartutil.CoalesceValues(hc.chart, *vals)
	if err != nil {
		common.Log.Errorf("Failed to merge values: %v", err)
		return err
	}
	hc.chart.Values = mergedValues
	valuesPath := fmt.Sprintf("%s/%s", hc.path, chartutil.ValuesfileName)
	var valuesData []byte

	if len(hc.chart.Values) > 0 {
		valuesData, err = yaml.Marshal(hc.chart.Values)
		if err != nil {
			common.Log.Errorf("failed to marshal values: %v", err)
			return err
		}
	}

	if err := os.WriteFile(valuesPath, valuesData, 0644); err != nil {
		common.Log.Errorf("failed to write values.yaml: %v", err)
		return err
	}

	return nil
}

func (hc *HelmChart) Lint(settings *common.HelmSettings) error {
	k8sVersionString := settings.LintK8s
	lintNamespace := "lint-namespace"
	lintK8sVersion, err := chartutil.ParseKubeVersion(k8sVersionString)
	if err != nil {
		common.Log.Warnf("Invalid Kubernetes version for linting: %s, defaulting to 1.30.0", k8sVersionString)
		k8sVersionString = "1.30.0"
		lintK8sVersion, _ = chartutil.ParseKubeVersion(k8sVersionString)
	}
	common.Log.Infof("Linting Helm chart in: %s against Kubernetes version: %s", hc.path, k8sVersionString)
	linter := lint.AllWithKubeVersion(hc.path, hc.chart.Values, lintNamespace, lintK8sVersion)

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

func (hc *HelmChart) clearTemplates() error {
	templatesDir := fmt.Sprintf("%s/templates", hc.path)
	files, err := os.ReadDir(templatesDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".tpl") {
			continue
		}
		err := os.RemoveAll(fmt.Sprintf("%s/%s", templatesDir, file.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

func NewHelmChart(helmSettings *common.HelmSettings, chartName string, m *common.Manifests, values *map[string]any, isCrdsOnly bool) (*HelmChart, error) {
	chartPath, err := chartutil.Create(chartName, helmSettings.Dir) //overwrites
	if err != nil {
		common.Log.Errorf("Failed to create Helm chart in %s: %v", helmSettings.Dir, err)
		return nil, err
	}
	common.Log.Infof("Created Helm chart: %s", chartPath)
	chartObj, err := loader.Load(chartPath)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", chartPath, err)
		return nil, err
	}

	createdChart := &HelmChart{
		path:  chartPath,
		chart: chartObj,
	}

	if isCrdsOnly {
		err = createdChart.createTemplates(&m.Crds)
	} else {
		err = createdChart.createTemplates(&m.Manifests)
	}
	if err != nil {
		return nil, err
	}

	err = createdChart.updateChartManifest(m.Version, isCrdsOnly)
	if err != nil {
		return nil, err
	}

	err = createdChart.save(values)
	if err != nil {
		return nil, err
	}

	err = createdChart.Lint(helmSettings)
	if err != nil {
		return nil, err
	}

	return createdChart, nil
}

func PeekAppVersion(chartDir, chartName string) (string, error) {
	path := fmt.Sprintf("%s/%s", chartDir, chartName)
	chartObj, err := loader.Load(path)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", path, err)
		return "", err
	}
	return chartObj.AppVersion(), nil
}
