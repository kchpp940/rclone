package chunker

import (
	"testing"

	"github.com/rclone/rclone/fs/fspath"
	"github.com/stretchr/testify/assert"
)

func TestChunkerPathParsing(t *testing.T) {
	for _, test := range []struct {
		remote     string
		rpath      string
		wantParsed bool
		wantName   string
		wantPath   string
	}{
		{"remote:", "path", true, "remote", "path"},
		{"remote:base", "sub", true, "remote", "base/sub"},
		{"alias:crypt:", "data", true, "alias", "crypt:/data"},
		{"alias:crypt:base", "sub", true, "alias", "crypt:base/sub"},
		{"crypt:chunker:", "data", true, "crypt", "chunker:/data"},
		{"chunker:alias:crypt:", "data", true, "chunker", "alias:crypt:/data"},
		{"C:", "path", true, "C", "path"},
		{"C:base", "sub", true, "C", "base/sub"},
		{":s3,key=xxx:", "path", true, ":s3", "path"},
		{":s3,key=xxx:base", "sub", true, ":s3", "base/sub"},
		{"/local/path", "sub", false, "", "/local/path/sub"},
		{"./relative/path", "sub", false, "", "relative/path/sub"},
	} {
		remotePath := fspath.JoinRootPath(test.remote, test.rpath)
		parsed, err := fspath.Parse(remotePath)
		assert.NoError(t, err, test.remote)

		if test.wantParsed {
			assert.Equal(t, test.wantName, parsed.Name, "Name for %s + %s", test.remote, test.rpath)
			assert.Equal(t, test.wantPath, parsed.Path, "Path for %s + %s", test.remote, test.rpath)
		} else {
			assert.Equal(t, "", parsed.Name, "Name should be empty for %s + %s", test.remote, test.rpath)
			assert.Equal(t, test.wantPath, parsed.Path, "Path for %s + %s", test.remote, test.rpath)
		}
	}
}

func TestChunkerPathRoundTrip(t *testing.T) {
	for _, test := range []struct {
		remote string
		rpath  string
	}{
		{"remote:", "path/to/file"},
		{"remote:base", "sub/dir"},
		{"alias:crypt:", "data/file.txt"},
		{"alias:crypt:base", "sub/dir"},
		{"crypt:chunker:base", "sub/dir"},
		{"chunker:alias:crypt:base", "sub/dir"},
		{"C:", "path/to/file"},
		{"C:base", "sub/dir"},
	} {
		remotePath := fspath.JoinRootPath(test.remote, test.rpath)
		remoteName, remotePath2, err := fspath.SplitFs(remotePath)
		assert.NoError(t, err, test.remote)

		rejoined := remoteName + remotePath2
		assert.Equal(t, remotePath, rejoined, "Round trip failed for %s + %s", test.remote, test.rpath)

		parsed, err := fspath.Parse(test.remote)
		assert.NoError(t, err, test.remote)

		if parsed.Name != "" {
			firstChunkPath := parsed.Path + ".rclone_chunk.00001_000000000"
			firstChunkFullPath := fspath.JoinRootPath(test.remote, firstChunkPath)
			parsed2, err := fspath.Parse(firstChunkFullPath)
			assert.NoError(t, err, firstChunkFullPath)
			assert.Equal(t, parsed.Name, parsed2.Name, "Chunk path should preserve remote name for %s", test.remote)
		}
	}
}

func TestChunkerWrapperBackends(t *testing.T) {
	for _, test := range []struct {
		remote string
		want   string
	}{
		{"alias:crypt:path", "alias"},
		{"crypt:chunker:path", "crypt"},
		{"chunker:alias:crypt:path", "chunker"},
		{"alias:crypt:chunker:path", "alias"},
	} {
		parsed, err := fspath.Parse(test.remote)
		assert.NoError(t, err, test.remote)
		assert.Equal(t, test.want, parsed.Name, "Outer remote name for %s", test.remote)
	}
}
