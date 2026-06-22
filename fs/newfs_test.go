package fs_test

import (
	"context"
	"runtime"
	"testing"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
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
			wantName:   "unknown",
			wantPath:   "path",
			wantConfig: "unknown",
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

	t.Run("known_remote", func(t *testing.T) {
		parsed, err := fs.ResolvePath("foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "foo", parsed.Name)
		assert.Equal(t, "bar", parsed.Path)
	})

	t.Run("unknown_remote_preserved_RemoteFirst", func(t *testing.T) {
		parsed, err := fs.ResolvePath("typo:data")
		require.NoError(t, err)
		assert.Equal(t, "typo", parsed.Name, "RemoteFirst should preserve unknown remote name for ParseRemote to error on")
		assert.Equal(t, "data", parsed.Path)
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
		return []string{"testremote", "alias", "crypt", "chunker"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		switch section {
		case "testremote", "alias", "crypt", "chunker":
			if key == "type" {
				return "mockfs", true
			}
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

	t.Run("unknown_remote_returns_error", func(t *testing.T) {
		_, _, _, _, err := fs.ParseRemote("unknown:path")
		require.Error(t, err, "unknown remote should return NotFoundInConfigFile error, not fall back to local")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("typo_remote_returns_error", func(t *testing.T) {
		_, _, _, _, err := fs.ParseRemote("testremot:data")
		require.Error(t, err, "typo'd remote name should return error")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("explicit_local_path_with_colon", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "./foo:bar", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("absolute_local_path_with_colon", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("/path/to/foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "/path/to/foo:bar", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("UNC_path", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("//server/share/path")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "//server/share/path", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("wrapper_backend_known_outer", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("alias:crypt:path")
		require.NoError(t, err)
		assert.Equal(t, "alias", configName)
		assert.Equal(t, "crypt:path", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("wrapper_backend_unknown_outer_returns_error", func(t *testing.T) {
		_, _, _, _, err := fs.ParseRemote("unknownalias:crypt:path")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("on_the_fly_remote", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote(":mockfs:/tmp/test")
		require.NoError(t, err)
		assert.Equal(t, ":mockfs", configName)
		assert.Equal(t, "/tmp/test", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("plain_relative_local_path", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("relative/path")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "relative/path", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})
}

func TestNewFsEntryPointPathResolution(t *testing.T) {
	ctx := context.Background()
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
		return []string{"myremote", "alias", "crypt"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		switch section {
		case "myremote", "alias", "crypt":
			if key == "type" {
				return "mockfs", true
			}
		}
		return "", false
	}

	t.Run("known_remote_via_NewFs", func(t *testing.T) {
		f, err := fs.NewFs(ctx, "myremote:path/to/data")
		require.NoError(t, err)
		assert.Equal(t, "myremote", f.Name())
		assert.Equal(t, "path/to/data", f.Root())
	})

	t.Run("unknown_remote_via_NewFs_returns_error", func(t *testing.T) {
		_, err := fs.NewFs(ctx, "typo:path/to/data")
		require.Error(t, err, "unknown remote via NewFs should return error, not fall back to local")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("explicit_local_with_colon_via_NewFs", func(t *testing.T) {
		f, err := fs.NewFs(ctx, "./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "local", f.Name())
		assert.Contains(t, f.Root(), "foo:bar", "local backend Root should contain the original foo:bar suffix (may be absolutized)")
	})

	t.Run("wrapper_backend_via_NewFs", func(t *testing.T) {
		f, err := fs.NewFs(ctx, "alias:crypt:somepath")
		require.NoError(t, err)
		assert.Equal(t, "alias", f.Name())
		assert.Equal(t, "crypt:somepath", f.Root())
	})

	t.Run("on_the_fly_remote_via_NewFs", func(t *testing.T) {
		f, err := fs.NewFs(ctx, ":mockfs:/tmp/test")
		require.NoError(t, err)
		assert.Equal(t, ":mockfs", f.Name())
		assert.Equal(t, "/tmp/test", f.Root())
	})
}

func TestResolvePathRoundTrip(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	defer func() { fs.ConfigFileGetSectionNames = oldGetSectionNames }()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"foo", "alias", "crypt", "chunker", "C"}
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
