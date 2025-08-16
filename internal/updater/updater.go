package updater

import (
	"bytes"
	"context"
	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"io"
	"k8s.io/client-go/util/jsonpath"
	"net/http"
	"reflect"
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

func DownloadManifests(ctx context.Context, client *github.Client, releaseConfig *common.Release, releaseData *github.RepositoryRelease) (*[]*map[string]interface{}, *[]*map[string]interface{}, error) {
	crds := make([]*map[string]interface{}, 0)
	manifests := make([]*map[string]interface{}, 0)
	assetSet := make(map[string]bool)
	for _, asset := range releaseConfig.Assets {
		assetSet[asset] = true
	}

	for _, asset := range releaseData.Assets {
		if assetSet[asset.GetName()] {
			data, err := DownloadReleaseAsset(ctx, client, releaseConfig, asset)
			if err != nil {
				common.Log.Errorf("Failed to download asset %s for release %s: %v", asset.GetName(), releaseConfig.Repo, err)
				return nil, nil, err
			}
			common.Log.Infof("Downloaded asset %s for release %s, size: %d bytes", asset.GetName(), releaseConfig.Repo, len(data))

			maps, err := ExtractYamlFromAsset(data)
			if err != nil {
				common.Log.Errorf("Failed to extract YAML from asset %s: %v", asset.GetName(), err)
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
	}
	common.Log.Infof("Total manifests extracted for release %s: %d", releaseConfig.Repo, len(manifests))
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

//func UpdateNamespace(manifests *[]*map[string]interface{}) *[]*map[string]interface{} {
//	//remove, only use parametrize
//	//fixme - need something way more clever to update not only medata.namespace but nested namespaces like clusterRoleBinding
//	for _, m := range *manifests {
//		metadata, ok := (*m)["metadata"].(map[string]interface{})
//		if ok {
//			if _, nsOk := metadata["namespace"].(string); nsOk {
//				metadata["namespace"] = Namespace
//			}
//		}
//	}
//	return manifests
//}

func Parametrize(manifests *[]*map[string]interface{}, replacements *[]map[string]string) (*[]*map[string]interface{}, error) {
	jsonpathReplacer := jsonpath.New("replacer")
	for _, replacement := range *replacements {
		err := jsonpathReplacer.Parse(replacement[ReplacePath])
		if err != nil {
			common.Log.Errorf("Failed to parse JSONPath replacement: %s, %v", replacement, err)
			continue
		}
		path := replacement[ReplacePath]
		newValue := replacement[ReplaceValue]
		for _, m := range *manifests {
			results, err := jsonpathReplacer.FindResults(m)
			if err != nil {
				common.Log.Warnf("Failed to apply JSONPath replacement %s on manifest: %v", replacement[ReplacePath], err)
				continue // wut?
				//return nil, err
			}

			//fixme minimize this and debug separately cont here
			for _, result := range results {
				for _, r := range result {
					if r.CanSet() {
						common.Log.Debugf("Applying replacement for path: %s, new value: %s", path, newValue)
						r.Set(reflect.ValueOf(newValue))
					} else {
						common.Log.Warnf("Cannot set value for path: %s", path)
					}
				}
			}

			//if len(results) > 0 && len(results[0]) > 0 {
			//	common.Log.Debugf("Applying replacement from: %s, to %s on manifest", results[0][0].Interface(), replacement[ReplaceValue])
			//
			//	//how to access the value and set it?!
			//
			//	if results[0][0].CanSet() {
			//		results[0][0].Set(reflect.ValueOf(replacement[ReplaceValue]))
			//	} else {
			//		common.Log.Errorf("Cannot set value for path: %s", replacement[ReplacePath])
			//		return nil, fmt.Errorf("cannot set value for path: %s", replacement[ReplacePath])
			//	}
			//
			//}
		}
	}

	return manifests, nil
}
