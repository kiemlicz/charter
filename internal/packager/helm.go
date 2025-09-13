package packager

import (
	"fmt"
	"os"
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

func (hc *HelmChart) AppVersion() string {
	return hc.chart.AppVersion()
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

func (hc *HelmChart) updateVersions(appVersion string, crds bool) error {
	v, err := semver.NewVersion(appVersion)
	if err != nil {
		return fmt.Errorf("invalid appVersion (must also follow SemVer): %s, %w", appVersion, err)
	}
	if !crds {
		hc.chart.Metadata.AppVersion = appVersion
	}
	hc.chart.Metadata.Version = v.String()

	return nil
}

func (hc *HelmChart) save(vals *map[string]any) error {
	err := hc.clearTemplates()
	if err != nil {
		common.Log.Errorf("Failed to clear templates directory: %v", err)
		return err
	}

	dir := strings.SplitN(hc.path, "/", 2)[0]
	common.Log.Infof("Saving Helm chart to: %s", dir)
	err = chartutil.SaveDir(hc.chart, dir)
	if err != nil {
		common.Log.Errorf("Failed to save Helm chart to %s: %v", dir, err)
		return err
	}

	// saving values separately as SaveDir doesn't respect the current hc.chart.Values
	mergedValues, err := chartutil.CoalesceValues(hc.chart, *vals)
	if err != nil {
		common.Log.Errorf("Failed to merge values: %v", err)
		return err
	}
	hc.chart.Values = mergedValues
	if len(hc.chart.Values) > 0 {
		valuesData, err := yaml.Marshal(hc.chart.Values)
		if err != nil {
			common.Log.Errorf("failed to marshal values: %v", err)
			return err
		}
		valuesPath := fmt.Sprintf("%s/%s", hc.path, chartutil.ValuesfileName)
		if err := os.WriteFile(valuesPath, valuesData, 0644); err != nil {
			common.Log.Errorf("failed to write values.yaml: %v", err)
			return err
		}
	}

	return nil
}

func (hc *HelmChart) Lint() error {
	k8sVersionString := "1.30.0"
	lintNamespace := "lint-namespace"
	lintK8sVersion := chartutil.KubeVersion{
		Version: k8sVersionString,
		Major:   "1",
		Minor:   "30",
	}
	common.Log.Infof("Linting Helm chart in: %s against Kubernetes version: %s", hc.path, k8sVersionString)
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

func NewHelmChart(path string, m *common.Manifests, values *map[string]any, crds bool) (*HelmChart, error) {
	common.Log.Infof("Loading Helm chart from: %s", path)
	chartObj, err := loader.Load(path)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", path, err)
		return nil, err
	}

	c := &HelmChart{
		path:  path,
		chart: chartObj,
	}

	if crds {
		err = c.createTemplates(&m.Crds)
	} else {
		err = c.createTemplates(&m.Manifests)
	}
	if err != nil {
		return nil, err
	}
	err = c.updateVersions(m.Version, crds)
	if err != nil {
		return nil, err
	}

	err = c.save(values)
	if err != nil {
		return nil, err
	}
	err = c.Lint()
	if err != nil {
		return nil, err
	}
	err = c.Package()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func PeekAppVersion(path string) (string, error) {
	chartObj, err := loader.Load(path)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", path, err)
		return "", err
	}
	return chartObj.AppVersion(), nil
}
