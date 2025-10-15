package packager

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kiemlicz/charter/internal/common"
	ghup "github.com/kiemlicz/charter/internal/updater/github"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
)

var (
	ChartModifier = newModifier()
)

type modifier struct {
	encoder   yqlib.Encoder
	decoder   yqlib.Decoder
	evaluator yqlib.Evaluator
}

func newModifier() *modifier {
	encoder := yqlib.NewYamlEncoder(yqlib.NewDefaultYamlPreferences())
	decoder := yqlib.NewYamlDecoder(yqlib.NewDefaultYamlPreferences())
	evaluator := yqlib.NewAllAtOnceEvaluator()

	return &modifier{
		encoder:   encoder,
		decoder:   decoder,
		evaluator: evaluator,
	}
}

func (m *modifier) decode(yamlBytes *[]byte) (*yqlib.CandidateNode, error) {
	err := m.decoder.Init(bytes.NewReader(*yamlBytes))
	if err != nil {
		common.Log.Errorf("Failed to initialize decoder for manifest: %v", err)
		return nil, err
	}
	candidNode, err := m.decoder.Decode()
	if err != nil {
		common.Log.Errorf("Failed to decode manifest to yaml node: %v", err)
		return nil, err
	}
	return candidNode, nil
}

func (m *modifier) FilterManifests(manifests *common.Manifests, denyKindFilter []string) *common.Manifests {
	filteredManifests := make([]map[string]any, 0)
	deniedKinds := make(map[string]bool)
	for _, filter := range denyKindFilter {
		deniedKinds[strings.ToLower(filter)] = true
	}

	for _, m := range (*manifests).Manifests {
		if kind, ok := m["kind"].(string); ok && deniedKinds[strings.ToLower(kind)] {
			continue
		}
		filteredManifests = append(filteredManifests, m)
	}

	return &common.Manifests{
		Crds:       manifests.Crds,
		Manifests:  filteredManifests,
		Version:    manifests.Version,
		AppVersion: manifests.AppVersion,
		Values:     manifests.Values,
		CrdsValues: manifests.CrdsValues,
	}
}

// ParametrizeManifests applies modifications to manifests
// returns modified manifests and extracted values
func (m *modifier) ParametrizeManifests(manifests *common.Manifests, mods *[]common.Modification) (*common.Manifests, error) {
	modifiedManifests := make([]map[string]any, 0)
	modifiedCrds := make([]map[string]any, 0)
	extractedValues := manifests.Values
	extractedCrdValues := manifests.CrdsValues

	for _, manifest := range manifests.Manifests {
		modifiedManifest, v, err := m.applyModifications(&manifest, mods)
		if err != nil {
			return nil, err //not continuing on error
		}
		modifiedManifests = append(modifiedManifests, *modifiedManifest)
		extractedValues = *common.DeepMerge(&extractedValues, v)
	}

	for _, crd := range manifests.Crds {
		m, v, err := m.applyModifications(&crd, mods)
		if err != nil {
			return nil, err //not continuing on error
		}
		modifiedCrds = append(modifiedCrds, *m)
		extractedCrdValues = *common.DeepMerge(&extractedCrdValues, v)
	}

	return &common.Manifests{
		Crds:       modifiedCrds,
		Manifests:  modifiedManifests,
		Version:    manifests.Version,
		AppVersion: manifests.AppVersion,
		Values:     extractedValues,
		CrdsValues: extractedCrdValues,
	}, nil
}

func (m *modifier) applyModifications(manifest *map[string]any, mods *[]common.Modification) (*map[string]any, *map[string]any, error) {
	common.Log.Debugf("Applying %d modifications to manifest of kind: %v", len(*mods), (*manifest)[common.Kind])
	common.Log.Tracef("Original manifest:\n%+v", manifest)

	modifiedManifest := *manifest
	extractedValues := make(map[string]any)

	yamlBytes, err := yaml.Marshal(manifest)
	if err != nil {
		common.Log.Errorf("Failed to marshal manifest to YAML during applying modifications: %v", err)
		return nil, nil, err
	}
	candidNode, err := m.decode(&yamlBytes)

	for _, mod := range *mods {
		if mod.Kind != "" {
			rc, err := regexp.Compile(mod.Kind)
			if err != nil {
				common.Log.Errorf("Failed to compile kind regex '%s': %v", mod.Kind, err)
				return nil, nil, err
			}
			kind, ok := (*manifest)[common.Kind].(string)
			if !ok || !rc.MatchString(kind) {
				continue
			}
		}

		if mod.Reject != "" {
			kind, ok := (*manifest)[common.Kind].(string)
			rc, err := regexp.Compile(mod.Reject)
			if err != nil {
				common.Log.Errorf("Failed to compile reject regex '%s': %v", mod.Kind, err)
				return nil, nil, err
			}
			if ok && rc.MatchString(kind) {
				common.Log.Debugf("Omitting manifest of kind '%s' due to reject rule", kind)
				continue
			}
		}

		if mod.ValuesSelector != nil {
			matches := common.ValuesRegexCompiled.FindAllStringSubmatch(mod.Expression, -1)
			for i, sel := range mod.ValuesSelector {
				vals, err := m.evaluator.EvaluateNodes(sel, candidNode)
				if err != nil {
					common.Log.Errorf("Failed to apply values selector '%s' on manifest: %v", mod.ValuesSelector, err)
					return nil, nil, err
				}

				if len(matches) >= 1 {
					valuesMap, err := m.wrapResult(vals, matches[i][1])
					if err != nil {
						return nil, nil, err
					}
					extractedValues = *common.DeepMerge(&extractedValues, valuesMap)
				} else {
					err = fmt.Errorf("no value path found in expression '%s'", mod.Expression)
					return nil, nil, err
				}
			}
		}

		result, err := m.evaluator.EvaluateNodes(mod.Expression, candidNode)
		if err != nil {
			common.Log.Errorf("Failed to apply expression '%s' on manifest: %v", mod.Expression, err)
			return nil, nil, err
		}

		resultManifest, err := m.resultToMap(result)
		if err != nil {
			return nil, nil, err
		}
		modifiedManifest = *resultManifest
	}
	common.Log.Tracef("Modified manifest:\n%+v", modifiedManifest)
	common.Log.Tracef("Extracted values:\n%+v", extractedValues)
	return &modifiedManifest, &extractedValues, nil
}

func (m *modifier) wrapResult(result *list.List, underPath string) (*map[string]any, error) {
	if result.Len() != 1 {
		return nil, fmt.Errorf("yq result does not contain exactly one element")
	}

	// Decode the (single) result node into a Go value
	v, err := m.resultToAny(result)
	if err != nil {
		common.Log.Errorf("Cannot decode valuesSelector result: %v", err)
		return nil, err
	}

	if v == nil {
		return new(map[string]any), nil // empty map for nil values
	}

	// If it is already a map keep it, otherwise treat as scalar (or slice) and wrap
	var e any = v
	path := strings.Split(underPath, ".")
	for i := len(path) - 1; i >= 0; i-- {
		e = map[string]any{path[i]: e}
	}

	mapVal, ok := e.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("wrapped value is not a map[string]any")
	}
	return &mapVal, nil
}

// helper: generic unmarshal of a single yq result element into interface{}
func (m *modifier) resultToAny(result *list.List) (any, error) {
	return decodeResult[any](m, result)
}

func (m *modifier) resultToMap(result *list.List) (*map[string]any, error) {
	return decodeResult[*map[string]any](m, result)
}

func (m *modifier) InsertHelpers(template *chart.File) error {
	candidNode, err := m.decode(&template.Data)
	if err != nil {
		common.Log.Errorf("Failed to decode helpers template: %v", err)
		return err
	}
	//todo cont here
}

func ProcessManifests(ctx context.Context, releaseConfig *common.GithubRelease, helmSettings *common.HelmSettings) (*common.Manifests, error) {
	common.Log.Infof("Updating release: %s", releaseConfig.Repo)

	currentVersion, currentAppVersion, err := PeekVersions(helmSettings.SrcDir, releaseConfig.ChartName)
	if err != nil {
		common.Log.Errorf("Failed to get app version from Helm chart %s: %v", releaseConfig.ChartName, err)
		return nil, err
	}
	manifests, err := ghup.FetchManifests(ctx, releaseConfig, currentVersion, currentAppVersion)
	if err != nil {
		return nil, err
	}
	if manifests == nil {
		common.Log.Infof("No updates for release %s, skipping", releaseConfig.Repo)
		return nil, nil
	}

	common.Log.Infof("Creating or updating Helm chart %s with %d manifests", releaseConfig.ChartName, len(manifests.Manifests))

	modifiedManifests, err := ChartModifier.ParametrizeManifests(
		ChartModifier.FilterManifests(
			manifests,
			releaseConfig.Drop,
		),
		&releaseConfig.Modifications,
	)
	if err != nil {
		return nil, err
	}

	return modifiedManifests, nil
}

// generic decoder
func decodeResult[T any](m *modifier, result *list.List) (T, error) {
	var zero T
	out := new(bytes.Buffer)
	printer := yqlib.NewPrinter(m.encoder, yqlib.NewSinglePrinterWriter(out))
	if err := printer.PrintResults(result); err != nil {
		return zero, err
	}
	var v T
	if err := yaml.Unmarshal(out.Bytes(), &v); err != nil {
		return zero, err
	}
	return v, nil
}
