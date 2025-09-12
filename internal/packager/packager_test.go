package packager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
)

func TestMain(m *testing.M) {
	common.Setup("debug")
	exitVal := m.Run()
	os.Exit(exitVal)
}

func TestParseAssets(t *testing.T) {
	assetsData := readTestData(t)
	t.Run("ParseAssets", func(t *testing.T) {
		manifests, err := common.NewManifests(assetsData, "0.0.1")
		if err != nil {
			t.Errorf("ParseAssets() error = %v", err)
			return
		}
		if len((*manifests).Manifests) != 10 {
			t.Errorf("ParseAssets() manifests = %v, want 10", len((*manifests).Manifests))
		}
		if len((*manifests).Crds) != 1 {
			t.Errorf("ParseAssets() crds = %v, want 1", len((*manifests).Crds))
		}
	})
}

func TestParametrizeExtractsValues(t *testing.T) {
	testManifests, _ := common.NewManifests(readTestData(t), "0.0.1")
	testCases := map[string]struct {
		modifications   []common.Modification
		expectedValues  map[string]any
		expectedChanges map[string]any
	}{
		"empty": {
			modifications:   []common.Modification{},
			expectedValues:  map[string]any{},
			expectedChanges: map[string]any{},
		},
		"no_values": {
			modifications: []common.Modification{
				*common.NewYqModification(".metadata.namespace |= \"{{ .Release.Namespace }}\""),
			},
			expectedValues: map[string]any{}, // none expected as no value extraction
			expectedChanges: map[string]any{
				"metadata": map[string]any{
					"namespace": "{{ .Release.Namespace }}",
				},
			},
		},
		"simple_values": {
			modifications: []common.Modification{
				*common.NewYqModification(".metadata.namespace |= \"{{ .Release.Namespace }}\""),
				{
					Expression:     ".spec.configuration |= \"{{ .Values.kubevirt.configuration }}\"",
					ValuesSelector: "developerConfiguration.featureGates",
					Kind:           "kubevirt",
				},
			},
			expectedValues: map[string]any{
				"kubevirt": map[string]any{
					"configuration": map[string]any{
						"developerConfiguration": map[string]any{
							"featureGates": []any{},
						},
					},
				},
			}, // none expected as no value extraction
			expectedChanges: map[string]any{
				"metadata": map[string]any{
					"namespace": "{{ .Release.Namespace }}",
				},
				"spec": map[string]any{
					"configuration": "{{ .Values.kubevirt.configuration }}",
				},
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			//given

			//when
			modifiedManifests, extractedValues, err := ChartModifier.ParametrizeManifests(testManifests, &tc.modifications)

			//then
			if err != nil {
				t.Errorf("ParametrizeManifests() error = %v", err)
				return
			}

			for _, m := range (*modifiedManifests).Manifests {
				if !mapContains(&m, &tc.expectedChanges, false) {
					t.Errorf("ParametrizeManifests() modified manifest = %v, want changes = %v", mustYaml(m), mustYaml(tc.expectedChanges))
					return
				}
			}
			common.Log.Infof("Extracted Values:\n%v\n", mustYaml(extractedValues))

			if !mapContains(extractedValues, &tc.expectedValues, true) {
				t.Errorf("ParametrizeManifests() extractedValues = %v, want = %v", *extractedValues, tc.expectedValues)
				return
			}
		})
	}
}

func mapContains(mainMap *map[string]any, subMap *map[string]any, mustExist bool) bool {
	for k, v := range *subMap {
		mainValue, exists := (*mainMap)[k]
		if !exists {
			return !mustExist
		}

		switch subValueTyped := v.(type) {
		case map[string]any:
			mainValueTyped, ok := mainValue.(map[string]any)
			if !ok || !mapContains(&mainValueTyped, &subValueTyped, mustExist) {
				return false
			}
		case []any:
			mainValueTyped, ok := mainValue.([]any)
			if !ok || !reflect.DeepEqual(mainValueTyped, subValueTyped) {
				return false
			}
		default:
			if mainValue != v {
				return false
			}
		}
	}
	return true
}

func readTestData(t *testing.T) *map[string][]byte {
	testdata := make(map[string][]byte)
	dir := filepath.Join("testdata", "manifests")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read testdata dir: %v", err)
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatalf("failed to read file %s: %v", file.Name(), err)
		}
		testdata[file.Name()] = data
	}

	return &testdata
}

func mustYaml(v any) string {
	data, err := yaml.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}
