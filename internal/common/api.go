package common

import (
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	ValuesRegex                 = `\{\{\s*\.Values\.([^\s\}]+).*?\}\}`
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
		Level string `koanf:"level"`
	} `koanf:"log"`

	ModeOfOperation ModeOfOperation `koanf:"mode"`
	Offline         bool            `koanf:"offline"`

	PullRequest PullRequest `koanf:"pr"`

	Helm HelmSettings `koanf:"helm"`

	Releases []GithubRelease `koanf:"githubReleases"`
}

type PullRequest struct {
	DefaultBranch string `koanf:"defaultBranch"`
	Title         string `koanf:"title"`
	Body          string `koanf:"body"`
	Repo          string `koanf:"repo"`
	Owner         string `koanf:"owner"`
	AuthToken     string `koanf:"authToken"`
}

type HelmSettings struct {
	SrcDir    string `koanf:"srcDir"`
	TargetDir string `koanf:"targetDir"`
	LintK8s   string `koanf:"lintK8s"`
	Remote    string `koanf:"remote"`
}

type GithubRelease struct {
	Owner         string         `koanf:"owner"`
	Repo          string         `koanf:"repo"`
	Assets        []string       `koanf:"assets"`
	ChartName     string         `koanf:"chartName"`
	Drop          []string       `koanf:"drop"`
	Modifications []Modification `koanf:"modifications"`
	AddValues     map[string]any `koanf:"addValues"`
	AddCrdValues  map[string]any `koanf:"addCrdValues"`
}

type Modification struct {
	Expression     string   `koanf:"expression"`     // yq expression to modify manifest
	ValuesSelector []string `koanf:"valuesSelector"` // cuts selected section and moves to Values
	Kind           string   `koanf:"kind"`           // if set, apply modification only to resources of this kind
	Reject         string   `koanf:"reject"`         // don't apply for these
}

type Manifests struct {
	Crds       []map[string]any
	Manifests  []map[string]any
	Version    semver.Version
	AppVersion string
	Values     map[string]any
	CrdsValues map[string]any
}

func (m Manifests) ContainsCrds() bool {
	return len(m.Crds) > 0
}

func NewManifests(assetsData *map[string][]byte, version *semver.Version, appVersion string, initialValues *map[string]any, initialCrdValues *map[string]any) (*Manifests, error) {
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
		Crds:       crds,
		Manifests:  manifests,
		Version:    *version,
		AppVersion: appVersion,
		Values:     *initialValues,
		CrdsValues: *initialCrdValues,
	}, nil
}

func NewYqModification(expression string) *Modification {
	return &Modification{
		Expression:     expression,
		ValuesSelector: []string{},
		Kind:           "",
	}
}
