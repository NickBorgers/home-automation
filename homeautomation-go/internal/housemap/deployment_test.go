package housemap

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/roompresence"
)

// deploymentConfigDir is the config directory at the workspace root. A public checkout has no house map
// there. A private deployment's CI puts its own configs there, so these tests check the real map without it
// living in this repository.
var deploymentConfigDir = filepath.Join("..", "..", "..", "configs")

// coverageRingsM are the distances from the house at which TestDeploymentCoverage looks for gaps.
var coverageRingsM = []float64{3, 5, 8}

func loadDeploymentMap(t *testing.T) *Map {
	t.Helper()
	m, err := Load(filepath.Join(deploymentConfigDir, FileName))
	var verr *ValidationError
	if errors.As(err, &verr) {
		for _, p := range verr.Problems {
			t.Log(p)
			t.Errorf("house map: %s", p)
		}
		t.FailNow()
	}
	require.NoError(t, err)
	if m == nil {
		t.Skip("no deployment house map")
	}
	return m
}

// TestDeploymentMap validates a deployment's real map when one is present.
func TestDeploymentMap(t *testing.T) {
	m := loadDeploymentMap(t)
	require.NotEmpty(t, m.Spaces)
}

// TestDeploymentCrossCheck checks that a deployment's map and room presence config describe the same house.
func TestDeploymentCrossCheck(t *testing.T) {
	m := loadDeploymentMap(t)
	rp, err := roompresence.LoadConfig(filepath.Join(deploymentConfigDir, roompresence.ConfigFileName))
	require.NoError(t, err)
	if rp == nil {
		t.Skip("no deployment room presence config")
	}
	for _, p := range CrossCheck(m, rp) {
		t.Errorf("cross-check: %s", p)
	}
}

// TestDeploymentCoverage logs the points round the house that no outdoor camera covers. Camera placement is
// approximate, so gaps do not fail the test.
func TestDeploymentCoverage(t *testing.T) {
	m := loadDeploymentMap(t)
	for _, d := range coverageRingsM {
		ring := m.Ring(d, 1)
		gaps := m.CoverageGaps(d, 1)
		t.Logf("%g m from the house: %d of %d sampled points uncovered", d, len(gaps), len(ring))
		for _, g := range gaps {
			t.Logf("  uncovered at %g m: [%.1f, %.1f], nearest to %s", d, g[0], g[1], m.nearestBuilding(g))
		}
	}
}
