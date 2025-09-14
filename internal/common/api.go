package common

import (
	"regexp"
	"strings"
)

const (
	ValuesRegex = `\{\{\s*\.Values\.([^\s\}]+).*\}\}`
	Kind        = "kind"
)

var (
	ValuesRegexCompiled = regexp.MustCompile(ValuesRegex)
)

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	Helm HelmSettings `mapstructure:"helm"`

	Releases []GithubRelease `mapstructure:"githubReleases"`
}

type HelmSettings struct {
	Dir     string `mapstructure:"dir"`
	LintK8s string `mapstructure:"lintK8s"`
}

type GithubRelease struct {
	Owner         string         `mapstructure:"owner"`
	Repo          string         `mapstructure:"repo"`
	Assets        []string       `mapstructure:"assets"`
	ChartName     string         `mapstructure:"chartName"`
	Drop          []string       `mapstructure:"drop"`
	Modifications []Modification `mapstructure:"modifications"`
	Replacements  string         `mapstructure:"replacements"` // must be kept as a string for yaml unmarshalling into kustomize replacements...
}

type Modification struct {
	Expression     string `mapstructure:"expression"`
	ValuesSelector string `mapstructure:"valuesSelector"`
	Kind           string `mapstructure:"kind"` // if set, apply modification only to resources of this kind
	Reject         string `mapstructure:"reject"`
}

type Manifests struct {
	Crds      []map[string]any
	Manifests []map[string]any
	Version   string
}

func (m Manifests) ContainsCrds() bool {
	return len(m.Crds) > 0
}

func NewManifests(assetsData *map[string][]byte, version string) (*Manifests, error) {
	crds := make([]map[string]any, 0)
	manifests := make([]map[string]any, 0)

	for assetName, assetData := range *assetsData {
		maps, err := ExtractYamls(assetData)
		if err != nil {
			Log.Errorf("Failed to extract YAML from asset %s: %v", assetName, err)
			return nil, err
		}
		for _, m := range *maps {
			if kind, ok := m[Kind].(string); ok && strings.HasPrefix(kind, "CustomResourceDefinition") {
				crds = append(crds, m)
			} else {
				manifests = append(manifests, m)
			}
		}
	}

	Log.Debugf("Total manifests extracted: %d", len(manifests))
	return &Manifests{
		Crds:      crds,
		Manifests: manifests,
		Version:   version,
	}, nil
}

func NewYqModification(expression string) *Modification {
	return &Modification{
		Expression:     expression,
		ValuesSelector: "",
		Kind:           "",
	}
}
