package cli

import (
	"testing"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// TestCoresBelowFloor pins the auto-shrink threshold to the documented
// standard core floor: tight hosts (< floor) engage shrink; hosts at or
// above it don't.
func TestCoresBelowFloor(t *testing.T) {
	floor := version.MinBaseline.Cores
	cases := []struct {
		cores   int
		workers int
		want    bool
	}{
		{1, 1, true},
		{4, 1, true},          // Raspberry-Pi shape
		{floor - 1, 1, true},  // just under
		{floor, 1, false},     // exactly the floor is enough
		{floor + 1, 1, false}, // roomy
		{64, 1, false},
		// N-aware: each extra TMM node raises the floor by
		// PerExtraTMMNodeCores, so a host that's roomy for 1 TMM can be
		// tight for several.
		{floor, 2, true}, // floor for 2 = floor+8 > floor
		{floor + version.PerExtraTMMNodeCores, 2, false},      // exactly meets the 2-node floor
		{floor + version.PerExtraTMMNodeCores - 1, 2, true},   // just under the 2-node floor
		{floor + 2*version.PerExtraTMMNodeCores, 3, false},    // meets the 3-node floor
		{floor + 2*version.PerExtraTMMNodeCores - 1, 3, true}, // under the 3-node floor
	}
	for _, c := range cases {
		if got := coresBelowFloor(c.cores, c.workers); got != c.want {
			t.Errorf("coresBelowFloor(%d, workers=%d) = %v, want %v (floor1=%d)",
				c.cores, c.workers, got, c.want, floor)
		}
	}
}

// TestDeployShrinkPhaseWiring locks in that deploy-shrink is part of the
// canonical pipeline, is marked auto (conditional on host size), and runs
// after deploy-flo but before deploy-cne — the ordering that lets the
// Kyverno cap reach the TMM/DSSM pods deploy-cne creates.
func TestDeployShrinkPhaseWiring(t *testing.T) {
	idx := map[string]int{}
	var shrink *e2ePhase
	for i := range canonicalPhases {
		ph := &canonicalPhases[i]
		idx[ph.name] = i
		if ph.name == "deploy-shrink" {
			shrink = ph
		}
	}
	if shrink == nil {
		t.Fatal("deploy-shrink missing from canonicalPhases")
	}
	if !shrink.auto {
		t.Error("deploy-shrink must be marked auto (conditional on host cores)")
	}
	if !shrink.destructive || shrink.confirmFlag != "--confirm-deploy" {
		t.Errorf("deploy-shrink gates wrong: destructive=%v confirm=%q", shrink.destructive, shrink.confirmFlag)
	}
	flo, okFlo := idx["deploy-flo"]
	cne, okCne := idx["deploy-cne"]
	if !okFlo || !okCne {
		t.Fatal("deploy-flo / deploy-cne missing from canonicalPhases")
	}
	if !(flo < idx["deploy-shrink"] && idx["deploy-shrink"] < cne) {
		t.Errorf("deploy-shrink must sit between deploy-flo (%d) and deploy-cne (%d), got %d",
			flo, cne, idx["deploy-shrink"])
	}
}

// TestSelectPhasesIncludesShrink confirms the new phase is reachable both in
// a full run and via an explicit --phase selection.
func TestSelectPhasesIncludesShrink(t *testing.T) {
	full, err := selectPhases("")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPhase(full, "deploy-shrink") {
		t.Error("full run should include deploy-shrink")
	}

	only, err := selectPhases("deploy-shrink")
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].name != "deploy-shrink" {
		t.Errorf("--phase deploy-shrink selected %v, want [deploy-shrink]", phaseNames(only))
	}
}

func containsPhase(phs []e2ePhase, name string) bool {
	for _, ph := range phs {
		if ph.name == name {
			return true
		}
	}
	return false
}
