package updater

import (
	"bytes"
	"context"
	"github.com/google/go-github/v74/github"
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"strings"
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

func UpdateNamespace(manifests *[]*map[string]interface{}) *[]*map[string]interface{} {
	//fixme - need something way more clever to update not only medata.namespace but nested namespaces like clusterRoleBinding
	for _, m := range *manifests {
		metadata, ok := (*m)["metadata"].(map[string]interface{})
		if ok {
			if _, nsOk := metadata["namespace"].(string); nsOk {
				metadata["namespace"] = Namespace
			}
		}
	}
	return manifests
}

func Parametrize(manifests *[]*map[string]interface{}) *[]*map[string]interface{} {
	// todo move common values
	return manifests
}
