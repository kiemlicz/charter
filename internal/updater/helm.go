package updater

import (
	"fmt"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
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

	hc.chart.Files = templates
	return nil
}

func (hc *HelmChart) Save() error {
	dir := strings.SplitN(hc.path, "/", 2)[0]
	common.Log.Infof("Saving Helm chart to: %s", dir)
	return chartutil.SaveDir(hc.chart, dir)
}

//func (hc *HelmChart) Lint() error {
//	common.Log.Infof("Linting Helm chart in: %s", hc.path)
//
//	lint.AllWithKubeVersion(hc.path, ???, )
//
//}
