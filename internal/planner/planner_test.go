package planner

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"duplicate-detective/internal/scanner"
)

func TestRecommendOneKeeperPerDuplicateGroup(t *testing.T) {
	now := time.Now()
	groups := []scanner.DuplicateGroup{
		{
			Hash: "hash1",
			Size: 1024,
			Files: []scanner.FileInfo{
				{Path: "/root/dir1/a.txt", Size: 1024, ModTime: now},
				{Path: "/root/dir1/sub/b.txt", Size: 1024, ModTime: now},
				{Path: "/root/c.txt", Size: 1024, ModTime: now},
			},
		},
		{
			Hash: "hash2",
			Size: 2048,
			Files: []scanner.FileInfo{
				{Path: "/root/x/file.bin", Size: 2048, ModTime: now},
				{Path: "/root/y/nested/file.bin", Size: 2048, ModTime: now},
			},
		},
	}

	plan, err := PlanDuplicates(groups, Options{BaseDir: "/root"})
	if err != nil {
		t.Fatalf("unexpected error planning duplicates: %v", err)
	}

	if len(plan.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(plan.Groups))
	}

	for _, g := range plan.Groups {
		var keeperCount int
		for _, f := range g.Files {
			if f.IsKeeper {
				keeperCount++
				if f.Path != g.RecommendedKeeper {
					t.Errorf("keeper path mismatch: file says %s, group says %s", f.Path, g.RecommendedKeeper)
				}
			}
		}
		if keeperCount != 1 {
			t.Errorf("group %s: expected exactly 1 keeper, got %d", g.GroupID, keeperCount)
		}
		if g.RecommendationReason == "" {
			t.Errorf("group %s: missing recommendation reason", g.GroupID)
		}
	}
}

func TestDeterministicTieBreaker_ShortestRelativePath(t *testing.T) {
	baseDir := "/workspace/project"
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	// Case 1: Shortest relative path should win over deeper/longer paths
	group1 := scanner.DuplicateGroup{
		Hash: "hash-len-tie",
		Size: 500,
		Files: []scanner.FileInfo{
			{Path: filepath.Join(baseDir, "very", "long", "nested", "path", "doc.txt"), Size: 500, ModTime: now},
			{Path: filepath.Join(baseDir, "doc.txt"), Size: 500, ModTime: now}, // Shortest relative path: "doc.txt"
			{Path: filepath.Join(baseDir, "sub", "doc.txt"), Size: 500, ModTime: now},
		},
	}

	plan, err := PlanDuplicates([]scanner.DuplicateGroup{group1}, Options{
		BaseDir:  baseDir,
		Strategy: StrategyShortestPath,
	})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	expectedKeeper := filepath.Join(baseDir, "doc.txt")
	if plan.Groups[0].RecommendedKeeper != expectedKeeper {
		t.Fatalf("expected keeper %q, got %q", expectedKeeper, plan.Groups[0].RecommendedKeeper)
	}

	// Case 2: Exact same relative path lengths -> deterministic alphabetical tie-breaker
	group2 := scanner.DuplicateGroup{
		Hash: "hash-alpha-tie",
		Size: 500,
		Files: []scanner.FileInfo{
			{Path: filepath.Join(baseDir, "b", "file.txt"), Size: 500, ModTime: now}, // rel: "b/file.txt" (len 10)
			{Path: filepath.Join(baseDir, "a", "file.txt"), Size: 500, ModTime: now}, // rel: "a/file.txt" (len 10)
		},
	}

	plan2, err := PlanDuplicates([]scanner.DuplicateGroup{group2}, Options{
		BaseDir:  baseDir,
		Strategy: StrategyShortestPath,
	})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	expectedAlphaKeeper := filepath.Join(baseDir, "a", "file.txt")
	if plan2.Groups[0].RecommendedKeeper != expectedAlphaKeeper {
		t.Fatalf("expected tie-breaker keeper %q, got %q", expectedAlphaKeeper, plan2.Groups[0].RecommendedKeeper)
	}
	if !strings.Contains(plan2.Groups[0].RecommendationReason, "alphabetically") {
		t.Errorf("expected explanation to mention alphabetical tie-breaker, got: %s", plan2.Groups[0].RecommendationReason)
	}

	// Case 3: StrategyOldest with identical timestamps -> tie-breaker is shortest relative path
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	group3 := scanner.DuplicateGroup{
		Hash: "hash-modtime-tie",
		Size: 500,
		Files: []scanner.FileInfo{
			{Path: filepath.Join(baseDir, "deep", "nested", "copy.txt"), Size: 500, ModTime: t1},
			{Path: filepath.Join(baseDir, "short.txt"), Size: 500, ModTime: t1}, // same modtime, shorter path
		},
	}

	plan3, err := PlanDuplicates([]scanner.DuplicateGroup{group3}, Options{
		BaseDir:  baseDir,
		Strategy: StrategyOldest,
	})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	expectedOldestTieKeeper := filepath.Join(baseDir, "short.txt")
	if plan3.Groups[0].RecommendedKeeper != expectedOldestTieKeeper {
		t.Fatalf("expected oldest tie-breaker keeper %q, got %q", expectedOldestTieKeeper, plan3.Groups[0].RecommendedKeeper)
	}
	if !strings.Contains(plan3.Groups[0].RecommendationReason, "tie-breaker: shortest relative path") {
		t.Errorf("expected reason to document shortest relative path tie-breaker, got: %s", plan3.Groups[0].RecommendationReason)
	}
}

func TestKeeperProtection_NeverAllowRemovingEveryCopy(t *testing.T) {
	group := scanner.DuplicateGroup{
		Hash: "test-hash",
		Size: 100,
		Files: []scanner.FileInfo{
			{Path: "/tmp/copy1.txt", Size: 100},
			{Path: "/tmp/copy2.txt", Size: 100},
			{Path: "/tmp/copy3.txt", Size: 100},
		},
	}

	plan, err := PlanDuplicates([]scanner.DuplicateGroup{group}, Options{})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	// 1. Initially none selected -> plan is valid
	if err := plan.Validate(); err != nil {
		t.Fatalf("initial plan should be valid: %v", err)
	}

	// 2. Select first 2 copies -> valid (1 copy remains)
	if err := plan.SetSelection("/tmp/copy2.txt", true); err != nil {
		t.Fatalf("selecting copy2 should succeed: %v", err)
	}
	if err := plan.SetSelection("/tmp/copy3.txt", true); err != nil {
		t.Fatalf("selecting copy3 should succeed: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan with 1 preserved copy should be valid: %v", err)
	}

	// 3. Attempting to select the final copy MUST be blocked
	err = plan.SetSelection("/tmp/copy1.txt", true)
	if err == nil {
		t.Fatal("expected SetSelection to fail when removing all copies, but it succeeded")
	}
	if !errors.Is(err, ErrAllCopiesSelected) {
		t.Fatalf("expected ErrAllCopiesSelected, got: %v", err)
	}

	// 4. Test Validate() explicitly catches manual tampering if every copy is marked selected
	for i := range plan.Groups[0].Files {
		plan.Groups[0].Files[i].Selected = true
	}
	err = plan.Validate()
	if err == nil {
		t.Fatal("Validate() must reject a plan where every copy is selected")
	}
	if !errors.Is(err, ErrAllCopiesSelected) {
		t.Fatalf("expected ErrAllCopiesSelected from Validate(), got: %v", err)
	}

	// 5. SelectNonKeepers must keep the keeper intact
	plan.SelectNonKeepers()
	if err := plan.Validate(); err != nil {
		t.Fatalf("SelectNonKeepers must maintain at least one keeper: %v", err)
	}
	var keptCount int
	for _, f := range plan.Groups[0].Files {
		if !f.Selected {
			keptCount++
			if !f.IsKeeper {
				t.Errorf("expected kept file to be designated keeper")
			}
		}
	}
	if keptCount != 1 {
		t.Fatalf("expected exactly 1 kept file after SelectNonKeepers, got %d", keptCount)
	}
}

func TestSavingsCalculation_ActualSelectedFiles(t *testing.T) {
	group1 := scanner.DuplicateGroup{
		Hash: "hash-100",
		Size: 100,
		Files: []scanner.FileInfo{
			{Path: "/test/g1/a.txt", Size: 100},
			{Path: "/test/g1/b.txt", Size: 100},
			{Path: "/test/g1/c.txt", Size: 100},
		},
	}
	group2 := scanner.DuplicateGroup{
		Hash: "hash-500",
		Size: 500,
		Files: []scanner.FileInfo{
			{Path: "/test/g2/x.bin", Size: 500},
			{Path: "/test/g2/y.bin", Size: 500},
		},
	}

	plan, err := PlanDuplicates([]scanner.DuplicateGroup{group1, group2}, Options{})
	if err != nil {
		t.Fatalf("failed to plan: %v", err)
	}

	// Potential savings: (3-1)*100 + (2-1)*500 = 200 + 500 = 700
	if plan.PotentialSavings != 700 {
		t.Fatalf("expected potential savings 700, got %d", plan.PotentialSavings)
	}

	// Step 1: Nothing selected initially
	if plan.SelectedSavings != 0 {
		t.Fatalf("expected 0 initial selected savings, got %d", plan.SelectedSavings)
	}

	// Step 2: Select 1 file from group 1 (100 bytes)
	if err := plan.SetSelection("/test/g1/b.txt", true); err != nil {
		t.Fatalf("failed to select: %v", err)
	}
	if plan.SelectedSavings != 100 {
		t.Fatalf("expected 100 selected savings, got %d", plan.SelectedSavings)
	}
	if plan.Groups[0].SelectedSavings != 100 {
		t.Fatalf("expected group 1 savings 100, got %d", plan.Groups[0].SelectedSavings)
	}
	if plan.Groups[1].SelectedSavings != 0 {
		t.Fatalf("expected group 2 savings 0, got %d", plan.Groups[1].SelectedSavings)
	}

	// Step 3: Select 1 file from group 2 (500 bytes)
	if err := plan.SetSelection("/test/g2/y.bin", true); err != nil {
		t.Fatalf("failed to select: %v", err)
	}
	if plan.SelectedSavings != 600 {
		t.Fatalf("expected 600 selected savings, got %d", plan.SelectedSavings)
	}

	// Step 4: Deselect file from group 1
	if err := plan.SetSelection("/test/g1/b.txt", false); err != nil {
		t.Fatalf("failed to deselect: %v", err)
	}
	if plan.SelectedSavings != 500 {
		t.Fatalf("expected 500 selected savings after deselect, got %d", plan.SelectedSavings)
	}

	// Step 5: Select all non-keepers
	plan.SelectNonKeepers()
	if plan.SelectedSavings != 700 {
		t.Fatalf("expected 700 selected savings when all non-keepers selected, got %d", plan.SelectedSavings)
	}

	// Step 6: Deselect all
	plan.DeselectAll()
	if plan.SelectedSavings != 0 {
		t.Fatalf("expected 0 selected savings after DeselectAll, got %d", plan.SelectedSavings)
	}
}
