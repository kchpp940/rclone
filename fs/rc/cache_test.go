package rc

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/filter"
	"github.com/rclone/rclone/fs/fspath"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	tests := []struct {
		name           string
		input          string
		wantName       string
		wantPath       string
		wantConfigName string
		skipOnWindows  bool
		onlyOnWindows  bool
		skipFsCreate   bool
	}{
		{
			name:           "known_remote_path",
			input:          "testremote:path/to/file",
			wantName:       "testremote",
			wantPath:       "path/to/file",
			wantConfigName: "testremote",
		},
		{
			name:           "wrapper_backend_nested",
			input:          "alias:crypt:chunker:data",
			wantName:       "alias",
			wantPath:       "crypt:chunker:data",
			wantConfigName: "alias",
		},
		{
			name:           "on_the_fly_remote",
			input:          ":mockfs:/tmp/test",
			wantName:       ":mockfs",
			wantPath:       "/tmp/test",
			wantConfigName: ":mockfs",
		},
		{
			name:           "single_letter_remote_non_windows",
			input:          "C:path/to/file",
			wantName:       "C",
			wantPath:       "path/to/file",
			wantConfigName: "C",
			skipOnWindows:  true,
		},
		{
			name:           "single_letter_remote_windows",
			input:          "C:path/to/file",
			wantName:       "",
			wantPath:       "C:path/to/file",
			wantConfigName: "",
			onlyOnWindows:  true,
			skipFsCreate:   true,
		},
		{
			name:           "chunker_wrapper_path",
			input:          "chunker:testremote:data",
			wantName:       "chunker",
			wantPath:       "testremote:data",
			wantConfigName: "chunker",
		},
		{
			name:           "unknown_remote_falls_back_to_local",
			input:          "unknown:path",
			wantName:       "",
			wantPath:       "unknown:path",
			wantConfigName: "",
			skipFsCreate:   true,
		},
		{
			name:           "explicit_local_with_colon",
			input:          "./foo:bar",
			wantName:       "",
			wantPath:       "./foo:bar",
			wantConfigName: "",
			skipFsCreate:   true,
		},
		{
			name:           "absolute_path_with_colon",
			input:          "/path/to/foo:bar",
			wantName:       "",
			wantPath:       "/path/to/foo:bar",
			wantConfigName: "",
			skipFsCreate:   true,
		},
		{
			name:           "UNC_path",
			input:          "//server/share/path",
			wantName:       "",
			wantPath:       "//server/share/path",
			wantConfigName: "",
			skipFsCreate:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOnWindows && runtime.GOOS == "windows" {
				t.Skip("Skipping on Windows")
			}
			if tt.onlyOnWindows && runtime.GOOS != "windows" {
				t.Skip("Skipping on non-Windows")
			}

			parsed, err := fs.ResolvePath(tt.input)
			require.NoError(t, err, tt.input)
			assert.Equal(t, tt.wantName, parsed.Name, "Name mismatch for %q", tt.input)
			assert.Equal(t, tt.wantPath, parsed.Path, "Path mismatch for %q", tt.input)
			assert.Equal(t, tt.wantConfigName, parsed.ConfigString, "ConfigString mismatch for %q", tt.input)

			if tt.skipFsCreate {
				return
			}

			in := Params{
				"fs": tt.input,
			}
			f, err := GetFs(context.Background(), in)
			require.NoError(t, err, tt.input)
			require.NotNil(t, f)

			assert.Equal(t, tt.wantConfigName, f.Name(), "Fs Name mismatch for %q", tt.input)
			assert.Equal(t, tt.wantPath, f.Root(), "Fs Root mismatch for %q", tt.input)
		})
	}
}

func TestConfigResolvePath(t *testing.T) {
	oldGetSectionNames := fs.ConfigFileGetSectionNames
	defer func() { fs.ConfigFileGetSectionNames = oldGetSectionNames }()

	fs.ConfigFileGetSectionNames = func() []string {
		return []string{"myremote", "foo"}
	}

	t.Run("configured_remote", func(t *testing.T) {
		parsed, err := fs.ResolvePath("myremote:bucket/path")
		require.NoError(t, err)
		assert.Equal(t, "myremote", parsed.Name)
		assert.Equal(t, "bucket/path", parsed.Path)
	})

	t.Run("ambiguous_foo_bar_configured", func(t *testing.T) {
		parsed, err := fs.ResolvePath("foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "foo", parsed.Name)
		assert.Equal(t, "bar", parsed.Path)
	})

	t.Run("ambiguous_foo_bar_unconfigured", func(t *testing.T) {
		old := fs.ConfigFileGetSectionNames
		defer func() { fs.ConfigFileGetSectionNames = old }()

		fs.ConfigFileGetSectionNames = func() []string { return []string{"other"} }

		parsed, err := fs.ResolvePath("foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "foo:bar", parsed.Path)
	})

	t.Run("explicit_local_always_wins", func(t *testing.T) {
		parsed, err := fs.ResolvePath("./foo:bar")
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "./foo:bar", parsed.Path)
	})

	t.Run("absolute_path_always_local", func(t *testing.T) {
		parsed, err := fs.ResolvePath("/abs/path:with:colons")
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "/abs/path:with:colons", parsed.Path)
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

	t.Run("chunker_nested_wrapper", func(t *testing.T) {
		joined := fspath.JoinRootPath("chunker:crypt:base:", "file.txt")
		assert.Equal(t, "chunker:crypt:base:/file.txt", joined)

		parsed, err := fs.ResolvePath(joined)
		require.NoError(t, err)
		assert.Equal(t, "chunker", parsed.Name)
		assert.Equal(t, "crypt:base:/file.txt", parsed.Path)
	})

	t.Run("chunker_round_trip", func(t *testing.T) {
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

	t.Run("local_file_with_colon_in_chunker_context", func(t *testing.T) {
		joined := fspath.JoinRootPath("", "foo:bar")
		assert.Equal(t, "foo:bar", joined)

		parsed, err := fs.ResolvePath(joined)
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "foo:bar", parsed.Path)
	})

	t.Run("relative_path_with_colon", func(t *testing.T) {
		joined := fspath.JoinRootPath("", "./test:file")
		assert.Equal(t, "test:file", joined)

		parsed, err := fs.ResolvePath(joined)
		require.NoError(t, err)
		assert.Equal(t, "", parsed.Name)
		assert.Equal(t, "test:file", parsed.Path)
	})
}
