package bisync

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rclone/rclone/cmd/bisync/bilib"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/terminal"
)

const basicallyforever = fs.Duration(200 * 365 * 24 * time.Hour)

type lockFileOpt struct {
	stopRenewal func()
	data        struct {
		Session     string
		PID         string
		TimeRenewed time.Time
		TimeExpires time.Time
	}
}

func (b *bisyncRun) setLockFile() (err error) {
	b.lockFile = ""
	b.setLockFileExpiration()
	if !b.opt.DryRun {
		b.lockFile = b.basePath + ".lck"
		if bilib.FileExists(b.lockFile) {
			if !b.lockFileIsExpired() {
				errTip := Color(terminal.MagentaFg, "Tip: this indicates that another bisync run (of these same paths) either is still running or was interrupted before completion. \n")
				errTip += Color(terminal.MagentaFg, "If you're SURE you want to override this safety feature, you can delete the lock file with the following command, then run bisync again: \n")
				errTip += fmt.Sprintf(Color(terminal.HiRedFg, "rclone deletefile \"%s\""), b.lockFile)
				return fmt.Errorf(Color(terminal.RedFg, "prior lock file found: %s \n")+errTip, Color(terminal.HiYellowFg, b.lockFile))
			}
			// Lock file was expired - archive its stale temp files into a
			// timestamped recovery archive directory with a manifest. This is
			// an atomic batch operation: if interrupted mid-archive, the
			// next run will resume and finalize it.
			fs.Infof(nil, Color(terminal.GreenFg, "Archiving stale files from expired/interrupted prior run"))
			b.ensureNoStaleBasePathFiles("lock-expired")
			// Also remove the expired lock file itself so we can recreate it cleanly
			_ = os.Remove(b.lockFile)
		}

		pidStr := []byte(strconv.Itoa(os.Getpid()))
		if err = os.WriteFile(b.lockFile, pidStr, bilib.PermSecure); err != nil {
			return fmt.Errorf(Color(terminal.RedFg, "cannot create lock file: %s: %w"), b.lockFile, err)
		}
		fs.Debugf(nil, "Lock file created: %s", b.lockFile)
		b.renewLockFile()
		b.lockFileOpt.stopRenewal = b.startLockRenewal()
	}

	// ---- UNCONDITIONAL RECOVERY CHECK ----
	// Before proceeding with any listing/delta computation, we must ensure
	// no half-finished recovery archives are left dangling, and that no
	// stale prior-run files remain in the basePath. The ensureNoStaleBasePathFiles
	// call:
	//   1. Scans for any recovery directories with in_progress manifests
	//   2. Resumes moving any remaining stale files into those directories
	//   3. Marks the manifests complete
	//   4. If new stale files are found (e.g. .lst-err from a failed run),
	//      creates a fresh recovery archive for them
	//
	// This is the critical barrier that guarantees old queued deltas,
	// old filter hashes, or old .lst-err markers never leak into the
	// current run's delta/listing computations.
	hasFailedMarker := bilib.FileExists(b.listing1+"-err") || bilib.FileExists(b.listing2+"-err")
	if hasFailedMarker {
		fs.Infof(nil, Color(terminal.GreenFg, "Detected failed-run marker; archiving stale listing/queue artifacts from prior run"))
		b.ensureNoStaleBasePathFiles("failed-run-detected")
	} else {
		// Even without a failed-run marker, there may be in-progress
		// recovery archives from a mid-archive interruption. Finalize
		// those first.
		for _, dir := range b.findRecoveryDirs() {
			b.finalizeRecoveryArchive(dir)
		}
	}

	// Double-check after recovery: no stale files should be left in basePath.
	// If any are still present (shouldn't happen, but defensive), do a
	// final sweep.
	remaining := b.listStaleFiles()
	if len(remaining) > 0 {
		fs.Infof(nil, Color(terminal.YellowFg, "%d stale files still present after recovery pass; final archive sweep"), len(remaining))
		b.ensureNoStaleBasePathFiles("final-sweep")
	}

	return nil
}

func (b *bisyncRun) removeLockFile() (err error) {
	if b.lockFile != "" {
		b.lockFileOpt.stopRenewal()
		err = os.Remove(b.lockFile)
		if err == nil {
			fs.Debugf(nil, "Lock file removed: %s", b.lockFile)
		} else {
			fs.Errorf(nil, "cannot remove lockfile %s: %v", b.lockFile, err)
		}
		b.lockFile = "" // block removing it again
	}
	return err
}

func (b *bisyncRun) setLockFileExpiration() {
	if b.opt.MaxLock > 0 && b.opt.MaxLock < fs.Duration(2*time.Minute) {
		fs.Logf(nil, Color(terminal.YellowFg, "--max-lock cannot be shorter than 2 minutes (unless 0.) Changing --max-lock from %v to %v"), b.opt.MaxLock, 2*time.Minute)
		b.opt.MaxLock = fs.Duration(2 * time.Minute)
	} else if b.opt.MaxLock <= 0 {
		b.opt.MaxLock = basicallyforever
	}
}

func (b *bisyncRun) renewLockFile() {
	if b.lockFile != "" && bilib.FileExists(b.lockFile) {

		b.lockFileOpt.data.Session = b.basePath
		b.lockFileOpt.data.PID = strconv.Itoa(os.Getpid())
		b.lockFileOpt.data.TimeRenewed = time.Now()
		b.lockFileOpt.data.TimeExpires = time.Now().Add(time.Duration(b.opt.MaxLock))

		// save data file
		df, err := os.Create(b.lockFile)
		b.handleErr(b.lockFile, "error renewing lock file", err, true, true)
		b.handleErr(b.lockFile, "error encoding JSON to lock file", json.NewEncoder(df).Encode(b.lockFileOpt.data), true, true)
		b.handleErr(b.lockFile, "error closing lock file", df.Close(), true, true)
		if b.opt.MaxLock < basicallyforever {
			fs.Infof(nil, Color(terminal.HiBlueFg, "lock file renewed for %v. New expiration: %v"), b.opt.MaxLock, b.lockFileOpt.data.TimeExpires)
		}
	}
}

func (b *bisyncRun) lockFileIsExpired() bool {
	if b.lockFile != "" && bilib.FileExists(b.lockFile) {
		rdf, err := os.Open(b.lockFile)
		b.handleErr(b.lockFile, "error reading lock file", err, true, true)
		dec := json.NewDecoder(rdf)
		var decodeErr error
		for {
			if err := dec.Decode(&b.lockFileOpt.data); err != nil {
				if err != io.EOF {
					decodeErr = err
				}
				break
			}
		}
		b.handleErr(b.lockFile, "error closing file", rdf.Close(), true, true)
		if decodeErr != nil {
			if b.opt.MaxLock < basicallyforever {
				fs.Infof(b.lockFile, Color(terminal.YellowFg, "Lock file is unreadable (decode error: %v) and --max-lock is set. Treating as expired."), decodeErr)
				markFailed(b.listing1)
				markFailed(b.listing2)
				return true
			}
			fs.Errorf(b.lockFile, Color(terminal.RedFg, "Lock file exists, but contents are unreadable. (decode error: %v)"), decodeErr)
			return false
		}
		if !b.lockFileOpt.data.TimeExpires.IsZero() && b.lockFileOpt.data.TimeExpires.Before(time.Now()) {
			fs.Infof(b.lockFile, Color(terminal.GreenFg, "Lock file found, but it expired at %v. Will delete it and proceed."), b.lockFileOpt.data.TimeExpires)
			markFailed(b.listing1) // listing is untrusted so force revert to prior (if --recover) or create new ones (if --resync)
			markFailed(b.listing2)
			return true
		}
		fs.Infof(b.lockFile, Color(terminal.RedFg, "Valid lock file found. Expires at %v. (%v from now)"), b.lockFileOpt.data.TimeExpires, time.Since(b.lockFileOpt.data.TimeExpires).Abs().Round(time.Second))
		prettyprint(b.lockFileOpt.data, "Lockfile info", fs.LogLevelInfo)
	}
	return false
}

// StartLockRenewal renews the lockfile every --max-lock minus one minute.
//
// It returns a func which should be called to stop the renewal.
func (b *bisyncRun) startLockRenewal() func() {
	if b.opt.MaxLock <= 0 || b.opt.MaxLock >= basicallyforever || b.lockFile == "" {
		return func() {}
	}
	stopLockRenewal := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(time.Duration(b.opt.MaxLock) - time.Minute)
		for {
			select {
			case <-ticker.C:
				b.renewLockFile()
			case <-stopLockRenewal:
				ticker.Stop()
				return
			}
		}
	})
	return func() {
		close(stopLockRenewal)
		wg.Wait()
	}
}

// markFailed renames the current listing file to an error marker so the
// next run knows the previous run failed. It deliberately preserves
// sibling files (-new, -old, -dry, -err, .que) so they can be
// inspected for diagnosis and will be cleaned up by the next run's
// setLockFile() / resync cleanup instead of being wiped eagerly.
func markFailed(file string) {
	failFile := file + "-err"
	if bilib.FileExists(file) {
		_ = os.Remove(failFile)
		_ = os.Rename(file, failFile)
	}
}

// archiveStaleFiles moves any leftover temporary files from a prior
// interrupted/failed run into a recovery archive directory with a
// manifest. This is a compatibility wrapper around ensureNoStaleBasePathFiles.
//
// Deprecated: use ensureNoStaleBasePathFiles directly for clearer semantics.
func (b *bisyncRun) archiveStaleFiles() {
	b.ensureNoStaleBasePathFiles("legacy-archive")
}

// cleanupStaleFiles removes any leftover temporary files from a prior interrupted run
// Deprecated: use ensureNoStaleBasePathFiles() instead to preserve diagnostics
func (b *bisyncRun) cleanupStaleFiles() {
	b.ensureNoStaleBasePathFiles("legacy-cleanup")
}
