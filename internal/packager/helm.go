package packager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kiemlicz/charter/internal/common"
	"github.com/kiemlicz/charter/internal/updater/github"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/lint"
	"helm.sh/helm/v3/pkg/registry"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

var ErrVersionExists = errors.New("chart version already exists in registry")

// HelmizedManifests holds the Helm chart and its path created from Kubernetes manifests.
type HelmizedManifests struct {
	Path     string
	Chart    *chart.Chart
	CrdChart *chart.Chart
}

func (packaged *HelmizedManifests) AppVersion() string {
	return packaged.Chart.Metadata.AppVersion
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

	var ref string // oci://registry/repository:version
	if last == chartName {
		ref = fmt.Sprintf("%s:%s", trimmed, ch.Metadata.Version)
	} else {
		ref = fmt.Sprintf("%s/%s:%s", trimmed, chartName, ch.Metadata.Version)
	}

	exists, err := versionExistsInRegistry(rc, ref, ch.Metadata.Version)
	if err != nil {
		common.Log.Errorf("failed to check if version exists in registry: %v", err)
		return "", err
	}
	if exists {
		common.Log.Errorf("version %s of chart %s already exists in the registry %s", ch.Metadata.Version, chartName, ref)
		return "", ErrVersionExists
	}

	common.Log.Infof("Pushing chart %s version %s to %s", chartName, ch.Metadata.Version, ref)

	result, err := rc.Push(chartData, ref)
	if err != nil {
		common.Log.Errorf("failed to push chart: %v", err)
		return "", err
	}

	if fmt.Sprintf("oci://%s", result.Ref) != ref {
		common.Log.Warnf("Pushed chart reference %s does not match expected %s", result.Ref, ref)
		return result.Ref, nil
	}

	common.Log.Infof("Successfully pushed chart to %s", ref)
	return ref, nil
}

func versionExistsInRegistry(rc *registry.Client, ref, version string) (bool, error) {
	tags, err := rc.Tags(strings.TrimPrefix(ref, "oci://"))
	if err != nil && !isNotFound(err) {
		return false, fmt.Errorf("failed to fetch tags: %w", err)
	}
	for _, tag := range tags {
		if tag == version {
			return true, nil
		}
	}
	return false, nil
}

func isNotFound(err error) bool {
	var er *errcode.ErrorResponse
	if errors.As(err, &er) {
		common.Log.Infof("Previous Chart version not found in registry: %s, will create new", er.Error())
		return er.StatusCode == http.StatusNotFound
	}
	return false
}

func clearTemplates(path string) error {
	templatesDir := filepath.Join(path, "templates")
	files, err := os.ReadDir(templatesDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".tpl") {
			continue
		}
		err := os.RemoveAll(filepath.Join(templatesDir, file.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

func FetchAndUpdate(ctx context.Context, release *common.GithubRelease, settings *common.HelmSettings) (*HelmizedManifests, error) {
	common.Log.Infof("Updating release: %s", release.Repo)
	currentVersion, currentAppVersion, err := PeekVersions(settings.SrcDir, release.Helm.ChartName)
	if err != nil {
		common.Log.Errorf("Failed to get app version from Helm chart %s: %v", release.Helm.ChartName, err)
		return nil, err
	}

	manifests, err := github.FetchManifests(ctx, release, currentVersion, currentAppVersion)
	if err != nil {
		return nil, err
	}
	if manifests == nil {
		common.Log.Infof("No updates for release %s, skipping", release.Repo)
		return nil, nil
	}
	return Prepare(manifests, &release.Helm, settings)
}

// Prepare creates a Helm chart by
// applying manifests modifications
// inserting helpers using textRegex clauses
func Prepare(manifests *common.Manifests, helmOps *common.HelmOps, settings *common.HelmSettings) (*HelmizedManifests, error) {
	common.Log.Infof("Creating or updating Helm chart %s with %d manifests", helmOps.ChartName, len(manifests.Manifests))
	modifiedManifests, err := ChartModifier.ParametrizeManifests(
		ChartModifier.FilterManifests(
			manifests,
			helmOps.Drop,
		),
		&helmOps.Modifications,
	)
	if err != nil {
		return nil, err
	}

	version := modifiedManifests.Version
	appVersion := modifiedManifests.AppVersion

	var crdsChart *chart.Chart
	if modifiedManifests.ContainsCrds() {
		crdsChartName := fmt.Sprintf("%s-crds", helmOps.ChartName)
		common.Log.Infof("Moving %d CRDs to dedicated chart %s", len(modifiedManifests.Crds), crdsChartName)
		templates, err := createTemplates(&modifiedManifests.Crds, &helmOps.Modifications)
		if err != nil {
			return nil, err
		}
		crdChartData := common.ChartData{
			Name:       crdsChartName,
			Version:    version,
			AppVersion: appVersion,
			Templates:  templates,
			Values:     modifiedManifests.CrdsValues,
		}
		crdsChart, err = newHelmChart(&crdChartData, settings)
		if err != nil {
			return nil, err
		}
	}

	templates, err := createTemplates(&modifiedManifests.Manifests, &helmOps.Modifications)
	common.Log.Infof("Created %d templates for main chart", len(templates))
	if err != nil {
		return nil, err
	}
	chartData := common.ChartData{
		Name:       helmOps.ChartName,
		Version:    version,
		AppVersion: appVersion,
		Templates:  templates,
		Values:     modifiedManifests.Values,
	}

	mainChart, err := newHelmChart(&chartData, settings)
	if err != nil {
		return nil, err
	}

	createdChart := &HelmizedManifests{
		Path:     settings.SrcDir,
		Chart:    mainChart,
		CrdChart: crdsChart,
	}

	return createdChart, nil
}

func createTemplates(manifests *[]map[string]any, modification *[]common.Modification) ([]*chart.File, error) {
	kindToFile, err := materializeManifests(manifests)
	if err != nil {
		return nil, err
	}
	for kind, file := range kindToFile {
		err = insertHelpers(kind, file, modification)
		if err != nil {
			return nil, err
		}
	}

	templates := make([]*chart.File, 0, len(kindToFile))
	for _, tmpl := range kindToFile {
		templates = append(templates, tmpl)
	}

	return templates, nil
}

func insertHelpers(kind string, template *chart.File, mods *[]common.Modification) error {
	content := string(template.Data)
	for _, mod := range *mods {
		if mod.TextRegex == "" {
			continue
		}
		if mod.Kind != "" {
			kindMatches, err := common.Matches(mod.Kind, kind)
			if err != nil {
				return err
			}
			if !kindMatches {
				continue
			}
		}
		if mod.Reject != "" {
			kindMatches, err := common.Matches(mod.Reject, kind)
			if err != nil {
				return err
			}
			if kindMatches {
				continue
			}
		}
		textRegex := regexp.MustCompile(mod.TextRegex)
		content = textRegex.ReplaceAllString(content, mod.Expression)
	}
	template.Data = []byte(content)
	return nil
}

func newHelmChart(chartData *common.ChartData, helmSettings *common.HelmSettings) (*chart.Chart, error) {
	chartName := chartData.Name
	version := chartData.Version
	appVersion := chartData.AppVersion
	vals := chartData.Values
	templates := chartData.Templates

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

	chartObj.Metadata.AppVersion = appVersion
	chartObj.Metadata.Version = version.String()
	chartObj.Metadata.Description = fmt.Sprintf("A Helm Chart for %s", chartObj.Metadata.Name)
	chartObj.Templates = make([]*chart.File, 0, len(templates))
	for _, tmpl := range templates {
		chartObj.Templates = append(chartObj.Templates, tmpl)
	}

	err = save(chartPath, chartObj, &vals)
	if err != nil {
		return nil, err
	}

	err = Lint(chartPath, chartObj, helmSettings)
	if err != nil {
		return nil, err
	}

	return chartObj, nil
}

func PeekVersions(chartDir, chartName string) (string, string, error) {
	path := fmt.Sprintf("%s/%s", chartDir, chartName)
	chartObj, err := loader.Load(path)
	if err != nil {
		common.Log.Errorf("Failed to load Helm chart from %s: %v", path, err)
		return "", "", err
	}
	return chartObj.Metadata.Version, chartObj.AppVersion(), nil
}

func materializeManifests(newManifests *[]map[string]any) (map[string]*chart.File, error) {
	templates := make(map[string]*chart.File, len(*newManifests))
	re := regexp.MustCompile(`'(\{\{.*?\}\})'|"(\{\{.*?\}\})"`)

	for i, manifest := range *newManifests {
		manifestYAML, err := yaml.Marshal(manifest)
		if err != nil {
			common.Log.Errorf("Failed to marshal manifest %d: %v", i, err)
			return nil, err
		}
		manifestYAML = re.ReplaceAllFunc(manifestYAML, func(match []byte) []byte {
			// Remove the surrounding quotes that break the Helm template syntax
			return match[1 : len(match)-1]
		})
		kind, ok := manifest[common.Kind].(string)
		if !ok {
			common.Log.Errorf("Broken manifest: %s", string(manifestYAML))
			return nil, fmt.Errorf("manifest %d does not have a valid 'kind' field", i)
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

	return templates, nil
}
