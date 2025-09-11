package common

import (
	"bytes"

	"github.com/mikefarah/yq/v4/pkg/yqlib"
)

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	Releases []Release `mapstructure:"githubReleases"`
}

type Release struct {
	Owner         string         `mapstructure:"owner"`
	Repo          string         `mapstructure:"repo"`
	Assets        []string       `mapstructure:"assets"`
	HelmChart     string         `mapstructure:"chart"`
	Filter        []string       `mapstructure:"filter"`
	Modifications []Modification `mapstructure:"modifications"`
	Replacements  string         `mapstructure:"replacements"` // must be kept as a string for yaml unmarshalling into kustomize replacements...
}

type Modification struct {
	Expression string `mapstructure:"expression"`
	Value      string `mapstructure:"value"` //value under which attach the extracted keys
	Kind       string `mapstructure:"kind"`  // if set, apply modification only to resources of this kind
}

func NewYqModification(expression string) *Modification {
	return &Modification{
		Expression: expression,
		Value:      "",
		Kind:       "",
	}
}

func (m Modification) Modify(encoder yqlib.Encoder, nodes ...*yqlib.CandidateNode) (*bytes.Buffer, error) {
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
