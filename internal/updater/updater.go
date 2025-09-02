package updater

import (
	"bytes"
	"context"
	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"io"
	"net/http"
	"regexp"
	"sigs.k8s.io/kustomize/api/filters/replacement"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
	"strings"
)

const (
	ReplacePath  = "path"
	ReplaceValue = "value"
)

func DownloadReleaseMeta(ctx context.Context, client *github.Client, release *common.Release) (*github.RepositoryRelease, error) {
	repoRelease, response, err := client.Repositories.GetLatestRelease(ctx, release.Owner, release.Repo)
	if err != nil || response.StatusCode != http.StatusOK {
		common.Log.Errorf("Failed to download release: %v", err)
		return nil, err
	}

	return repoRelease, nil
}

func DownloadReleaseAsset(ctx context.Context, client *github.Client, release *common.Release, asset *github.ReleaseAsset) ([]byte, error) {
	reader, _, err := client.Repositories.DownloadReleaseAsset(ctx, release.Owner, release.Repo, asset.GetID(), client.Client())
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

func ExtractYamlFromAsset(assetData []byte) (*[]map[string]interface{}, error) {
	reader := bytes.NewReader(assetData)
	decoder := yaml.NewDecoder(reader)

	var documents []map[string]interface{}
	for {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			common.Log.Errorf("Failed to decode YAML document for asset: %v", err)
			return nil, err
		}
		documents = append(documents, doc)
	}

	common.Log.Infof("Successfully unmarshalled %d documents", len(documents))
	return &documents, nil
}

func DownloadAssets(ctx context.Context, client *github.Client, releaseConfig *common.Release, releaseData *github.RepositoryRelease) (*map[string][]byte, error) {
	assetsData := make(map[string][]byte)
	for _, asset := range releaseConfig.Assets {
		assetsData[asset] = []byte{}
	}

	for _, asset := range releaseData.Assets {
		if _, ok := assetsData[asset.GetName()]; ok {
			data, err := DownloadReleaseAsset(ctx, client, releaseConfig, asset)
			if err != nil {
				common.Log.Errorf("Failed to download asset %s for release %s: %v", asset.GetName(), releaseConfig.Repo, err)
				return nil, err
			}
			common.Log.Infof("Downloaded asset %s for release %s, size: %d bytes", asset.GetName(), releaseConfig.Repo, len(data))

			assetsData[asset.GetName()] = data
		}
	}
	common.Log.Infof("Total assets downloaded for release %s: %d", releaseConfig.Repo, len(assetsData))
	return &assetsData, nil
}

func ParseAssets(assetsData *map[string][]byte) (*[]*map[string]interface{}, *[]*map[string]interface{}, error) {
	crds := make([]*map[string]interface{}, 0)
	manifests := make([]*map[string]interface{}, 0)

	for assetName, assetData := range *assetsData {
		maps, err := ExtractYamlFromAsset(assetData)
		if err != nil {
			common.Log.Errorf("Failed to extract YAML from asset %s: %v", assetName, err)
			return nil, nil, err
		}
		for _, m := range *maps {
			if kind, ok := m["kind"].(string); ok && strings.HasPrefix(kind, "CustomResourceDefinition") {
				crds = append(crds, &m)
			} else {
				manifests = append(manifests, &m)
			}
		}
	}

	common.Log.Debugf("Total manifests extracted: %d", len(manifests))
	return &manifests, &crds, nil
}

func FilterManifests(manifests *[]*map[string]interface{}, denyKindFilter []string) *[]*map[string]interface{} {
	filteredManifests := make([]*map[string]interface{}, 0)
	deniedKinds := make(map[string]bool)
	for _, filter := range denyKindFilter {
		deniedKinds[strings.ToLower(filter)] = true
	}

	for _, m := range *manifests {
		if kind, ok := (*m)["kind"].(string); ok && deniedKinds[strings.ToLower(kind)] {
			continue
		}
		filteredManifests = append(filteredManifests, m)
	}

	return &filteredManifests
}

func find(manifests *map[string]any, s *types.Selector) *[]*map[string]any {
	selected := make([]*map[string]any, 0)

	return &selected
}

// extractOriginalValues for all replacements from return key-values that got moved to values.yaml
// values are nested on extracted .Values expression
func extractOriginalValues(manifests *[]*map[string]any, replacements *[]types.Replacement) map[string]any {
	valuesRegex := regexp.MustCompile(`\{\{.*\.Values\..+\}\}`)

	selector := func(sel *types.Selector) {
		//sel.Kind
		//for _, m := range *manifests {
		//TODO
		//m[]
		//}
	}

	for _, repl := range *replacements {
		sv := repl.SourceValue
		if sv != nil && *sv != "" && valuesRegex.MatchString(*sv) {
			//replaced for values
			common.Log.Infof("Replacing value to %s", *sv)

			for _, ts := range repl.Targets {
				selector(ts.Select)

			}
		}
	}

	return nil //todo
}

func Parametrize(manifests *[]*map[string]any, replacements *string) (*[]*map[string]any, *map[string]any, error) {
	// TODO think how to unmarshall it along with Config, without representing replacements as a string to yaml.unmarshal here...
	// problem is with yaml tags not respected by Viper
	var r []types.Replacement
	err := yaml.Unmarshal([]byte(*replacements), &r)
	if err != nil {
		common.Log.Errorf("Failed to unmarshal replacement filter: %v", err)
		return nil, nil, err
	}
	filter := replacement.Filter{
		Replacements: r,
	}
	// Convert manifests to []*yaml.RNode
	var nodes []*yaml.RNode
	for _, m := range *manifests {
		n, err := yaml.FromMap(*m)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, n)
	}

	// Apply the filter
	result, err := filter.Filter(nodes)
	if err != nil {
		common.Log.Errorf("Failure applying kustomization: %v", err)
		return nil, nil, err
	}

	values := extractOriginalValues(manifests, &r)

	// Convert back to []*map[string]interface{}
	var out []*map[string]interface{}
	for _, n := range result {
		m, err := n.Map()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, &m)
	}

	return &out, &values, nil
}
