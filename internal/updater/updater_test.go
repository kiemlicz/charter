package updater

import (
	"github.com/kiemlicz/kubevirt-charts/internal/common"
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

//func TestParametrizeExtractsValues() {
//}

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
