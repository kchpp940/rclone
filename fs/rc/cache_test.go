package rc

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/filter"
	"github.com/rclone/rclone/fs/fspath"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerMockLocalBackend() func() {
	oldRegistry := fs.Registry
	fs.Register(&fs.RegInfo{
		Name:        "local",
		Description: "Mock Local Disk",
		NewFs: func(ctx context.Context, name string, root string, config configmap.Mapper) (fs.Fs, error) {
			return mockfs.NewFs(ctx, name, root, config)
		},
	})
	return func() { fs.Registry = oldRegistry }
}

func mockNewFs(t *testing.T) func() {
	ctx := context.Background()
	f, err := mockfs.NewFs(ctx, "/", "", nil)
	require.NoError(t, err)
	cache.Put("/", f)
	f, err = mockfs.NewFs(ctx, "mock", "/", nil)
	require.NoError(t, err)
	cache.Put("mock:/", f)
	cache.Put(":mock:/", f)
	f, err = mockfs.NewFs(ctx, "mock", "dir/", nil)
	require.NoError(t, err)
	cache.PutErr("mock:dir/file.txt", f, fs.ErrorIsFile)
	return func() {
		cache.Clear()
	}
}

func TestGetFsNamed(t *testing.T) {
	defer mockNewFs(t)()

	in := Params{
		"potato": "/",
	}
	f, err := GetFsNamed(context.Background(), in, "potato")
	require.NoError(t, err)
	assert.NotNil(t, f)

	in = Params{
		"sausage": "/",
	}
	f, err = GetFsNamed(context.Background(), in, "potato")
	require.Error(t, err)
	assert.Nil(t, f)
}

func TestGetFsNamedStruct(t *testing.T) {
	defer mockNewFs(t)()

	in := Params{
		"potato": Params{
			"type":  "mock",
			"_root": "/",
		},
	}
	f, err := GetFsNamed(context.Background(), in, "potato")
	require.NoError(t, err)
	assert.NotNil(t, f)

	in = Params{
		"potato": Params{
			"_name": "mock",
			"_root": "/",
		},
	}
	f, err = GetFsNamed(context.Background(), in, "potato")
	require.NoError(t, err)
	assert.NotNil(t, f)
}

func TestGetFsNamedFileOK(t *testing.T) {
	defer mockNewFs(t)()
	ctx := context.Background()

	in := Params{
		"potato": "/",
	}
	newCtx, f, err := GetFsNamedFileOK(ctx, in, "potato")
	require.NoError(t, err)
	assert.NotNil(t, f)
	assert.Equal(t, ctx, newCtx)

	in = Params{
		"sausage": "/",
	}
	newCtx, f, err = GetFsNamedFileOK(ctx, in, "potato")
	require.Error(t, err)
	assert.Nil(t, f)
	assert.Equal(t, ctx, newCtx)

	in = Params{
		"potato": "mock:dir/file.txt",
	}
	newCtx, f, err = GetFsNamedFileOK(ctx, in, "potato")
	assert.Nil(t, err)
	assert.NotNil(t, f)
	assert.NotEqual(t, ctx, newCtx)

	fi := filter.GetConfig(newCtx)
	assert.False(t, fi.InActive())
	assert.True(t, fi.IncludeRemote("file.txt"))
	assert.False(t, fi.IncludeRemote("other.txt"))
}

func TestGetConfigMap(t *testing.T) {
	for _, test := range []struct {
		in           Params
		fsName       string
		wantFsString string
		wantErr      string
	}{
		{
			in: Params{
				"Fs": Params{},
			},
			fsName:  "Fs",
			wantErr: `couldn't find "type" or "_name" in JSON config definition`,
		},
		{
			in: Params{
				"Fs": Params{
					"notastring": true,
				},
			},
			fsName:  "Fs",
			wantErr: `cannot unmarshal bool`,
		},
		{
			in: Params{
				"Fs": Params{
					"_name": "potato",
				},
			},
			fsName:       "Fs",
			wantFsString: "potato:",
		},
		{
			in: Params{
				"Fs": Params{
					"type": "potato",
				},
			},
			fsName:       "Fs",
			wantFsString: ":potato:",
		},
		{
			in: Params{
				"Fs": Params{
					"type":       "sftp",
					"_name":      "potato",
					"parameter":  "42",
					"parameter2": "true",
					"_root":      "/path/to/somewhere",
				},
			},
			fsName:       "Fs",
			wantFsString: "potato,parameter='42',parameter2='true':/path/to/somewhere",
		},
	} {
		gotFsString, gotErr := getConfigMap(test.in, test.fsName)
		what := fmt.Sprintf("%+v", test.in)
		assert.Equal(t, test.wantFsString, gotFsString, what)
		if test.wantErr == "" {
			assert.NoError(t, gotErr)
		} else {
			require.Error(t, gotErr)
			assert.Contains(t, gotErr.Error(), test.wantErr)

		}
	}
}

func TestGetFs(t *testing.T) {
	defer mockNewFs(t)()

	in := Params{
		"fs": "/",
	}
	f, err := GetFs(context.Background(), in)
	require.NoError(t, err)
	assert.NotNil(t, f)
}

func TestGetFsAndRemoteNamed(t *testing.T) {
	defer mockNewFs(t)()

	in := Params{
		"fs":     "/",
		"remote": "hello",
	}
	f, remote, err := GetFsAndRemoteNamed(context.Background(), in, "fs", "remote")
	require.NoError(t, err)
	assert.NotNil(t, f)
	assert.Equal(t, "hello", remote)

	f, _, err = GetFsAndRemoteNamed(context.Background(), in, "fsX", "remote")
	require.Error(t, err)
	assert.Nil(t, f)

	f, _, err = GetFsAndRemoteNamed(context.Background(), in, "fs", "remoteX")
	require.Error(t, err)
	assert.Nil(t, f)

}

func TestGetFsAndRemote(t *testing.T) {
	defer mockNewFs(t)()

	in := Params{
		"fs":     "/",
		"remote": "hello",
	}
	f, remote, err := GetFsAndRemote(context.Background(), in)
	require.NoError(t, err)
	assert.NotNil(t, f)
	assert.Equal(t, "hello", remote)

	t.Run("RcFscache", func(t *testing.T) {
		getEntries := func() int {
			call := Calls.Get("fscache/entries")
			require.NotNil(t, call)

			in := Params{}
			out, err := call.Fn(context.Background(), in)
			require.NoError(t, err)
			require.NotNil(t, out)
			return out["entries"].(int)
		}

		t.Run("Entries", func(t *testing.T) {
			assert.NotEqual(t, 0, getEntries())
		})

		t.Run("Clear", func(t *testing.T) {
			call := Calls.Get("fscache/clear")
			require.NotNil(t, call)

			in := Params{}
			out, err := call.Fn(context.Background(), in)
			require.NoError(t, err)
			require.Nil(t, out)
		})

		t.Run("Entries2", func(t *testing.T) {
			assert.Equal(t, 0, getEntries())
		})
	})
}

func TestRCEntryResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	oldGet := fs.ConfigFileGet
	oldRegistry := fs.Registry
	defer func() {
		fs.ConfigFileGetSectionNames = oldGetSectionNames
		fs.ConfigFileGet = oldGet
		fs.Registry = oldRegistry
	}()

	mockfs.Register()
	registerMockLocalBackend()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"testremote", "alias", "crypt", "chunker", "C"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		switch section {
		case "testremote", "alias", "crypt", "chunker", "C":
			if key == "type" {
				return "mockfs", true
			}
		}
		return "", false
	}

	t.Run("known_remote_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "testremote:path/to/file"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "testremote", f.Name())
		assert.Equal(t, "path/to/file", f.Root())
	})

	t.Run("unknown_remote_via_RC_GetFs_returns_error", func(t *testing.T) {
		in := Params{"fs": "typo:path/to/file"}
		_, err := GetFs(context.Background(), in)
		require.Error(t, err, "RC entry should report error for unknown remote, not silently fall back to local")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("explicit_local_with_colon_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "./foo:bar"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "local", f.Name())
		assert.Equal(t, "./foo:bar", f.Root())
	})

	t.Run("absolute_local_with_colon_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "/path/to/foo:bar"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "local", f.Name())
		assert.Equal(t, "/path/to/foo:bar", f.Root())
	})

	t.Run("wrapper_backend_nested_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "alias:crypt:chunker:data"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "alias", f.Name())
		assert.Equal(t, "crypt:chunker:data", f.Root())
	})

	t.Run("on_the_fly_remote_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": ":mockfs:/tmp/test"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, ":mockfs", f.Name())
		assert.Equal(t, "/tmp/test", f.Root())
	})

	t.Run("single_letter_remote_non_windows_via_RC_GetFs", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping on Windows")
		}
		in := Params{"fs": "C:path/to/file"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "C", f.Name())
		assert.Equal(t, "path/to/file", f.Root())
	})

	t.Run("chunker_wrapper_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "chunker:testremote:data"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "chunker", f.Name())
		assert.Equal(t, "testremote:data", f.Root())
	})

	t.Run("ParseRemote_error_for_typo", func(t *testing.T) {
		_, _, _, _, err := fs.ParseRemote("testremot:data")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})
}

func TestConfigResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	oldGet := fs.ConfigFileGet
	oldRegistry := fs.Registry
	defer func() {
		fs.ConfigFileGetSectionNames = oldGetSectionNames
		fs.ConfigFileGet = oldGet
		fs.Registry = oldRegistry
	}()

	mockfs.Register()
	registerMockLocalBackend()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"myremote", "foo"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		switch section {
		case "myremote", "foo":
			if key == "type" {
				return "mockfs", true
			}
		}
		return "", false
	}

	t.Run("configured_remote_via_ConfigFs", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ConfigFs("myremote:bucket/path")
		require.NoError(t, err)
		assert.Equal(t, "myremote", configName)
		assert.Equal(t, "bucket/path", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("ambiguous_foo_bar_configured_via_ParseRemote", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "foo", configName)
		assert.Equal(t, "bar", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("ambiguous_foo_bar_unconfigured_returns_error", func(t *testing.T) {
		oldSections := fs.ConfigFileGetSectionNames
		oldGetFn := fs.ConfigFileGet
		defer func() {
			fs.ConfigFileGetSectionNames = oldSections
			fs.ConfigFileGet = oldGetFn
		}()

		fs.ConfigFileGetSectionNames = func() []string { return []string{"other"} }
		fs.ConfigFileGet = func(string, string) (string, bool) { return "", false }

		_, _, _, _, err := fs.ParseRemote("foo:bar")
		require.Error(t, err, "unconfigured foo:bar should return error in CLI/RC mode")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("explicit_local_always_wins_via_ParseRemote", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ParseRemote("./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "./foo:bar", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("absolute_path_always_local_via_ConfigFs", func(t *testing.T) {
		fsInfo, configName, fsPath, _, err := fs.ConfigFs("/abs/path:with:colons")
		require.NoError(t, err)
		assert.Equal(t, "local", configName)
		assert.Equal(t, "/abs/path:with:colons", fsPath)
		assert.Equal(t, "local", fsInfo.Name)
	})

	t.Run("unknown_typo_remote_returns_error", func(t *testing.T) {
		_, _, _, _, err := fs.ParseRemote("myremot:data")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})
}

func TestChunkerEntryResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	oldGet := fs.ConfigFileGet
	oldRegistry := fs.Registry
	defer func() {
		fs.ConfigFileGetSectionNames = oldGetSectionNames
		fs.ConfigFileGet = oldGet
		fs.Registry = oldRegistry
	}()

	mockfs.Register()
	registerMockLocalBackend()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"chunker", "crypt", "base"}
	}

	fs.ConfigFileGet = func(section, key string) (string, bool) {
		switch section {
		case "chunker", "crypt", "base":
			if key == "type" {
				return "mockfs", true
			}
		}
		return "", false
	}

	t.Run("chunker_join_root_path", func(t *testing.T) {
		joined := fspath.JoinRootPath("chunker:crypt:", "data/subdir")
		assert.Equal(t, "chunker:crypt:/data/subdir", joined)

		parsed, err := fspath.Parse(joined)
		require.NoError(t, err)
		assert.Equal(t, "chunker", parsed.Name)
		assert.Equal(t, "crypt:/data/subdir", parsed.Path)
	})

	t.Run("chunker_nested_wrapper_via_ParseRemote", func(t *testing.T) {
		joined := fspath.JoinRootPath("chunker:crypt:base:", "file.txt")
		assert.Equal(t, "chunker:crypt:base:/file.txt", joined)

		fsInfo, configName, fsPath, _, err := fs.ParseRemote(joined)
		require.NoError(t, err)
		assert.Equal(t, "chunker", configName)
		assert.Equal(t, "crypt:base:/file.txt", fsPath)
		assert.Equal(t, "mockfs", fsInfo.Name)
	})

	t.Run("chunker_round_trip_via_ResolvePath", func(t *testing.T) {
		original := "chunker:crypt:base:/data"
		parsed, err := fs.ResolvePath(original)
		require.NoError(t, err)

		if parsed.Name != "" {
			reconstructed := parsed.ConfigString + ":" + parsed.Path
			parsed2, err := fs.ResolvePath(reconstructed)
			require.NoError(t, err)
			assert.Equal(t, parsed.Name, parsed2.Name)
			assert.Equal(t, parsed.Path, parsed2.Path)
		}
	})

	t.Run("unknown_ambiguous_path_via_ParseRemote_returns_error", func(t *testing.T) {
		joined := fspath.JoinRootPath("", "foo:bar")
		assert.Equal(t, "foo:bar", joined)

		_, _, _, _, err := fs.ParseRemote(joined)
		require.Error(t, err, "unconfigured foo:bar via ParseRemote should return error in RemoteFirst mode")
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})

	t.Run("explicit_local_via_ParseRemote_in_chunker_context", func(t *testing.T) {
		joined := fspath.JoinRootPath("", "./foo:bar")
		assert.Equal(t, "foo:bar", joined)

		_, _, _, _, err := fs.ParseRemote(joined)
		require.Error(t, err, "unconfigured foo:bar via ParseRemote should return error")

		_, _, _, _, err = fs.ParseRemote("./foo:bar")
		require.NoError(t, err, "./foo:bar with explicit prefix should resolve as local")
	})

	t.Run("chunker_via_RC_GetFs", func(t *testing.T) {
		in := Params{"fs": "chunker:crypt:base:some/dir"}
		f, err := GetFs(context.Background(), in)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, "chunker", f.Name())
		assert.Equal(t, "crypt:base:some/dir", f.Root())
	})

	t.Run("unknown_remote_via_RC_GetFs_in_chunker_context_returns_error", func(t *testing.T) {
		in := Params{"fs": "nochunker:data"}
		_, err := GetFs(context.Background(), in)
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrorNotFoundInConfigFile)
	})
}
