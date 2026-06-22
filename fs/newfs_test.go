package fs_test

import (
	"context"
	"runtime"
	"testing"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fspath"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFs(t *testing.T) {
	ctx := context.Background()

	// Register mockfs temporarily
	oldRegistry := fs.Registry
	mockfs.Register()
	defer func() {
		fs.Registry = oldRegistry
	}()

	f1, err := fs.NewFs(ctx, ":mockfs:/tmp")
	require.NoError(t, err)
	assert.Equal(t, ":mockfs", f1.Name())
	assert.Equal(t, "/tmp", f1.Root())

	assert.Equal(t, ":mockfs:/tmp", fs.ConfigString(f1))

	f2, err := fs.NewFs(ctx, ":mockfs,potato:/tmp")
	require.NoError(t, err)
	assert.Equal(t, ":mockfs{S_NHG}", f2.Name())
	assert.Equal(t, "/tmp", f2.Root())

	assert.Equal(t, ":mockfs{S_NHG}:/tmp", fs.ConfigString(f2))
	assert.Equal(t, ":mockfs,potato='true':/tmp", fs.ConfigStringFull(f2))

	f3, err := fs.NewFs(ctx, ":mockfs,potato='true':/tmp")
	require.NoError(t, err)
	assert.Equal(t, ":mockfs{S_NHG}", f3.Name())
	assert.Equal(t, "/tmp", f3.Root())

	assert.Equal(t, ":mockfs{S_NHG}:/tmp", fs.ConfigString(f3))
	assert.Equal(t, ":mockfs,potato='true':/tmp", fs.ConfigStringFull(f3))

	// Check that the overrides work
	globalCI := fs.GetConfig(ctx)
	original := globalCI.UserAgent
	defer func() {
		globalCI.UserAgent = original
	}()

	f4, err := fs.NewFs(ctx, ":mockfs,global.user_agent='julian':/tmp")
	require.NoError(t, err)
	assert.Equal(t, ":mockfs", f4.Name())
	assert.Equal(t, "/tmp", f4.Root())

	assert.Equal(t, ":mockfs:/tmp", fs.ConfigString(f4))
	assert.Equal(t, ":mockfs:/tmp", fs.ConfigStringFull(f4))

	assert.Equal(t, "julian", globalCI.UserAgent)
}

func TestResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	defer func() { fs.ConfigFileGetSectionNames = oldGetSectionNames }()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"foo", "alias", "crypt", "chunker", "C"}
	}

	for _, test := range []struct {
		in         string
		wantName   string
		wantPath   string
		wantConfig string
		win        bool
		noWin      bool
	}{
		{
			in:         "foo:bar",
			wantName:   "foo",
			wantPath:   "bar",
			wantConfig: "foo",
		}, {
			in:         "unknown:path",
			wantName:   "",
			wantPath:   "unknown:path",
			wantConfig: "",
		}, {
			in:         "./foo:bar",
			wantName:   "",
			wantPath:   "./foo:bar",
			wantConfig: "",
		}, {
			in:         "/foo:bar",
			wantName:   "",
			wantPath:   "/foo:bar",
			wantConfig: "",
		}, {
			in:         "//server/share/path",
			wantName:   "",
			wantPath:   "//server/share/path",
			wantConfig: "",
		}, {
			in:         "C:",
			wantName:   "C",
			wantPath:   "",
			wantConfig: "C",
			noWin:      true,
		}, {
			in:         "C:",
			wantName:   "",
			wantPath:   "C:",
			wantConfig: "",
			win:        true,
		}, {
			in:         "C:file.txt",
			wantName:   "C",
			wantPath:   "file.txt",
			wantConfig: "C",
			noWin:      true,
		}, {
			in:         "C:file.txt",
			wantName:   "",
			wantPath:   "C:file.txt",
			wantConfig: "",
			win:        true,
		}, {
			in:         "alias:crypt:path",
			wantName:   "alias",
			wantPath:   "crypt:path",
			wantConfig: "alias",
		}, {
			in:         "crypt:chunker:data",
			wantName:   "crypt",
			wantPath:   "chunker:data",
			wantConfig: "crypt",
		}, {
			in:         ":s3,key=xxx:",
			wantName:   ":s3",
			wantPath:   "",
			wantConfig: ":s3,key=xxx",
		}, {
			in:         "/local/path",
			wantName:   "",
			wantPath:   "/local/path",
			wantConfig: "",
		}, {
			in:         "relative/path",
			wantName:   "",
			wantPath:   "relative/path",
			wantConfig: "",
		},
	} {
		if runtime.GOOS == "windows" && test.noWin {
			continue
		}
		if runtime.GOOS != "windows" && test.win {
			continue
		}
		parsed, err := fs.ResolvePath(test.in)
		require.NoError(t, err, test.in)
		assert.Equal(t, test.wantName, parsed.Name, "Name for %q", test.in)
		assert.Equal(t, test.wantPath, parsed.Path, "Path for %q", test.in)
		assert.Equal(t, test.wantConfig, parsed.ConfigString, "ConfigString for %q", test.in)
	}
}

func TestResolvePathWithCaches(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	oldHasSection := fs.ConfigFileHasSection
	defer func() {
		fs.ConfigFileGetSectionNames = oldGetSectionNames
		fs.ConfigFileHasSection = oldHasSection
	}()

	configuredRemotes := map[string]bool{
		"foo":     true,
		"alias":   true,
		"crypt":   true,
		"chunker": true,
	}

	fs.ConfigFileGetSectionNames = func() []string {
		names := make([]string, 0, len(configuredRemotes))
		for name := range configuredRemotes {
			names = append(names, name)
		}
		return names
	}

	fs.ConfigFileHasSection = func(section string) bool {
		return configuredRemotes[section]
	}

	t.Run("ambiguous_local_file_vs_remote", func(t *testing.T) {
		parsed, err := fs.ResolvePath("foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "foo", parsed.Name)
		assert.Equal(t, "bar", parsed.Path)
	})

	t.Run("explicit_local_path_with_colon", func(t *testing.T) {
		parsed, err := fs.ResolvePath("./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "./foo:bar", parsed.Path)
	})

	t.Run("wrapper_backend_nested_path", func(t *testing.T) {
		parsed, err := fs.ResolvePath("alias:crypt:chunker:data")
		require.NoError(t, err)
		assert.Equal(t, "alias", parsed.Name)
		assert.Equal(t, "crypt:chunker:data", parsed.Path)
	})

	t.Run("on_the_fly_remote", func(t *testing.T) {
		parsed, err := fs.ResolvePath(":s3,key=xxx:bucket/path")
		require.NoError(t, err)
		assert.Equal(t, ":s3", parsed.Name)
		assert.Equal(t, "bucket/path", parsed.Path)
	})

	t.Run("local_path_with_colon_in_middle", func(t *testing.T) {
		parsed, err := fs.ResolvePath("/path/to/foo:bar/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "/path/to/foo:bar/file.txt", parsed.Path)
	})
}

func TestParseRemoteWithResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	oldGet := fs.ConfigFileGet
	oldRegistry := fs.Registry
	defer func() {
		fs.ConfigFileGetSectionNames = oldGetSectionNames
		fs.ConfigFileGet = oldGet
		fs.Registry = oldRegistry
	}()

	mockfs.Register()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"testremote"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		if section == "testremote" && key == "type" {
			return "mockfs", true
		}
		return "", false
	}

	t.Run("known_remote", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("testremote:path/to/file")
		require.NoError(t, err)
		assert.Equal(t, "testremote", configName)
		assert.Equal(t, "path/to/file", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("unknown_remote_falls_back_to_local", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("unknown:path")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "unknown:path", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("explicit_local_path_with_colon", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "./foo:bar", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("wrapper_backend_path", func(t *testing.T) {
		parsed, err := fspath.Parse("alias:crypt:path")
		require.NoError(t, err)
		assert.Equal(t, "alias", parsed.Name)
		assert.Equal(t, "crypt:path", parsed.Path)
	})
}

func TestResolvePathRoundTrip(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	defer func() { fs.ConfigFileGetSectionNames = oldGetSectionNames }()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"foo", "alias", "crypt", "chunker"}
	}

	testCases := []string{
		"foo:bar",
		"alias:crypt:path",
		"crypt:chunker:data",
		"/local/path",
		"./relative/path",
		"//server/share/path",
		":s3,key=xxx:bucket/path",
	}

	if runtime.GOOS == "windows" {
		testCases = append(testCases, "C:\\path\\to\\file")
	} else {
		testCases = append(testCases, "C:path")
	}

	for _, tc := range testCases {
		parsed, err := fs.ResolvePath(tc)
		require.NoError(t, err, tc)

		if parsed.Name != "" && parsed.Name != "local" {
			reconstructed := parsed.ConfigString + ":" + parsed.Path
			if parsed.ConfigString == "" {
				reconstructed = parsed.Path
			}

			parsed2, err := fs.ResolvePath(reconstructed)
			require.NoError(t, err, reconstructed)
			assert.Equal(t, parsed.Name, parsed2.Name, "Round trip failed for %q", tc)
			assert.Equal(t, parsed.Path, parsed2.Path, "Round trip failed for %q", tc)
		}
	}
}
