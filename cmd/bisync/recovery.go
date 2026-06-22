package bisync

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/terminal"
)

const (
	recoveryStatusInProgress = "in_progress"
	recoveryStatusComplete   = "complete"
	recoveryDirPrefix        = ".recovery_"
	manifestFileName         = "manifest.json"
)

// recoveryFile describes a single file tracked by a recovery manifest.
type recoveryFile struct {
	OriginalName string `json:"originalName"`
	ArchivedName string `json:"archivedName"`
	Moved        bool   `json:"moved"`
}

// recoveryManifest is the JSON manifest stored in each recovery archive
// directory. It records which files were archived, why, and whether the
// archive operation completed atomically.
type recoveryManifest struct {
	RunID       string         `json:"runId"`
	BasePath    string         `json:"basePath"`
	CreatedAt   time.Time      `json:"createdAt"`
	CompletedAt time.Time      `json:"completedAt,omitempty"`
	Status      string         `json:"status"`
	Reason      string         `json:"reason"`
	Files       []recoveryFile `json:"files"`
	FiltersHash string         `json:"filtersHash,omitempty"`
	PID         int            `json:"pid,omitempty"`
}

// generateRunID creates a unique identifier for a recovery archive run.
// It combines a timestamp with a short random suffix to avoid collisions
// when multiple archives happen in the same second.
func generateRunID() string {
	ts := time.Now().Format("20060102_150405")
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	suffix := hex.EncodeToString(randBytes)
	return fmt.Sprintf("%s_%s", ts, suffix)
}

// recoveryDirPath returns the full path to a recovery archive directory
// for the given basePath and runID.
func recoveryDirPath(basePath string, runID string) string {
	return basePath + recoveryDirPrefix + runID
}

// writeRecoveryManifest writes (or overwrites) the manifest for a recovery
// archive directory.
func writeRecoveryManifest(dir string, m *recoveryManifest) error {
	path := filepath.Join(dir, manifestFileName)
	tmpPath := path + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery manifest: %w", err)
	}
	if err = os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write recovery manifest tmp: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename recovery manifest: %w", err)
	}
	return nil
}

// readRecoveryManifest reads and parses the manifest from a recovery
// archive directory. Returns os.ErrNotExist if there is no manifest.
func readRecoveryManifest(dir string) (*recoveryManifest, error) {
	path := filepath.Join(dir, manifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m recoveryManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse recovery manifest: %w", err)
	}
	return &m, nil
}

// listStaleFiles returns the full paths of all stale prior-run files that
// should be archived. These are listings, queues, and error markers
// associated with the current basePath.
// listStaleFiles returns a list of stale prior-run files that should be
// archived to a recovery directory before starting a new bisync run.
//
// Stale files are those left behind by a FAILED prior run. Files from a
// SUCCESSFUL prior run (.lst, .lst-old, .lst-dry, .lst-dry-old) are NOT
// considered stale – they are needed for delta computation in the next run.
func (b *bisyncRun) listStaleFiles() []string {
	// Only check for files that indicate a FAILED prior run.
	// Successful prior-run files (.lst, .lst-old, .lst-dry, .lst-dry-old)
	// must remain in place for delta comparison.
	candidates := []string{
		b.listing1 + "-err",
		b.listing1 + "-new",
		b.listing1 + "-dry-new",
		b.listing2 + "-err",
		b.listing2 + "-new",
		b.listing2 + "-dry-new",
		b.basePath + ".copy1to2.que",
		b.basePath + ".copy2to1.que",
		b.basePath + ".delete1.que",
		b.basePath + ".delete2.que",
	}
	// Use a map to avoid duplicates when glob patterns overlap with explicit list
	seen := map[string]bool{}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			seen[p] = true
		}
	}
	// Also glob for any variant we might have missed.
	// Note: we use b.basePath + "." as prefix to avoid matching
	// files from other test runs that happen to have b.basePath
	// as an infix (e.g. "missing-listings.{basePath}.path1.lst-new").
	//
	// Only glob for failure-indicating files, not for successful-run files.
	globPatterns := []string{
		b.basePath + ".*.lst-err",
		b.basePath + ".*.lst-new",
		b.basePath + ".*.lst-dry-new",
		b.basePath + ".*.que",
	}
	for _, pat := range globPatterns {
		if ls, err := filepath.Glob(pat); err == nil {
			for _, f := range ls {
				seen[f] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for f := range seen {
		// Skip recovery directories themselves
		if info, err := os.Stat(f); err == nil && info.IsDir() {
			continue
		}
		result = append(result, f)
	}
	return result
}

// finalizeRecoveryArchive finishes a recovery archive that was left in
// "in_progress" state by a prior interrupted run. It moves any files that
// are still in their original location into the recovery directory and
// marks the archive complete.
//
// Returns true if the archive is complete (either was already complete
// or was successfully finalized).
func (b *bisyncRun) finalizeRecoveryArchive(dir string) bool {
	m, err := readRecoveryManifest(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No manifest – directory may have been partially created.
			// Since it's already isolated (in its own dir), just mark
			// it complete with what we can find.
			fs.Infof(nil, Color(terminal.YellowFg, "Recovery archive %q has no manifest; marking as-is complete"), dir)
			base := filepath.Base(dir)
			runID := ""
			if idx := len(base) - len(recoveryDirPrefix); idx > 0 && base[:idx] != "" {
				runID = base[idx+len(recoveryDirPrefix):]
			}
			m = &recoveryManifest{
				RunID:     runID,
				BasePath:  b.basePath,
				CreatedAt: time.Now(),
				Status:    recoveryStatusComplete,
				Reason:    "orphan-recovery-dir",
				Files:     []recoveryFile{},
				PID:       os.Getpid(),
			}
			// Scan directory for archived files
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if e.Name() == manifestFileName {
						continue
					}
					m.Files = append(m.Files, recoveryFile{
						ArchivedName: e.Name(),
						Moved:        true,
					})
				}
			}
			_ = writeRecoveryManifest(dir, m)
			return true
		}
		fs.Errorf(nil, "Cannot read recovery manifest in %q: %v", dir, err)
		return false
	}

	if m.Status == recoveryStatusComplete {
		return true
	}

	fs.Infof(nil, Color(terminal.YellowFg, "Resuming incomplete recovery archive %q (status: %s)"), dir, m.Status)

	allMoved := true
	for i, rf := range m.Files {
		if rf.Moved {
			continue
		}
		// Check if file is still in original location
		if _, err := os.Stat(rf.OriginalName); err != nil {
			if os.IsNotExist(err) {
				// File is gone – might have been moved already, or cleaned up.
				// Check if it's in the recovery dir.
				dst := filepath.Join(dir, rf.ArchivedName)
				if _, staterr := os.Stat(dst); staterr == nil {
					m.Files[i].Moved = true
					continue
				}
				// File missing from both places – skip but don't fail
				fs.Debugf(nil, "Recovery file %q missing from both original and archive locations", rf.OriginalName)
				m.Files[i].Moved = true // mark as done so we don't loop forever
				continue
			}
			allMoved = false
			continue
		}
		// Move it
		dst := filepath.Join(dir, rf.ArchivedName)
		if err := os.Rename(rf.OriginalName, dst); err != nil {
			fs.Errorf(nil, "Cannot resume archive of %q -> %q: %v", rf.OriginalName, dst, err)
			allMoved = false
			continue
		}
		m.Files[i].Moved = true
		fs.Infof(nil, Color(terminal.GreenFg, "Archived (resumed) %q -> %s"), rf.OriginalName, filepath.Base(dir))
	}

	if allMoved {
		m.Status = recoveryStatusComplete
		m.CompletedAt = time.Now()
		if err := writeRecoveryManifest(dir, m); err != nil {
			fs.Errorf(nil, "Cannot mark recovery archive %q complete: %v", dir, err)
			return false
		}
		fs.Infof(nil, Color(terminal.GreenFg, "Recovery archive %q finalized"), dir)
		return true
	}

	// Update manifest with whatever progress we made
	_ = writeRecoveryManifest(dir, m)
	return false
}

// findRecoveryDirs finds all recovery archive directories associated with
// the current basePath.
func (b *bisyncRun) findRecoveryDirs() []string {
	pattern := b.basePath + recoveryDirPrefix + "*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			dirs = append(dirs, m)
		}
	}
	return dirs
}

// ensureNoStaleBasePathFiles verifies that no stale listing/queue/error
// files are present in the current basePath. If any are found, they are
// moved into a fresh recovery archive.
//
// This is the main entry point called from setLockFile to guarantee a
// clean starting state before any delta computation.
func (b *bisyncRun) ensureNoStaleBasePathFiles(reason string) {
	// First, finalize any incomplete recovery archives from prior runs
	for _, dir := range b.findRecoveryDirs() {
		b.finalizeRecoveryArchive(dir)
	}

	// Now check if there are still stale files in the basePath that need archiving
	stale := b.listStaleFiles()
	if len(stale) == 0 {
		return
	}

	runID := generateRunID()
	dir := recoveryDirPath(b.basePath, runID)

	fs.Infof(nil, Color(terminal.GreenFg, "Archiving %d stale prior-run files into recovery archive %q (reason: %s)"), len(stale), filepath.Base(dir), reason)

	// Create recovery directory
	if err := os.Mkdir(dir, 0o700); err != nil {
		fs.Errorf(nil, "Cannot create recovery archive dir %q: %v", dir, err)
		return
	}

	// Build manifest with in_progress status
	files := make([]recoveryFile, 0, len(stale))
	for _, orig := range stale {
		archived := filepath.Base(orig)
		files = append(files, recoveryFile{
			OriginalName: orig,
			ArchivedName: archived,
			Moved:        false,
		})
	}
	manifest := &recoveryManifest{
		RunID:       runID,
		BasePath:    b.basePath,
		CreatedAt:   time.Now(),
		Status:      recoveryStatusInProgress,
		Reason:      reason,
		Files:       files,
		FiltersHash: "", // filled in later if available
		PID:         os.Getpid(),
	}

	if err := writeRecoveryManifest(dir, manifest); err != nil {
		fs.Errorf(nil, "Cannot write recovery manifest to %q: %v", dir, err)
		return
	}

	// Move files one by one, updating manifest after each success
	allOK := true
	for i, rf := range manifest.Files {
		dst := filepath.Join(dir, rf.ArchivedName)
		if err := os.Rename(rf.OriginalName, dst); err != nil {
			fs.Errorf(nil, "Cannot archive %q -> %q: %v", rf.OriginalName, dst, err)
			allOK = false
			continue
		}
		manifest.Files[i].Moved = true
		fs.Debugf(nil, "Archived %q -> %s", rf.OriginalName, filepath.Base(dir))
	}

	// Mark complete if all files moved
	if allOK {
		manifest.Status = recoveryStatusComplete
		manifest.CompletedAt = time.Now()
		if err := writeRecoveryManifest(dir, manifest); err != nil {
			fs.Errorf(nil, "Cannot mark recovery archive %q complete: %v", dir, err)
		} else {
			fs.Infof(nil, Color(terminal.GreenFg, "Recovery archive %q complete (%d files)"), filepath.Base(dir), len(manifest.Files))
		}
	} else {
		// Partial success – update manifest with progress; next run will resume
		_ = writeRecoveryManifest(dir, manifest)
		fs.Infof(nil, Color(terminal.YellowFg, "Recovery archive %q partially complete; will resume on next run"), filepath.Base(dir))
	}
}
