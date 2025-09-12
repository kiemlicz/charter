package common

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/mikefarah/yq/v4/pkg/yqlib"
)

const (
	ValuesRegex = `\{\{\s*\.Values\.([^\s\}]+)\s*\}\}`
	Kind        = "kind"
)

var (
	ValuesRegexCompiled = regexp.MustCompile(ValuesRegex)
)

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	Releases []GithubRelease `mapstructure:"githubReleases"`
}

type GithubRelease struct {
	Owner         string         `mapstructure:"owner"`
	Repo          string         `mapstructure:"repo"`
	Assets        []string       `mapstructure:"assets"`
	HelmChart     string         `mapstructure:"chart"`
	Drop          []string       `mapstructure:"drop"`
	Modifications []Modification `mapstructure:"modifications"`
	Replacements  string         `mapstructure:"replacements"` // must be kept as a string for yaml unmarshalling into kustomize replacements...
}

type Modification struct {
	Expression     string `mapstructure:"expression"`
	ValuesSelector string `mapstructure:"valuesSelector"`
	Kind           string `mapstructure:"kind"` // if set, apply modification only to resources of this kind
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
			if kind, ok := m["kind"].(string); ok && strings.HasPrefix(kind, "CustomResourceDefinition") {
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

// fixme - continue here - add support for kind filtering and value extraction
func (m Modification) Modify(encoder yqlib.Encoder, manifest *map[string]any, nodes ...*yqlib.CandidateNode) (*bytes.Buffer, error) {
	out := new(bytes.Buffer)

	result, err := yqlib.NewAllAtOnceEvaluator().EvaluateNodes(m.Expression, nodes...)
	if err != nil {
		Log.Errorf("Failed to apply expression '%s' on manifest: %v", m.Expression, err)
		return nil, err
	}
	printer := yqlib.NewPrinter(encoder, yqlib.NewSinglePrinterWriter(out))
	if err := printer.PrintResults(result); err != nil {
		Log.Errorf("Failed to print results for expression '%s': %v", m.Expression, err)
		return nil, err
	}
	return out, nil
}
