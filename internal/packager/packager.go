package packager

import (
	"bytes"
	"container/list"
	"fmt"
	"regexp"
	"strings"

	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
	"gopkg.in/yaml.v3"
)

var (
	ChartModifier = NewModifier()
)

type Modifier struct {
	encoder   yqlib.Encoder
	decoder   yqlib.Decoder
	evaluator yqlib.Evaluator
	out       *bytes.Buffer
	printer   yqlib.Printer
}

func NewModifier() *Modifier {
	out := new(bytes.Buffer)
	encoder := yqlib.NewYamlEncoder(yqlib.NewDefaultYamlPreferences())
	decoder := yqlib.NewYamlDecoder(yqlib.NewDefaultYamlPreferences())
	evaluator := yqlib.NewAllAtOnceEvaluator()
	printer := yqlib.NewPrinter(encoder, yqlib.NewSinglePrinterWriter(out))

	return &Modifier{
		encoder:   encoder,
		decoder:   decoder,
		evaluator: evaluator,
		out:       out,
		printer:   printer,
	}
}

func (m *Modifier) FilterManifests(manifests *common.Manifests, denyKindFilter []string) *common.Manifests {
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
		Crds:      manifests.Crds,
		Manifests: filteredManifests,
		Version:   manifests.Version,
	}
}

// ParametrizeManifests applies modifications to manifests
// returns modified manifests and extracted values
func (m *Modifier) ParametrizeManifests(manifests *common.Manifests, mods *[]common.Modification) (*common.Manifests, *map[string]any, error) {
	modifiedManifests := make([]map[string]any, 0)
	extractedValues := make(map[string]any)

	for _, manifest := range manifests.Manifests {
		m, v, err := m.applyModifications(&manifest, mods)
		if err != nil {
			return nil, nil, err //not continuing on error
		}
		modifiedManifests = append(modifiedManifests, *m)
		extractedValues = *common.DeepMerge(&extractedValues, v)
	}

	return &common.Manifests{
		Crds:      manifests.Crds,
		Manifests: modifiedManifests,
		Version:   manifests.Version,
	}, &extractedValues, nil
}

func (m *Modifier) applyModifications(manifest *map[string]any, mods *[]common.Modification) (*map[string]any, *map[string]any, error) {
	common.Log.Debugf("Applying %d modifications to manifest of kind: %v", len(*mods), (*manifest)[common.Kind])

	modifiedManifest := *manifest
	extractedValues := make(map[string]any)

	yamlBytes, err := yaml.Marshal(manifest)
	if err != nil {
		common.Log.Errorf("Failed to marshal manifest to YAML during applying modifications: %v", err)
		return nil, nil, err
	}
	err = m.decoder.Init(bytes.NewReader(yamlBytes))
	if err != nil {
		common.Log.Errorf("Failed to initialize decoder for manifest: %v", err)
		return nil, nil, err
	}
	candidNode, err := m.decoder.Decode()
	if err != nil {
		common.Log.Errorf("Failed to decode manifest to yaml node: %v", err)
		return nil, nil, err
	}

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

		if mod.ValuesSelector != "" {
			vals, err := m.evaluator.EvaluateNodes(mod.ValuesSelector, candidNode)
			if err != nil {
				common.Log.Errorf("Failed to apply values selector '%s' on manifest: %v", mod.ValuesSelector, err)
				return nil, nil, err
			}
			matches := common.ValuesRegexCompiled.FindStringSubmatch(mod.Expression)
			if len(matches) > 1 { // matches[0] is the full match, matches[1] is the first capturing group
				valsMap, err := m.resultToMap(vals)
				if err != nil {
					return nil, nil, err
				}
				path := strings.Split(matches[1], ".")
				for i := len(path) - 1; i >= 0; i-- {
					*valsMap = map[string]any{path[i]: *valsMap}
				}

				extractedValues = *common.DeepMerge(&extractedValues, valsMap)
			} else {
				err = fmt.Errorf("no value path found in expression '%s'", mod.Expression)
				return nil, nil, err
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
	return &modifiedManifest, &extractedValues, nil
}

func (m *Modifier) resultToMap(result *list.List) (*map[string]any, error) {
	m.out.Reset()
	var modifiedManifest map[string]any
	if err := m.printer.PrintResults(result); err != nil {
		common.Log.Errorf("Failed to print results for expression: %v", err)
		return nil, err
	}
	if err := yaml.Unmarshal(m.out.Bytes(), &modifiedManifest); err != nil {
		common.Log.Errorf("Failed to unmarshal modified YAML: %v", err)
		return nil, err
	}

	return &modifiedManifest, nil
}
