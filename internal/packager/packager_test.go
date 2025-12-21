package packager

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/kiemlicz/charter/internal/common"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
)

const (
	TestChartDir   = "testdata/charts"
	TestPackageDir = "testdata/packaged"
)

var testHelmSettings = common.HelmSettings{
	SrcDir:    TestChartDir,
	TargetDir: TestPackageDir,
	LintK8s:   "1.30.0",
	Remote:    "",
}

// BeforeAll
func TestMain(m *testing.M) {
	common.Setup("debug")
	if err := os.MkdirAll(TestChartDir, 0o755); err != nil {
		panic("failed to create test charts directory: " + err.Error())
	}
	exitVal := m.Run()
	if exitVal == 0 {
		os.RemoveAll(TestChartDir)
	}
	os.Exit(exitVal)
}

func TestParseAssets(t *testing.T) {
	//given

	//when
	testManifests, err := getTestManifests(t)

	//then
	if err != nil {
		t.Errorf("ParseAssets() error = %v", err)
		return
	}
	if len((*testManifests).Manifests) != 18 {
		t.Errorf("ParseAssets() testManifests = %v, want 10", len((*testManifests).Manifests))
	}
	if len((*testManifests).Crds) != 2 {
		t.Errorf("ParseAssets() crds = %v, want 1", len((*testManifests).Crds))
	}
}

func TestValuesExtraction(t *testing.T) {
	testManifests, _ := getTestManifests(t)
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
					ValuesSelector: []string{".spec.configuration"},
					Kind:           "KubeVirt",
				},
				{
					Expression:     ".spec.customizeComponents |= \"{{ .Values.kubevirt.customizeComponents }}\"",
					ValuesSelector: []string{".spec.customizeComponents"},
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
			modifiedManifests, err := ChartModifier.ParametrizeManifests(testManifests, &tc.modifications)

			//then
			if err != nil {
				t.Errorf("TestValuesExtraction() error = %v", err)
				return
			}

			for _, m := range (*modifiedManifests).Manifests {
				if !mapContains(&m, &tc.expectedChanges, false) {
					t.Errorf("TestValuesExtraction() modified manifest:\n%v, but wanted:\n%v", mustYaml(m), mustYaml(tc.expectedChanges))
					return
				}
			}
			common.Log.Infof("Extracted Values:\n%v\n", mustYaml(modifiedManifests.Values))

			if !mapContains(&modifiedManifests.Values, &tc.expectedValues, true) {
				t.Errorf("TestValuesExtraction() extractedValues:\n%v, but wanted:\n%v", modifiedManifests.Values, tc.expectedValues)
				return
			}
		})
	}
}

func TestParametrizeListElement(t *testing.T) {
	//given
	testManifests, _ := getTestManifests(t)
	mods := []common.Modification{
		*common.NewYqModification(".metadata.namespace |= \"{{ .Release.Namespace }}\""),
		{
			Expression: "(.subjects[] | select(.name == \"kubevirt-operator\") .namespace) = \"{{ .Release.Namespace }}\"",
			Kind:       "RoleBinding",
		},
	}

	//when
	modifiedManifests, err := ChartModifier.ParametrizeManifests(testManifests, &mods)

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

func TestMultiValueSelector(t *testing.T) {
	//given
	testManifests, _ := getTestManifests(t)
	mods := []common.Modification{
		{
			Expression: ".spec.template.spec.containers[0].image |= \"{{ .Values.kubevirtOperator.deployment.image.repository }}:{{ .Values.kubevirtOperator.deployment.image.tag }}\"",
			ValuesSelector: []string{
				".spec.template.spec.containers[0].image | split(\":\") | .[0]",
				".spec.template.spec.containers[0].image | split(\":\") | .[1]",
			},
			Kind: "Deployment",
		},
	}

	//when
	modifiedManifests, err := ChartModifier.ParametrizeManifests(testManifests, &mods)

	//then
	if err != nil {
		t.Fatalf("ParametrizeManifests() error = %v", err)
	}

	imageMap, ok := modifiedManifests.Values["kubevirtOperator"].(map[string]any)["deployment"].(map[string]any)["image"].(map[string]any)
	if !ok {
		t.Fatalf("image values not found or of wrong type")
	}
	if _, repoOk := imageMap["repository"]; !repoOk {
		t.Errorf("image values missing 'repository' key")
	}
	if _, tagOk := imageMap["tag"]; !tagOk {
		t.Errorf("image values missing 'tag' key")
	}
}

func TestInsertHelpers(t *testing.T) {
	//given
	kind := "ClusterRole"
	name := "clusterrole.yaml"
	data, err := os.ReadFile(filepath.Join("testdata", "templates", name))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	template := chart.File{
		Name: name,
		Data: data,
	}
	mods := []common.Modification{
		{
			Expression: "${1}${2}${3} {{- include \"cdi.labels\" . | nindent 8 }}",
			TextRegex:  "(?m)(^metadata:\\s*\\n(?:[ \\t]+[^\\n]*\\n)*?)([ \\t]+)(labels:)",
			Kind:       ".*Role$",
		},
	}

	//when
	err = insertHelpers(kind, &template, &mods)

	//then
	if err != nil {
		t.Fatalf("insertHelpers() error = %v", err)
	}
	templateString := string(template.Data)
	expectedHelper := `metadata:
  labels: {{- include "cdi.labels" . | nindent 8 }}
    operator.cdi.kubevirt.io: ""
  name: cdi-operator-cluster
`

	t.Logf("Modified template:\n%s", templateString)

	if !strings.Contains(templateString, expectedHelper) {
		t.Errorf("InsertHelpers() template: %s, does not contain expected helper: %s", templateString, expectedHelper)
	}
}

func TestPrepare(t *testing.T) { // this is actually an integration test with both parametrize and insertion of templates
	//given
	manifests, _ := getTestManifests(t)
	helmOps := common.HelmOps{
		ChartName: "cdi",
		Drop:      []string{},
		Modifications: []common.Modification{
			{
				Expression:     ".metadata.labels |= \"{{ .Values.cdi.role.labels }}\"",
				ValuesSelector: []string{".metadata.labels"},
				Kind:           ".*Role$",
			},
			{
				Expression: "{{- include \"cdi.labels\" . | nindent 8 }}",
				TextRegex:  "{{ .Values.cdi.role.labels }}",
				Kind:       ".*Role$",
			},
		},
		AddValues:    map[string]any{},
		AddCrdValues: map[string]any{},
	}

	//when
	helmCharts, err := Prepare(manifests, &helmOps, &testHelmSettings)

	//then
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	templateString := string(getTemplate("templates/clusterrole.yaml", helmCharts.Chart.Templates).Data)
	expectedHelper := `metadata:
    labels: {{- include "cdi.labels" . | nindent 8 }}
    name: cdi-operator-cluster
`

	if !strings.Contains(templateString, expectedHelper) {
		t.Errorf("template:\n%s, does not contain expected helper:\n%s", templateString, expectedHelper)
	}
}

func getTemplate(name string, templates []*chart.File) *chart.File {
	for _, tmpl := range templates {
		if strings.EqualFold(tmpl.Name, name) {
			return tmpl
		}
	}
	return nil
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

func getTestManifests(t *testing.T) (*common.Manifests, error) {
	return common.NewManifests(readTestData(t), mustSemver("0.0.1"), "0.0.1", new(map[string]any), new(map[string]any))
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

func mustSemver(v string) *semver.Version {
	s, err := semver.NewVersion(v)
	if err != nil {
		panic(err)
	}
	return s
}
