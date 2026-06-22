package vfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"unsafe"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
	"github.com/rclone/rclone/fstest"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/rclone/rclone/fstest/mockobject"
	"github.com/rclone/rclone/vfs/vfscache/writeback"
	"github.com/rclone/rclone/vfs/vfscommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fileCreate(t *testing.T, mode vfscommon.CacheMode) (r *fstest.Run, vfs *VFS, fh *File, item fstest.Item) {
	opt := vfscommon.Opt
	opt.CacheMode = mode
	opt.WriteBack = writeBackDelay
	r, vfs = newTestVFSOpt(t, &opt)

	file1 := r.WriteObject(context.Background(), "dir/file1", "file1 contents", t1)
	r.CheckRemoteItems(t, file1)

	node, err := vfs.Stat("dir/file1")
	require.NoError(t, err)
	require.True(t, node.Mode().IsRegular())

	return r, vfs, node.(*File), file1
}

func TestFileMethods(t *testing.T) {
	r, vfs, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	// String
	assert.Equal(t, "dir/file1", file.String())
	assert.Equal(t, "<nil *File>", (*File)(nil).String())

	// IsDir
	assert.Equal(t, false, file.IsDir())

	// IsFile
	assert.Equal(t, true, file.IsFile())

	// Mode
	assert.Equal(t, os.FileMode(vfs.Opt.FilePerms), file.Mode())

	// Name
	assert.Equal(t, "file1", file.Name())

	// Path
	assert.Equal(t, "dir/file1", file.Path())

	// Sys
	assert.Equal(t, nil, file.Sys())

	// SetSys
	file.SetSys(42)
	assert.Equal(t, 42, file.Sys())

	// Inode
	assert.NotEqual(t, uint64(0), file.Inode())

	// Node
	assert.Equal(t, file, file.Node())

	// ModTime
	assert.WithinDuration(t, t1, file.ModTime(), r.Fremote.Precision())

	// Size
	assert.Equal(t, int64(14), file.Size())

	// Sync
	assert.NoError(t, file.Sync())

	// DirEntry
	assert.Equal(t, file.o, file.DirEntry())

	// Dir
	assert.Equal(t, file.d, file.Dir())

	// VFS
	assert.Equal(t, vfs, file.VFS())
}

func testFileSetModTime(t *testing.T, cacheMode vfscommon.CacheMode, open bool, write bool) {
	if !canSetModTimeValue {
		t.Skip("can't set mod time")
	}
	r, vfs, file, file1 := fileCreate(t, cacheMode)
	if !canSetModTime(t, r) {
		t.Skip("can't set mod time")
	}

	var (
		err      error
		fd       Handle
		contents = "file1 contents"
	)
	if open {
		// Open with write intent
		if cacheMode != vfscommon.CacheModeOff {
			fd, err = file.Open(os.O_WRONLY)
			if write {
				contents = "hello contents"
			}
		} else {
			// Can't write without O_TRUNC with CacheMode Off
			fd, err = file.Open(os.O_WRONLY | os.O_TRUNC)
			if write {
				contents = "hello"
			} else {
				contents = ""
			}
		}
		require.NoError(t, err)

		// Write some data
		if write {
			_, err = fd.WriteString("hello")
			require.NoError(t, err)
		}
	}

	err = file.SetModTime(t2)
	require.NoError(t, err)

	if open {
		require.NoError(t, fd.Close())
		vfs.WaitForWriters(waitForWritersDelay)
	}

	file1 = fstest.NewItem(file1.Path, contents, t2)
	r.CheckRemoteItems(t, file1)

	vfs.Opt.ReadOnly = true
	err = file.SetModTime(t2)
	assert.Equal(t, EROFS, err)
}

// Test various combinations of setting mod times with and
// without the cache and with and without opening or writing
// to the file.
//
// Each of these tests a different path through the VFS code.
func TestFileSetModTime(t *testing.T) {
	for _, cacheMode := range []vfscommon.CacheMode{vfscommon.CacheModeOff, vfscommon.CacheModeFull} {
		for _, open := range []bool{false, true} {
			for _, write := range []bool{false, true} {
				if write && !open {
					continue
				}
				t.Run(fmt.Sprintf("cache=%v,open=%v,write=%v", cacheMode, open, write), func(t *testing.T) {
					testFileSetModTime(t, cacheMode, open, write)
				})
			}
		}
	}
}

func fileCheckContents(t *testing.T, file *File) {
	fd, err := file.Open(os.O_RDONLY)
	require.NoError(t, err)

	contents, err := io.ReadAll(fd)
	require.NoError(t, err)
	assert.Equal(t, "file1 contents", string(contents))

	require.NoError(t, fd.Close())
}

func TestFileOpenRead(t *testing.T) {
	_, _, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	fileCheckContents(t, file)
}

func TestFileOpenReadUnknownSize(t *testing.T) {
	var (
		contents = []byte("file contents")
		remote   = "file.txt"
		ctx      = context.Background()
	)

	// create a mock object which returns size -1
	o := mockobject.New(remote).WithContent(contents, mockobject.SeekModeNone)
	o.SetUnknownSize(true)
	assert.Equal(t, int64(-1), o.Size())

	// add it to a mock fs
	fMock, err := mockfs.NewFs(context.Background(), "test", "root", nil)
	require.NoError(t, err)
	f := fMock.(*mockfs.Fs)
	f.AddObject(o)
	testObj, err := f.NewObject(ctx, remote)
	require.NoError(t, err)
	assert.Equal(t, int64(-1), testObj.Size())

	// create a VFS from that mockfs
	vfs := New(context.Background(), f, nil)
	defer cleanupVFS(t, vfs)

	// find the file
	node, err := vfs.Stat(remote)
	require.NoError(t, err)
	require.True(t, node.IsFile())
	file := node.(*File)

	// open it
	fd, err := file.openRead()
	require.NoError(t, err)
	assert.Equal(t, int64(0), fd.Size())

	// check the contents are not empty even though size is empty
	gotContents, err := io.ReadAll(fd)
	require.NoError(t, err)
	assert.Equal(t, contents, gotContents)
	t.Logf("gotContents = %q", gotContents)

	// check that file size has been updated
	assert.Equal(t, int64(len(contents)), fd.Size())

	require.NoError(t, fd.Close())
}

func TestFileOpenWrite(t *testing.T) {
	_, vfs, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	fd, err := file.openWrite(os.O_WRONLY | os.O_TRUNC)
	require.NoError(t, err)

	newContents := []byte("this is some new contents")
	n, err := fd.Write(newContents)
	require.NoError(t, err)
	assert.Equal(t, len(newContents), n)
	require.NoError(t, fd.Close())

	assert.Equal(t, int64(25), file.Size())

	vfs.Opt.ReadOnly = true
	_, err = file.openWrite(os.O_WRONLY | os.O_TRUNC)
	assert.Equal(t, EROFS, err)
}

func TestFileRemove(t *testing.T) {
	r, vfs, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	err := file.Remove()
	require.NoError(t, err)

	r.CheckRemoteItems(t)

	vfs.Opt.ReadOnly = true
	err = file.Remove()
	assert.Equal(t, EROFS, err)
}

func TestFileRemoveAll(t *testing.T) {
	r, vfs, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	err := file.RemoveAll()
	require.NoError(t, err)

	r.CheckRemoteItems(t)

	vfs.Opt.ReadOnly = true
	err = file.RemoveAll()
	assert.Equal(t, EROFS, err)
}

func TestFileOpen(t *testing.T) {
	_, _, file, _ := fileCreate(t, vfscommon.CacheModeOff)

	fd, err := file.Open(os.O_RDONLY)
	require.NoError(t, err)
	_, ok := fd.(*ReadFileHandle)
	assert.True(t, ok)
	require.NoError(t, fd.Close())

	fd, err = file.Open(os.O_WRONLY)
	assert.NoError(t, err)
	_, ok = fd.(*WriteFileHandle)
	assert.True(t, ok)
	require.NoError(t, fd.Close())

	fd, err = file.Open(os.O_RDWR)
	assert.NoError(t, err)
	_, ok = fd.(*WriteFileHandle)
	assert.True(t, ok)
	require.NoError(t, fd.Close())

	_, err = file.Open(3)
	assert.Equal(t, EPERM, err)
}

func testFileRename(t *testing.T, mode vfscommon.CacheMode, inCache bool, forceCache bool) {
	r, vfs, file, item := fileCreate(t, mode)

	if !operations.CanServerSideMove(r.Fremote) {
		t.Skip("skip as can't rename files")
	}

	rootDir, err := vfs.Root()
	require.NoError(t, err)

	// force the file into the cache if required
	if forceCache {
		// write the file with read and write
		fd, err := file.Open(os.O_RDWR | os.O_CREATE | os.O_TRUNC)
		require.NoError(t, err)

		n, err := fd.Write([]byte("file1 contents"))
		require.NoError(t, err)
		require.Equal(t, 14, n)

		require.NoError(t, file.SetModTime(item.ModTime))

		err = fd.Close()
		require.NoError(t, err)
	}
	vfs.WaitForWriters(waitForWritersDelay)

	// check file in cache
	if inCache {
		// read contents to get file in cache
		fileCheckContents(t, file)
		assert.True(t, vfs.cache.Exists(item.Path))
	}

	dir := file.Dir()

	// start with "dir/file1"
	r.CheckRemoteItems(t, item)

	// rename file to "newLeaf"
	err = dir.Rename("file1", "newLeaf", rootDir)
	require.NoError(t, err)

	item.Path = "newLeaf"
	r.CheckRemoteItems(t, item)

	// check file in cache
	if inCache {
		assert.True(t, vfs.cache.Exists(item.Path))
	}

	// check file exists in the vfs layer at its new name
	_, err = vfs.Stat("newLeaf")
	require.NoError(t, err)

	// rename it back to "dir/file1"
	err = rootDir.Rename("newLeaf", "file1", dir)
	require.NoError(t, err)

	item.Path = "dir/file1"
	r.CheckRemoteItems(t, item)

	// check file in cache
	if inCache {
		assert.True(t, vfs.cache.Exists(item.Path))
	}

	// now try renaming it with the file open
	// first open it and write to it but don't close it
	fd, err := file.Open(os.O_WRONLY | os.O_TRUNC)
	require.NoError(t, err)
	newContents := []byte("this is some new contents")
	_, err = fd.Write(newContents)
	require.NoError(t, err)

	// rename file to "newLeaf"
	err = dir.Rename("file1", "newLeaf", rootDir)
	require.NoError(t, err)
	newItem := fstest.NewItem("newLeaf", string(newContents), item.ModTime)

	// check file has been renamed at VFS layer immediately (POSIX semantics)
	// but cache item remains at old path until pending rename commits
	if inCache {
		// During pending rename, cache item is still at the old path
		assert.True(t, vfs.cache.Exists(item.Path))
		assert.False(t, vfs.cache.Exists("newLeaf"))
	}

	// check file exists in the vfs layer at its new name
	_, err = vfs.Stat("newLeaf")
	require.NoError(t, err)

	// Close the file - this triggers applyPendingRename which commits
	// the backend rename (cache item, writeback queue, virtual dir entries)
	require.NoError(t, fd.Close())

	// After close, cache item should have moved to the new path
	if inCache {
		vfs.WaitForWriters(waitForWritersDelay)
		assert.True(t, vfs.cache.Exists("newLeaf"))
		assert.False(t, vfs.cache.Exists(item.Path))
	}

	// Check file has now been renamed on the remote
	item.Path = "newLeaf"
	vfs.WaitForWriters(waitForWritersDelay)
	fstest.CheckListingWithPrecision(t, r.Fremote, []fstest.Item{newItem}, nil, fs.ModTimeNotSupported)
}

func TestFileRename(t *testing.T) {
	for _, test := range []struct {
		mode       vfscommon.CacheMode
		inCache    bool
		forceCache bool
	}{
		{mode: vfscommon.CacheModeOff, inCache: false},
		{mode: vfscommon.CacheModeMinimal, inCache: false},
		{mode: vfscommon.CacheModeMinimal, inCache: true, forceCache: true},
		{mode: vfscommon.CacheModeWrites, inCache: false},
		{mode: vfscommon.CacheModeWrites, inCache: true, forceCache: true},
		{mode: vfscommon.CacheModeFull, inCache: true},
	} {
		t.Run(fmt.Sprintf("%v,forceCache=%v", test.mode, test.forceCache), func(t *testing.T) {
			testFileRename(t, test.mode, test.inCache, test.forceCache)
		})
	}
}

func TestFileStructSize(t *testing.T) {
	t.Logf("File struct has size %d bytes", unsafe.Sizeof(File{}))
}

// TestFileRenamePendingRollback tests that when a pending rename fails,
// the VFS node state and directory entries are rolled back to the old path.
func TestFileRenamePendingRollback(t *testing.T) {
	for _, mode := range []vfscommon.CacheMode{
		vfscommon.CacheModeWrites,
		vfscommon.CacheModeFull,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			r, vfs, file, _ := fileCreate(t, mode)

			if !operations.CanServerSideMove(r.Fremote) {
				t.Skip("skip as can't rename files")
			}

			rootDir, err := vfs.Root()
			require.NoError(t, err)
			dir := file.Dir()

			// Write some data to make the file dirty
			fd, err := file.Open(os.O_RDWR | os.O_CREATE | os.O_TRUNC)
			require.NoError(t, err)
			_, err = fd.Write([]byte("file contents"))
			require.NoError(t, err)

			// Rename while file is open - this creates a pending rename
			err = dir.Rename("file1", "newLeaf", rootDir)
			require.NoError(t, err)

			// Verify VFS layer shows the new name immediately
			_, err = vfs.Stat("newLeaf")
			require.NoError(t, err)
			_, err = vfs.Stat("dir/file1")
			require.Error(t, err) // old path should not exist

			// Verify cache is still at old path (pending rename not committed)
			require.True(t, vfs.cache.Exists("dir/file1"))
			require.False(t, vfs.cache.Exists("newLeaf"))

			// Verify pending rename state exists
			require.True(t, file.HasPendingRename())

			// Now inject a failing pendingRenameFun to simulate backend rename failure
			renameErr := fmt.Errorf("simulated backend rename failure")
			file.mu.Lock()
			origPendingRenameFun := file.pendingRenameFun
			origPendingRename := file.pendingRename
			file.pendingRenameFun = func(ctx context.Context) (fs.Object, error) {
				return nil, renameErr
			}
			file.mu.Unlock()

			// Close the file - this triggers applyPendingRename which should fail and rollback
			require.NoError(t, fd.Close())
			vfs.WaitForWriters(waitForWritersDelay)

			// After rollback, VFS layer should show the old name again
			_, err = vfs.Stat("dir/file1")
			require.NoError(t, err)
			_, err = vfs.Stat("newLeaf")
			require.Error(t, err) // new path should not exist after rollback

			// Cache should still be at old path
			require.True(t, vfs.cache.Exists("dir/file1"))
			require.False(t, vfs.cache.Exists("newLeaf"))

			// File node should be back at old path
			require.Equal(t, "dir/file1", file.Path())
			require.Equal(t, "file1", file.Name())

			// Pending rename state should still exist for retry
			require.True(t, file.HasPendingRename())

			// Restore original pending rename function and let it succeed
			file.mu.Lock()
			file.pendingRenameFun = origPendingRenameFun
			file.pendingRename = origPendingRename
			file.mu.Unlock()

			// Trigger applyPendingRename again - should succeed now
			file.applyPendingRename()
			vfs.WaitForWriters(waitForWritersDelay)

			// After successful commit, VFS layer should show new name
			_, err = vfs.Stat("newLeaf")
			require.NoError(t, err)
			_, err = vfs.Stat("dir/file1")
			require.Error(t, err)

			// Cache should be at new path now
			require.True(t, vfs.cache.Exists("newLeaf"))
			require.False(t, vfs.cache.Exists("dir/file1"))

			// File node should be at new path
			require.Equal(t, "newLeaf", file.Path())
			require.Equal(t, "newLeaf", file.Name())

			// Pending rename state should be cleared
			require.False(t, file.HasPendingRename())
		})
	}
}

// TestFileTruncateWritebackSize tests that after truncating a dirty file,
// the writeback queue size is updated and old in-progress uploads are cancelled.
// This verifies that we never upload stale size content.
func TestFileTruncateWritebackSize(t *testing.T) {
	for _, mode := range []vfscommon.CacheMode{
		vfscommon.CacheModeWrites,
		vfscommon.CacheModeFull,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			r, vfs, file, _ := fileCreate(t, mode)
			_ = r
			itemPath := file.Path()

			// Write a larger amount of data
			h, err := file.Open(os.O_WRONLY)
			require.NoError(t, err)
			largeData := make([]byte, 100*1024)
			for i := range largeData {
				largeData[i] = 'A'
			}
			_, err = h.Write(largeData)
			require.NoError(t, err)
			err = h.Close()
			require.NoError(t, err)

			// Verify cache item exists at 100KB size
			require.True(t, vfs.cache.Exists(itemPath))
			isDirty, dirtySize := vfs.cache.StatDirty(itemPath)
			require.True(t, isDirty)
			require.Equal(t, int64(100*1024), dirtySize)

			// Check queue has the item with 100KB size
			queueOut := vfs.cache.Queue()
			queue, ok := queueOut["queue"].([]writeback.QueueInfo)
			require.True(t, ok)
			found := false
			for _, qi := range queue {
				if qi.Name == itemPath {
					require.Equal(t, int64(100*1024), qi.Size, "queued size should be 100KB before truncate")
					found = true
				}
			}
			require.True(t, found, "item should be in upload queue before truncate")

			// Re-open and truncate to 10KB
			h, err = file.Open(os.O_WRONLY)
			require.NoError(t, err)
			err = h.Truncate(10 * 1024)
			require.NoError(t, err)
			err = h.Close()
			require.NoError(t, err)

			// Verify queue size is updated to 10KB
			queueOut = vfs.cache.Queue()
			queue, ok = queueOut["queue"].([]writeback.QueueInfo)
			require.True(t, ok)
			found = false
			for _, qi := range queue {
				if qi.Name == itemPath {
					require.Equal(t, int64(10*1024), qi.Size, "queued size should be updated to 10KB after truncate")
					found = true
				}
			}
			require.True(t, found, "item should still be in upload queue after truncate")

			// Wait for writeback to complete and verify file size on remote
			vfs.WaitForWriters(waitForWritersDelay)
			stat, err := vfs.Stat(itemPath)
			require.NoError(t, err)
			require.Equal(t, int64(10*1024), stat.Size())
		})
	}
}

// TestFileRenameTruncateClose tests the interleaved scenario:
// 1. Open writer, write data
// 2. While writer is open, rename file (triggers pending rename)
// 3. While writer is still open, truncate the file
// 4. Close writer -> apply pending rename + upload new size
//
// This verifies that rename+truncate+close interleaving leaves
// consistent VFS state, cache files, and writeback queue entries.
func TestFileRenameTruncateClose(t *testing.T) {
	for _, mode := range []vfscommon.CacheMode{
		vfscommon.CacheModeWrites,
		vfscommon.CacheModeFull,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			r, vfs, file, _ := fileCreate(t, mode)

			if !operations.CanServerSideMove(r.Fremote) {
				t.Skip("skip as can't rename files")
			}

			rootDir, err := vfs.Root()
			require.NoError(t, err)
			dir := file.Dir()

			oldPath := file.Path()
			oldLeaf := file.Name()

			// Step 1: Open writer and write some data (50KB)
			h, err := file.Open(os.O_WRONLY | os.O_TRUNC)
			require.NoError(t, err)
			initialData := make([]byte, 50*1024)
			for i := range initialData {
				initialData[i] = 'B'
			}
			_, err = h.Write(initialData)
			require.NoError(t, err)

			// Step 2: Rename while writer is open -> pending rename
			newLeaf := "renamed_truncated_file"
			err = dir.Rename(oldLeaf, newLeaf, rootDir)
			require.NoError(t, err)
			newPath := file.Path()
			require.Equal(t, newLeaf, file.Name())

			// VFS should show new path immediately
			_, err = vfs.Stat(newLeaf)
			require.NoError(t, err)
			_, err = vfs.Stat(oldPath)
			require.Error(t, err)

			// Pending rename active
			require.True(t, file.HasPendingRename())

			// Cache item still at old path (pending rename not applied)
			require.True(t, vfs.cache.Exists(oldPath))
			require.False(t, vfs.cache.Exists(newPath))

			// Step 3: Truncate while still have open writer + pending rename
			err = h.Truncate(20 * 1024) // Shrink from 50KB to 20KB
			require.NoError(t, err)

			// Step 4: Close writer -> triggers applyPendingRename + queued upload
			err = h.Close()
			require.NoError(t, err)

			// Wait for pending rename to be applied
			vfs.WaitForWriters(waitForWritersDelay)

			// After apply: pending rename cleared
			require.False(t, file.HasPendingRename())

			// Cache item moved to new path
			require.False(t, vfs.cache.Exists(oldPath))
			require.True(t, vfs.cache.Exists(newPath))

			// VFS layer still shows new path
			_, err = vfs.Stat(newLeaf)
			require.NoError(t, err)
			_, err = vfs.Stat(oldPath)
			require.Error(t, err)

			// Check queue shows new path (not old) and correct size
			queueOut := vfs.cache.Queue()
			if queue, ok := queueOut["queue"].([]writeback.QueueInfo); ok {
				for _, qi := range queue {
					require.NotEqual(t, oldPath, qi.Name, "queue should never reference old path after rename applied")
					if qi.Name == newPath {
						require.Equal(t, int64(20*1024), qi.Size)
					}
				}
			}

			// Wait for writeback to complete
			vfs.WaitForWriters(waitForWritersDelay)

			// Final state: file exists at new path with 20KB size
			stat, err := vfs.Stat(newLeaf)
			require.NoError(t, err)
			require.Equal(t, int64(20*1024), stat.Size())
		})
	}
}
