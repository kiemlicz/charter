package packager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/kiemlicz/charter/internal/common"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/lint"
	"helm.sh/helm/v3/pkg/registry"
)

// HelmizedManifests holds the Helm chart and its path created from Kubernetes manifests.
type HelmizedManifests struct {
	Path     string
	Chart    *chart.Chart
	CrdChart *chart.Chart
}

func (packaged *HelmizedManifests) AppVersion() string {
	return packaged.Chart.Metadata.AppVersion
}

func createTemplates(ch *chart.Chart, newManifests *[]map[string]any) error {
	common.Log.Debugf("Updating: %d Helm Chart manifests in: %s", len(*newManifests), ch.Metadata.Name)
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

	ch.Templates = make([]*chart.File, 0, len(templates))
	for _, tmpl := range templates {
		ch.Templates = append(ch.Templates, tmpl)
	}

	return nil
}

func updateChartManifest(ch *chart.Chart, appVersion string) error {
	v, err := semver.NewVersion(appVersion)
	if err != nil {
		return fmt.Errorf("invalid appVersion (must also follow SemVer): %s, %w", appVersion, err)
	}
	ch.Metadata.AppVersion = appVersion
	ch.Metadata.Version = v.String()
	ch.Metadata.Description = fmt.Sprintf("A Helm Chart for %s", ch.Metadata.Name)
	return nil
}

func save(chartFullPath string, ch *chart.Chart, extraValues *map[string]any) error {
	err := clearTemplates(chartFullPath)
	if err != nil {
		common.Log.Errorf("Failed to clear templates directory: %v", err)
		return err
	}

	dir := filepath.Dir(chartFullPath)
	common.Log.Infof("Saving Helm chart to: %s", dir)
	err = chartutil.SaveDir(ch, dir)
	if err != nil {
		common.Log.Errorf("Failed to save Helm chart to %s: %v", dir, err)
		return err
	}

	//clear generated values
	ch.Values = map[string]any{}
	err = os.Remove(fmt.Sprintf("%s/%s", chartFullPath, chartutil.ValuesfileName))
	if err != nil {
		return err
	}

	// saving values separately as SaveDir doesn't respect the current ch.Values
	mergedValues, err := chartutil.CoalesceValues(ch, *extraValues)
	if err != nil {
		common.Log.Errorf("Failed to merge values: %v", err)
		return err
	}
	ch.Values = mergedValues
	valuesPath := fmt.Sprintf("%s/%s", chartFullPath, chartutil.ValuesfileName)
	var valuesData []byte

	if len(ch.Values) > 0 {
		valuesData, err = yaml.Marshal(ch.Values)
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

func Lint(chartFullPath string, ch *chart.Chart, settings *common.HelmSettings) error {
	k8sVersionString := settings.LintK8s
	lintNamespace := "lint-namespace"
	lintK8sVersion, err := chartutil.ParseKubeVersion(k8sVersionString)
	if err != nil {
		common.Log.Warnf("Invalid Kubernetes version for linting: %s, defaulting to 1.30.0", k8sVersionString)
		k8sVersionString = "1.30.0"
		lintK8sVersion, _ = chartutil.ParseKubeVersion(k8sVersionString)
	}
	common.Log.Infof("Linting Helm chart in: %s against Kubernetes version: %s", chartFullPath, k8sVersionString)
	linter := lint.AllWithKubeVersion(chartFullPath, ch.Values, lintNamespace, lintK8sVersion)

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
		return fmt.Errorf("chart %s has linting errors", chartFullPath)
	}

	return nil
}

func Package(chartPath string, settings *common.HelmSettings) (string, error) {
	if err := os.MkdirAll(settings.TargetDir, 0755); err != nil {
		common.Log.Errorf("failed to create target directory: %v", err)
		return "", err
	}

	client := action.NewPackage()
	client.Destination = settings.TargetDir

	common.Log.Infof("Packaging chart %s", chartPath)
	packagePath, err := client.Run(chartPath, nil)
	if err != nil {
		common.Log.Errorf("failed to package chart: %v", err)
		return "", err
	}

	common.Log.Infof("Successfully packaged chart to %s", packagePath)
	return packagePath, nil
}

func Push(packagedPath, remote string) (string, error) {
	if !strings.HasPrefix(remote, "oci://") {
		return "", fmt.Errorf("remote must start with oci://, got: %s", remote)
	}
	if fi, err := os.Stat(packagedPath); err != nil || fi.IsDir() {
		return "", fmt.Errorf("invalid packaged chart path: %s", packagedPath)
	}

	chartData, err := os.ReadFile(packagedPath)
	if err != nil {
		common.Log.Errorf("failed to read packaged chart %s: %v", packagedPath, err)
		return "", err
	}
	ch, err := loader.LoadFile(packagedPath)
	if err != nil {
		common.Log.Errorf("failed to load packaged chart %s: %v", packagedPath, err)
		return "", err
	}

	rc, err := registry.NewClient(
		registry.ClientOptEnableCache(true),
	)
	if err != nil {
		common.Log.Errorf("failed to create registry client: %v", err)
		return "", err
	}

	trimmed := strings.TrimSuffix(remote, "/")
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	chartName := ch.Metadata.Name

	var ref string
	if last == chartName {
		ref = fmt.Sprintf("%s:%s", trimmed, ch.Metadata.Version)
	} else {
		ref = fmt.Sprintf("%s/%s:%s", trimmed, chartName, ch.Metadata.Version)
	}

	common.Log.Infof("Pushing chart %s version %s to %s", chartName, ch.Metadata.Version, ref)

	result, err := rc.Push(chartData, ref)
	if err != nil {
		common.Log.Errorf("failed to push chart: %v", err)
		return "", err
	}

	if result.Ref != ref {
		common.Log.Warnf("Pushed chart reference %s does not match expected %s", result.Ref, ref)
		return result.Ref, nil
	} else {
		common.Log.Infof("Successfully pushed chart to %s", ref)
	}

	return ref, nil
}

func clearTemplates(path string) error {
	templatesDir := fmt.Sprintf("%s/templates", path)
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

func NewHelmCharts(helmSettings *common.HelmSettings, chartName string, m *common.Manifests) (*HelmizedManifests, error) {
	var crdsChart *chart.Chart
	var err error
	if m.ContainsCrds() {
		crdsChartName := fmt.Sprintf("%s-crds", chartName)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(m.Crds), crdsChartName)
		crdsChart, err = NewHelmChart(crdsChartName, &m.Crds, new(map[string]any), m.Version, helmSettings)
		if err != nil {
			return nil, err
		}
	}
	mainChart, err := NewHelmChart(chartName, &m.Manifests, &m.Values, m.Version, helmSettings)
	if err != nil {
		return nil, err
	}

	createdChart := &HelmizedManifests{
		Path:     helmSettings.SrcDir,
		Chart:    mainChart,
		CrdChart: crdsChart,
	}

	return createdChart, nil
}

func NewHelmChart(chartName string, templates *[]map[string]any, vals *map[string]any, appVersion string, helmSettings *common.HelmSettings) (*chart.Chart, error) {
	chartPath, err := chartutil.Create(chartName, helmSettings.SrcDir) //overwrites
	if err != nil {
		common.Log.Errorf("Failed to create Helm chart in %s: %v", helmSettings.SrcDir, err)
		return nil, err
	}
	common.Log.Infof("Created Helm chart: %s", chartPath)
	chartObj, err := loader.Load(chartPath)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", chartPath, err)
		return nil, err
	}

	err = createTemplates(chartObj, templates)
	if err != nil {
		return nil, err
	}

	err = updateChartManifest(chartObj, appVersion)
	if err != nil {
		return nil, err
	}

	err = save(chartPath, chartObj, vals)
	if err != nil {
		return nil, err
	}

	err = Lint(chartPath, chartObj, helmSettings)
	if err != nil {
		return nil, err
	}

	return chartObj, nil
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
