package common

import (
	"regexp"
	"strings"
)

const (
	ValuesRegex                 = `\{\{\s*\.Values\.([^\s\}]+).*\}\}`
	Kind                        = "kind"
	ModeUpdate  ModeOfOperation = "update"
	ModePublish ModeOfOperation = "publish"
)

var (
	ValuesRegexCompiled = regexp.MustCompile(ValuesRegex)
)

type ModeOfOperation string

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	ModeOfOperation ModeOfOperation `mapstructure:"mode"`

	PullRequest PullRequest `mapstructure:"pr"`

	Helm HelmSettings `mapstructure:"helm"`

	Releases []GithubRelease `mapstructure:"githubReleases"`
}

type PullRequest struct {
	DefaultBranch string `mapstructure:"defaultBranch"`
	Title         string `mapstructure:"title"`
	Body          string `mapstructure:"body"`
	Repo          string `mapstructure:"repo"`
	Owner         string `mapstructure:"owner"`
	AuthToken     string `mapstructure:"authToken"`
}

type HelmSettings struct {
	SrcDir    string `mapstructure:"srcDir"`
	TargetDir string `mapstructure:"targetDir"`
	LintK8s   string `mapstructure:"lintK8s"`
	Remote    string `mapstructure:"remote"`
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
	Values    map[string]any
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

	Log.Debugf("Manifests extracted: %d, CRDs: %d", len(manifests), len(crds))
	return &Manifests{
		Crds:      crds,
		Manifests: manifests,
		Version:   version,
		Values:    make(map[string]any),
	}, nil
}

func NewYqModification(expression string) *Modification {
	return &Modification{
		Expression:     expression,
		ValuesSelector: "",
		Kind:           "",
	}
}
