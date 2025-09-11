package updater

import (
	"bytes"
	"context"
	"io"
	"log"
	"maps"
	"net/http"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
	"sigs.k8s.io/kustomize/kyaml/yaml"
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

// Parametrize applies modifications to manifests
// returns modified manifests and extracted values
func Parametrize(manifests *[]*map[string]any, mods *[]common.Modification) (*[]*map[string]any, *map[string]any, error) {
	modifiedManifests := make([]*map[string]any, 0)
	extractedValues := make(map[string]any)

	for _, manifest := range *manifests {
		m, v, err := applyModifications(manifest, mods)
		if err != nil {
			common.Log.Errorf("Failed to apply modifications to manifest: %v", err)
			return nil, nil, err //FIXME continue on error?
		}
		modifiedManifests = append(modifiedManifests, m)
		for k, val := range *v {
			extractedValues[k] = val // TODO use merge
		}
	}

	return &modifiedManifests, &extractedValues, nil
}

func applyModifications(manifest *map[string]any, mods *[]common.Modification) (*map[string]any, *map[string]any, error) {
	modifiedManifest := maps.Clone(*manifest)
	extractedValues := make(map[string]any)

	//valuesRegex := regexp.MustCompile(`\{\{.*\.Values\..+\}\}`)

	encoder := yqlib.NewYamlEncoder(yqlib.NewDefaultYamlPreferences())
	out := new(bytes.Buffer)
	yamlBytes, _ := yaml.Marshal(modifiedManifest)
	decoder := yqlib.NewYamlDecoder(yqlib.NewDefaultYamlPreferences())
	err := decoder.Init(bytes.NewReader(yamlBytes))
	if err != nil {
		common.Log.Errorf("Failed to initialize decoder for manifest: %v", err)
		return nil, nil, err
	}
	candidNode, err := decoder.Decode()
	if err != nil {
		common.Log.Errorf("Failed to decode manifest to yaml node: %v", err)
		return nil, nil, err
	}

	for _, mod := range *mods {
		//if valuesRegex.MatchString(mod) {
		//	// extract value
		//}

		result, err := yqlib.NewAllAtOnceEvaluator().EvaluateNodes(mod.Expression, candidNode)
		if err != nil {
			common.Log.Errorf("Failed to evaluate manifest '%s': %v", mod, err)
			return nil, nil, err
		}
		printer := yqlib.NewPrinter(encoder, yqlib.NewSinglePrinterWriter(out))
		if err := printer.PrintResults(result); err != nil {
			log.Fatal(err)
		}
		var modMap map[string]any //fixme once validated, write directly to modifiedManifest
		if err := yaml.Unmarshal(out.Bytes(), &modMap); err != nil {
			common.Log.Errorf("Failed to unmarshal modified YAML: %v", err)
			return nil, nil, err
		}
		modifiedManifest = modMap
	}
	return &modifiedManifest, &extractedValues, nil
}
