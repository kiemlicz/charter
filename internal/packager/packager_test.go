package packager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kiemlicz/charter/internal/common"
	"gopkg.in/yaml.v3"
)

func TestMain(m *testing.M) {
	common.Setup("debug")
	exitVal := m.Run()
	os.Exit(exitVal)
}

func TestParseAssets(t *testing.T) {
	//given
	assetsData := readTestData(t)

	//when
	manifests, err := common.NewManifests(assetsData, "0.0.1")

	//then
	if err != nil {
		t.Errorf("ParseAssets() error = %v", err)
		return
	}
	if len((*manifests).Manifests) != 18 {
		t.Errorf("ParseAssets() manifests = %v, want 10", len((*manifests).Manifests))
	}
	if len((*manifests).Crds) != 2 {
		t.Errorf("ParseAssets() crds = %v, want 1", len((*manifests).Crds))
	}
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
					ValuesSelector: ".spec.configuration",
					Kind:           "KubeVirt",
				},
				{
					Expression:     ".spec.customizeComponents |= \"{{ .Values.kubevirt.customizeComponents }}\"",
					ValuesSelector: ".spec.customizeComponents",
					Kind:           "KubeVirt",
				},
			},
			expectedValues: map[string]any{
				"kubevirt": map[string]any{
					"configuration": map[string]any{
						"developerConfiguration": map[string]any{
							"featureGates": []any{},
						},
					},
					"customizeComponents": map[string]any{},
				},
			}, // none expected as no value extraction
			expectedChanges: map[string]any{
				"metadata": map[string]any{
					"namespace": "{{ .Release.Namespace }}",
				},
				"spec": map[string]any{
					"configuration":       "{{ .Values.kubevirt.configuration }}",
					"customizeComponents": "{{ .Values.kubevirt.customizeComponents }}",
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
				t.Errorf("TestParametrizeExtractsValues() error = %v", err)
				return
			}

			for _, m := range (*modifiedManifests).Manifests {
				if !mapContains(&m, &tc.expectedChanges, false) {
					t.Errorf("TestParametrizeExtractsValues() modified manifest:\n%v, but wanted:\n%v", mustYaml(m), mustYaml(tc.expectedChanges))
					return
				}
			}
			common.Log.Infof("Extracted Values:\n%v\n", mustYaml(extractedValues))

			if !mapContains(extractedValues, &tc.expectedValues, true) {
				t.Errorf("TestParametrizeExtractsValues() extractedValues:\n%v, but wanted:\n%v", *extractedValues, tc.expectedValues)
				return
			}
		})
	}
}

func TestParametrizeListElement(t *testing.T) {
	//given
	testManifests, _ := common.NewManifests(readTestData(t), "0.0.1")
	mods := []common.Modification{
		*common.NewYqModification(".metadata.namespace |= \"{{ .Release.Namespace }}\""),
		{
			Expression: "(.subjects[] | select(.name == \"kubevirt-operator\") .namespace) = \"{{ .Release.Namespace }}\"",
			Kind:       "RoleBinding",
		},
	}

	//when
	modifiedManifests, _, err := ChartModifier.ParametrizeManifests(testManifests, &mods)

	//then
	if err != nil {
		t.Errorf("ParametrizeManifests() error = %v", err)
		return
	}

	expectedChanges := map[string]any{
		"kind": "RoleBinding",
		"metadata": map[string]any{
			"namespace": "{{ .Release.Namespace }}",
		},
		"subjects": []any{
			map[string]any{
				"kind":      "ServiceAccount",
				"name":      "kubevirt-operator",
				"namespace": "{{ .Release.Namespace }}",
			},
		},
	}

	for _, m := range (*modifiedManifests).Manifests {
		if m["kind"] == "RoleBinding" && m["metadata"].(map[string]any)["name"] == "kubevirt-operator-rolebinding" {
			if !mapContains(&m, &expectedChanges, true) {
				t.Errorf("ParametrizeManifests() modified manifest: \n%v,but wanted:\n%v", mustYaml(m), mustYaml(expectedChanges))
			}
			return
		}
	}
	t.Errorf("ParametrizeManifests() did not find a matching RoleBinding manifest or did not match expected changes")
}

func mapContains(mainMap *map[string]any, subMap *map[string]any, mustExist bool) bool {
	for k, subVal := range *subMap {
		mainVal, exists := (*mainMap)[k]
		if !exists {
			return !mustExist
		}

		subMapVal, subIsMap := subVal.(map[string]any)
		mainMapVal, mainIsMap := mainVal.(map[string]any)

		if subIsMap && mainIsMap {
			if !mapContains(&mainMapVal, &subMapVal, mustExist) {
				return false
			}
		} else if !reflect.DeepEqual(mainVal, subVal) {
			return false
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
