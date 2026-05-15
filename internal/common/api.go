package common

import (
	"context"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/chart"
)

const (
	ValuesRegex                 = `\{\{\s*\.Values\.([^\s\}]+).*?\}\}`
	Kind                        = "kind"
	ModeUpdate  ModeOfOperation = "update"
	ModePublish ModeOfOperation = "publish"
)

// SourceType discriminates ManifestSource implementations in config.
type SourceType string

const (
	SourceTypeGithub    SourceType = "github"
	SourceTypeHelmChart SourceType = "helmChart"
)

// ManifestSource is the extension point for new upstream manifest origins.
// Each implementation is responsible for its own version-staleness check:
// returning (nil, nil) signals the existing chart is already up to date.
// Implementations must be safe to call concurrently from separate goroutines;
// a single Fetch invocation need not be re-entrant.
type ManifestSource interface {
	// Fetch returns raw manifests for chart generation, or (nil, nil) if already current.
	Fetch(ctx context.Context, currentVersion, currentAppVersion string) (*Manifests, error)
	// ChartName is the name of the Helm chart this source produces.
	ChartName() string
	// HelmOps returns the transformation and metadata settings for chart generation.
	HelmOps() *HelmOps
}

// GithubSourceConfig holds GitHub-specific source parameters.
type GithubSourceConfig struct {
	Owner  string   `koanf:"owner"`
	Repo   string   `koanf:"repo"`
	Assets []string `koanf:"assets"`
}

// HelmChartSourceConfig holds parameters for extracting CRDs from an existing Helm chart.
// The crds/ sub-directory of the chart at SrcDir is used as the manifest origin.
type HelmChartSourceConfig struct {
	SrcDir string `koanf:"srcDir"`
}

// SourceSpec is the tagged-union config entry for a single manifest source.
type SourceSpec struct {
	Type      SourceType             `koanf:"type"`
	Helm      HelmOps                `koanf:"helm"`
	Github    *GithubSourceConfig    `koanf:"github"`
	HelmChart *HelmChartSourceConfig `koanf:"helmChart"`
}

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

	// Sources is the list of manifest origins.
	Sources []SourceSpec `koanf:"sources"`
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

type HelmOps struct {
	ChartName     string         `koanf:"chartName"`
	Drop          []string       `koanf:"drop"`
	Modifications []Modification `koanf:"modifications"`
	AddValues     map[string]any `koanf:"addValues"`
	AddCrdValues  map[string]any `koanf:"addCrdValues"`
}

type Modification struct {
	Expression     string   `koanf:"expression"`     // yq expression to modify manifest, when using TextRegex this is a regex replacement expression
	TextRegex      string   `koanf:"textRegex"`      // regex to change the keys under path
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
		TextRegex:      "",
		ValuesSelector: []string{},
		Kind:           "",
		Reject:         "",
	}
}

type ChartData struct {
	Name       string
	Version    semver.Version
	AppVersion string
	Templates  []*chart.File
	Values     map[string]any
}
