package planner

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"duplicate-detective/internal/scanner"
)

var (
	// ErrNoDuplicates is returned when planning is requested with no duplicate groups.
	ErrNoDuplicates = errors.New("planner: no duplicate groups provided")

	// ErrAllCopiesSelected is returned when an action would remove all copies in a duplicate group.
	ErrAllCopiesSelected = errors.New("planner: safety violation: cannot select all copies for removal; at least one copy must be preserved")

	// ErrFileNotFound is returned when a requested file path does not exist in the plan.
	ErrFileNotFound = errors.New("planner: file path not found in plan")

	// ErrGroupNotFound is returned when a requested group ID is not in the plan.
	ErrGroupNotFound = errors.New("planner: group ID not found in plan")
)

// Strategy specifies the keeper selection strategy.
type Strategy string

const (
	// StrategyShortestPath recommends the file with the shortest relative path as keeper.
	StrategyShortestPath Strategy = "shortest_path"

	// StrategyOldest recommends the oldest file (earliest mod time) as keeper.
	StrategyOldest Strategy = "oldest"

	// StrategyNewest recommends the newest file (latest mod time) as keeper.
	StrategyNewest Strategy = "newest"
)

// Options configures the planning engine.
type Options struct {
	// BaseDir is the reference directory for computing relative paths.
	// If empty, relative paths will be computed relative to filepath.Clean(root) or working directory.
	BaseDir string

	// Strategy defines how the keeper is recommended. Defaults to StrategyShortestPath.
	Strategy Strategy

	// AutoSelectDuplicates automatically marks all non-keeper files as selected for cleanup.
	AutoSelectDuplicates bool
}

// FileEntry represents a single file within a duplicate group plan.
type FileEntry struct {
	Path                 string    `json:"path"`
	RelPath              string    `json:"rel_path"`
	Size                 int64     `json:"size"`
	ModTime              time.Time `json:"mod_time"`
	Hash                 string    `json:"hash"`
	IsKeeper             bool      `json:"is_keeper"`
	Selected             bool      `json:"selected"`
	RecommendationReason string    `json:"recommendation_reason,omitempty"`
}

// PlanGroup represents a single duplicate group with keeper designation and selection status.
type PlanGroup struct {
	GroupID              string      `json:"group_id"`
	Hash                 string      `json:"hash"`
	Size                 int64       `json:"size"`
	Files                []FileEntry `json:"files"`
	RecommendedKeeper    string      `json:"recommended_keeper"`
	RecommendationReason string      `json:"recommendation_reason"`
	PotentialSavings     int64       `json:"potential_savings"`
	SelectedSavings      int64       `json:"selected_savings"`
}

// Plan contains all planned duplicate groups, options, and calculated savings.
type Plan struct {
	Groups           []PlanGroup `json:"groups"`
	BaseDir          string      `json:"base_dir"`
	Strategy         Strategy    `json:"strategy"`
	TotalGroups      int         `json:"total_groups"`
	TotalFiles       int         `json:"total_files"`
	PotentialSavings int64       `json:"potential_savings"`
	SelectedSavings  int64       `json:"selected_savings"`
}

// ComputeRelativePath calculates the relative path from baseDir to path.
// If baseDir is empty or relative path cannot be computed, it returns the cleaned path.
func ComputeRelativePath(baseDir, path string) string {
	cleanPath := filepath.Clean(path)
	if baseDir == "" {
		return cleanPath
	}
	cleanBase := filepath.Clean(baseDir)
	rel, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil {
		return cleanPath
	}
	return filepath.Clean(rel)
}

// PlanDuplicates creates a cleanup plan for duplicate groups.
func PlanDuplicates(groups []scanner.DuplicateGroup, opts Options) (*Plan, error) {
	if opts.Strategy == "" {
		opts.Strategy = StrategyShortestPath
	}

	plan := &Plan{
		Groups:   make([]PlanGroup, 0, len(groups)),
		BaseDir:  opts.BaseDir,
		Strategy: opts.Strategy,
	}

	for i, group := range groups {
		if len(group.Files) == 0 {
			continue
		}

		groupID := fmt.Sprintf("group-%d", i+1)
		planGroup, err := planSingleGroup(groupID, group, opts)
		if err != nil {
			return nil, err
		}

		plan.Groups = append(plan.Groups, planGroup)
		plan.TotalGroups++
		plan.TotalFiles += len(planGroup.Files)
		plan.PotentialSavings += planGroup.PotentialSavings
		plan.SelectedSavings += planGroup.SelectedSavings
	}

	return plan, nil
}

// planSingleGroup processes one duplicate group: selects keeper, explains reason, and calculates savings.
func planSingleGroup(groupID string, group scanner.DuplicateGroup, opts Options) (PlanGroup, error) {
	fileCount := len(group.Files)
	entries := make([]FileEntry, fileCount)

	for i, f := range group.Files {
		rel := ComputeRelativePath(opts.BaseDir, f.Path)
		h := f.Hash
		if h == "" {
			h = group.Hash
		}
		entries[i] = FileEntry{
			Path:    f.Path,
			RelPath: rel,
			Size:    f.Size,
			ModTime: f.ModTime,
			Hash:    h,
		}
	}

	keeperIdx, reason := pickKeeper(entries, opts.Strategy)
	entries[keeperIdx].IsKeeper = true
	entries[keeperIdx].RecommendationReason = reason

	var selectedSavings int64
	for i := range entries {
		if i != keeperIdx {
			entries[i].RecommendationReason = fmt.Sprintf("Duplicate of keeper %q", entries[keeperIdx].Path)
			if opts.AutoSelectDuplicates {
				entries[i].Selected = true
				selectedSavings += entries[i].Size
			}
		}
	}

	potentialSavings := int64(0)
	if fileCount > 1 {
		potentialSavings = int64(fileCount-1) * group.Size
	}

	pg := PlanGroup{
		GroupID:              groupID,
		Hash:                 group.Hash,
		Size:                 group.Size,
		Files:                entries,
		RecommendedKeeper:    entries[keeperIdx].Path,
		RecommendationReason: reason,
		PotentialSavings:     potentialSavings,
		SelectedSavings:      selectedSavings,
	}

	return pg, nil
}

// pickKeeper identifies the single keeper for the group using the configured strategy
// and shortest relative path as the deterministic tie-breaker.
func pickKeeper(entries []FileEntry, strategy Strategy) (int, string) {
	if len(entries) == 1 {
		return 0, "Only copy in group"
	}

	type candidate struct {
		index   int
		entry   FileEntry
		relLen  int
		relPath string
	}

	candidates := make([]candidate, len(entries))
	for i, e := range entries {
		candidates[i] = candidate{
			index:   i,
			entry:   e,
			relLen:  len(e.RelPath),
			relPath: e.RelPath,
		}
	}

	switch strategy {
	case StrategyOldest:
		// Sort by ModTime ascending.
		// Deterministic tie-breaker 1: shortest relative path length.
		// Deterministic tie-breaker 2: lexicographical order of relative path.
		sort.Slice(candidates, func(i, j int) bool {
			ti, tj := candidates[i].entry.ModTime, candidates[j].entry.ModTime
			if !ti.Equal(tj) {
				return ti.Before(tj)
			}
			if candidates[i].relLen != candidates[j].relLen {
				return candidates[i].relLen < candidates[j].relLen
			}
			return candidates[i].relPath < candidates[j].relPath
		})

		best := candidates[0]
		// Check if tie-breaker was applied against any other candidate with identical mod time
		hasModTimeTie := len(candidates) > 1 && candidates[0].entry.ModTime.Equal(candidates[1].entry.ModTime)
		var reason string
		if hasModTimeTie {
			reason = fmt.Sprintf("Recommended keeper: oldest file (%s); tie-breaker: shortest relative path %q (length %d)",
				best.entry.ModTime.UTC().Format(time.RFC3339), best.relPath, best.relLen)
		} else {
			reason = fmt.Sprintf("Recommended keeper: oldest file modified at %s",
				best.entry.ModTime.UTC().Format(time.RFC3339))
		}
		return best.index, reason

	case StrategyNewest:
		// Sort by ModTime descending.
		// Deterministic tie-breaker 1: shortest relative path length.
		// Deterministic tie-breaker 2: lexicographical order.
		sort.Slice(candidates, func(i, j int) bool {
			ti, tj := candidates[i].entry.ModTime, candidates[j].entry.ModTime
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			if candidates[i].relLen != candidates[j].relLen {
				return candidates[i].relLen < candidates[j].relLen
			}
			return candidates[i].relPath < candidates[j].relPath
		})

		best := candidates[0]
		hasModTimeTie := len(candidates) > 1 && candidates[0].entry.ModTime.Equal(candidates[1].entry.ModTime)
		var reason string
		if hasModTimeTie {
			reason = fmt.Sprintf("Recommended keeper: newest file (%s); tie-breaker: shortest relative path %q (length %d)",
				best.entry.ModTime.UTC().Format(time.RFC3339), best.relPath, best.relLen)
		} else {
			reason = fmt.Sprintf("Recommended keeper: newest file modified at %s",
				best.entry.ModTime.UTC().Format(time.RFC3339))
		}
		return best.index, reason

	case StrategyShortestPath:
		fallthrough
	default:
		// Sort by shortest relative path.
		// Deterministic tie-breaker: lexicographical order of relative path.
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].relLen != candidates[j].relLen {
				return candidates[i].relLen < candidates[j].relLen
			}
			return candidates[i].relPath < candidates[j].relPath
		})

		best := candidates[0]
		hasLengthTie := len(candidates) > 1 && candidates[0].relLen == candidates[1].relLen
		var reason string
		if hasLengthTie {
			reason = fmt.Sprintf("Recommended keeper: shortest relative path %q (length %d); tie broken alphabetically",
				best.relPath, best.relLen)
		} else {
			reason = fmt.Sprintf("Recommended keeper: shortest relative path %q (length %d)",
				best.relPath, best.relLen)
		}
		return best.index, reason
	}
}

// Validate verifies safety invariants across the entire plan.
// It strictly enforces that no duplicate group has all copies marked for cleanup.
func (p *Plan) Validate() error {
	for _, g := range p.Groups {
		if len(g.Files) == 0 {
			continue
		}
		var unselected int
		for _, f := range g.Files {
			if !f.Selected {
				unselected++
			}
		}
		if unselected == 0 {
			return fmt.Errorf("%w: group %s (%d files, hash %s) has every copy selected",
				ErrAllCopiesSelected, g.GroupID, len(g.Files), g.Hash)
		}
	}
	return nil
}

// RecalculateSavings updates the savings on each group and the aggregate plan
// based strictly on actual selected files.
func (p *Plan) RecalculateSavings() {
	var totalSelectedSavings int64
	for gi := range p.Groups {
		var groupSavings int64
		for _, f := range p.Groups[gi].Files {
			if f.Selected {
				groupSavings += f.Size
			}
		}
		p.Groups[gi].SelectedSavings = groupSavings
		totalSelectedSavings += groupSavings
	}
	p.SelectedSavings = totalSelectedSavings
}

// SetSelection sets the cleanup selection status for a specific file.
// If selecting the file would cause all copies in its duplicate group to be removed,
// the operation is rejected with ErrAllCopiesSelected to protect the user's data.
func (p *Plan) SetSelection(path string, selected bool) error {
	clean := filepath.Clean(path)
	for gi := range p.Groups {
		for fi := range p.Groups[gi].Files {
			if filepath.Clean(p.Groups[gi].Files[fi].Path) == clean {
				if selected {
					// Check if all other files in this group are already selected
					var otherUnselected int
					for oi, of := range p.Groups[gi].Files {
						if oi != fi && !of.Selected {
							otherUnselected++
						}
					}
					if otherUnselected == 0 {
						return fmt.Errorf("%w: cannot select %q as it is the last remaining copy in %s",
							ErrAllCopiesSelected, path, p.Groups[gi].GroupID)
					}
				}

				p.Groups[gi].Files[fi].Selected = selected
				p.RecalculateSavings()
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrFileNotFound, path)
}

// SelectNonKeepers marks all non-keeper files as selected for cleanup,
// guaranteeing that exactly the recommended keeper is preserved for each group.
func (p *Plan) SelectNonKeepers() {
	for gi := range p.Groups {
		for fi := range p.Groups[gi].Files {
			p.Groups[gi].Files[fi].Selected = !p.Groups[gi].Files[fi].IsKeeper
		}
	}
	p.RecalculateSavings()
}

// DeselectAll unchecks all files for cleanup across the entire plan.
func (p *Plan) DeselectAll() {
	for gi := range p.Groups {
		for fi := range p.Groups[gi].Files {
			p.Groups[gi].Files[fi].Selected = false
		}
	}
	p.RecalculateSavings()
}

// SelectedFiles returns a list of all file entries currently marked for cleanup.
func (p *Plan) SelectedFiles() []FileEntry {
	var list []FileEntry
	for _, g := range p.Groups {
		for _, f := range g.Files {
			if f.Selected {
				list = append(list, f)
			}
		}
	}
	return list
}
