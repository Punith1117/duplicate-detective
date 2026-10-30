package quarantine

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReconciliationReport contains details of the startup state reconciliation.
type ReconciliationReport struct {
	Warnings         []string `json:"warnings"`
	RecoveredEntries int      `json:"recovered_entries"`
	FailedEntries    int      `json:"failed_entries"`
	UntrackedFiles   []string `json:"untracked_files"`
}

// startupReconciliation reconciles manifest records and quarantine filesystem state.
// In accordance with safety requirements:
// - It never purges quarantine contents.
// - It preserves ambiguous and untracked files.
// - It reports recovery warnings.
func (m *Manager) startupReconciliation() (*ReconciliationReport, error) {
	report := &ReconciliationReport{
		Warnings:       make([]string, 0),
		UntrackedFiles: make([]string, 0),
	}

	manifestChanged := false

	// Step 1: Read existing manifest or initialize new one
	if _, err := os.Stat(m.manifestPath); err == nil {
		data, err := os.ReadFile(m.manifestPath)
		if err != nil {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("failed to read existing manifest %s: %v; preserving files", m.manifestPath, err))
		} else {
			var loaded Manifest
			if err := json.Unmarshal(data, &loaded); err != nil {
				// Corrupted JSON manifest: NEVER delete! Back it up and preserve everything.
				backupPath := fmt.Sprintf("%s.corrupted.%d", m.manifestPath, time.Now().UnixNano())
				_ = os.Rename(m.manifestPath, backupPath)
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("corrupted manifest detected and backed up to %s: %v; quarantine contents preserved",
						filepath.Base(backupPath), err))
				manifestChanged = true
			} else {
				if loaded.Entries == nil {
					loaded.Entries = make(map[string]Entry)
				}
				m.manifest = &loaded
			}
		}
	} else if os.IsNotExist(err) {
		// New clean manifest will be written
		manifestChanged = true
	} else {
		report.Warnings = append(report.Warnings, fmt.Sprintf("unable to stat manifest %s: %v", m.manifestPath, err))
	}

	// Step 2: Reconcile each manifest entry against the filesystem
	trackedQuarantinePaths := make(map[string]bool)

	for id, entry := range m.manifest.Entries {
		if entry.QuarantinePath != "" {
			trackedQuarantinePaths[filepath.Clean(entry.QuarantinePath)] = true
		}

		switch entry.State {
		case StatePending:
			// Operation was interrupted mid-flight (crash/restart recovery)
			origExists := fileExists(entry.OriginalPath)
			qExists := fileExists(entry.QuarantinePath)

			if origExists && qExists {
				// Ambiguous: files exist at both paths. Preserve both!
				entry.State = StateFailed
				entry.ErrorMessage = "restart recovery: file exists at both original and quarantine paths; preserving both"
				report.FailedEntries++
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("ambiguous pending operation %s: file exists at both original (%s) and quarantine (%s); preserving both",
						id, entry.OriginalPath, entry.QuarantinePath))
				m.manifest.Entries[id] = entry
				manifestChanged = true

			} else if !origExists && qExists {
				// File was transferred to quarantine, but manifest update was interrupted
				qHash, err := ComputeFileSHA256(entry.QuarantinePath)
				if err == nil && strings.EqualFold(qHash, entry.SHA256) {
					// Confirmed complete transfer: mark quarantined
					entry.State = StateQuarantined
					report.RecoveredEntries++
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("recovered pending operation %s: file verified in quarantine and updated to quarantined", id))
					m.manifest.Entries[id] = entry
					manifestChanged = true
				} else {
					// Hash mismatch or unreadable: preserve and mark failed
					entry.State = StateFailed
					entry.ErrorMessage = fmt.Sprintf("restart recovery: quarantine file hash mismatch (got %s, expected %s); preserved", qHash, entry.SHA256)
					report.FailedEntries++
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("pending operation %s: quarantine file %s hash verification failed; preserving file", id, entry.QuarantinePath))
					m.manifest.Entries[id] = entry
					manifestChanged = true
				}

			} else if origExists && !qExists {
				// Move never started or completed: original file is safe
				entry.State = StateFailed
				entry.ErrorMessage = "restart recovery: operation interrupted before file move; original file preserved"
				report.FailedEntries++
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("pending operation %s: interrupted before move; original file preserved at %s", id, entry.OriginalPath))
				m.manifest.Entries[id] = entry
				manifestChanged = true

			} else {
				// Neither file exists
				entry.State = StateFailed
				entry.ErrorMessage = "restart recovery: file missing from both original and quarantine paths"
				report.FailedEntries++
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("pending operation %s: file missing at both original (%s) and quarantine (%s)",
						id, entry.OriginalPath, entry.QuarantinePath))
				m.manifest.Entries[id] = entry
				manifestChanged = true
			}

		case StateQuarantined:
			if !fileExists(entry.QuarantinePath) {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("stale manifest warning: quarantined file missing for operation %s at %s", id, entry.QuarantinePath))
			} else {
				qHash, err := ComputeFileSHA256(entry.QuarantinePath)
				if err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("integrity warning: could not hash quarantined file for operation %s at %s: %v", id, entry.QuarantinePath, err))
				} else if !strings.EqualFold(qHash, entry.SHA256) {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("integrity warning: quarantined file for operation %s has altered content at %s (expected %s, got %s)",
							id, entry.QuarantinePath, entry.SHA256, qHash))
				}
			}

		case StateRestored:
			if fileExists(entry.QuarantinePath) {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("ambiguous file: operation %s marked restored but file still present in quarantine at %s; preserving file",
						id, entry.QuarantinePath))
			}
		}
	}

	// Step 3: Scan quarantine filesystem for untracked/orphan files
	// Requirement 10: Never automatically purge quarantine contents.
	// Requirement 11: Preserve ambiguous files and report recovery warnings.
	if _, err := os.Stat(m.filesDir); err == nil {
		_ = filepath.WalkDir(m.filesDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("error scanning quarantine files: %v", err))
				return nil
			}
			if d.IsDir() {
				return nil
			}
			clean := filepath.Clean(path)
			if !trackedQuarantinePaths[clean] {
				report.UntrackedFiles = append(report.UntrackedFiles, clean)
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("untracked file found in quarantine directory: %s; preserved without purging", clean))
			}
			return nil
		})
	}

	// Save manifest if entries were reconciled or new manifest created
	if manifestChanged {
		if err := m.saveManifestLocked(); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("failed to save reconciled manifest: %v", err))
		}
	}

	return report, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

