package updater

import (
	"github.com/kiemlicz/kubevirt-charts/internal/common"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	common.Setup("debug")
	exitVal := m.Run()
	os.Exit(exitVal)
}

func TestParseAssets(t *testing.T) {
	assetsData := readTestData(t)
	t.Run("ParseAssets", func(t *testing.T) {
		manifests, crds, err := ParseAssets(assetsData)
		if err != nil {
			t.Errorf("ParseAssets() error = %v", err)
			return
		}
		if len(*manifests) != 10 {
			t.Errorf("ParseAssets() manifests = %v, want 10", manifests)
		}
		if len(*crds) != 1 {
			t.Errorf("ParseAssets() crds = %v, want 1", crds)
		}
	})
}

func TestParametrizeExtractsValues(t *testing.T) {
	testManifests, _, _ := ParseAssets(readTestData(t))
	testCases := map[string]struct {
		replacements   string
		expectedValues string
	}{
		"simple": {
			replacements: `
      - sourceValue: "{{ .Values.kubevirt.configuration | toYaml | nindent 4 }}"
        targets:
          - select:
              kind: KubeVirt
            fieldPaths:
              - "spec.configuration"
`,
			expectedValues: `kubevirt:
  configuration:
    developerConfiguration:
      featureGates: []
`,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			modifiedManifests, extractedValues, err := Parametrize(testManifests, &tc.replacements)
			if err != nil {
				t.Errorf("Parametrize() error = %v", err)
				return
			}
			common.Log.Infof("Modified Manifests:\n")
			for _, m := range *modifiedManifests {
				common.Log.Infof("---\n%v\n", mustYaml(m))
			}
			common.Log.Infof("Extracted Values:\n%v\n", mustYaml(extractedValues))
		})
	}
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
