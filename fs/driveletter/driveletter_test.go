package driveletter

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDriveLetter(t *testing.T) {
	for _, test := range []struct {
		in   string
		want bool
		win  bool
	}{
		{"C", true, true},
		{"c", true, true},
		{"Z", true, true},
		{"z", true, true},
		{"A", true, true},
		{"", false, true},
		{"1", false, true},
		{"AB", false, true},
		{"C:", false, true},
		{"C", false, false},
		{"c", false, false},
		{"Z", false, false},
		{"z", false, false},
		{"A", false, false},
	} {
		if runtime.GOOS == "windows" && !test.win {
			continue
		}
		if runtime.GOOS != "windows" && test.win {
			continue
		}
		got := IsDriveLetter(test.in)
		assert.Equal(t, test.want, got, test.in)
	}
}

func TestIsWindowsDrivePath(t *testing.T) {
	for _, test := range []struct {
		in   string
		want bool
		win  bool
	}{
		{"C:", true, true},
		{"C:\\", true, true},
		{"C:/", true, true},
		{"C:\\path", true, true},
		{"C:/path", true, true},
		{"C:file.txt", true, true},
		{"D:", true, true},
		{"Z:\\data", true, true},
		{"", false, true},
		{"C", false, true},
		{"C;", false, true},
		{"AB:\\path", false, true},
		{"remote:", false, true},
		{"C:", false, false},
		{"C:\\", false, false},
		{"C:/", false, false},
		{"C:\\path", false, false},
		{"C:/path", false, false},
		{"C:file.txt", false, false},
		{"D:", false, false},
		{"Z:\\data", false, false},
	} {
		if runtime.GOOS == "windows" && !test.win {
			continue
		}
		if runtime.GOOS != "windows" && test.win {
			continue
		}
		got := IsWindowsDrivePath(test.in)
		assert.Equal(t, test.want, got, test.in)
	}
}

func TestIsUNCPath(t *testing.T) {
	for _, test := range []struct {
		in   string
		want bool
	}{
		{"\\\\server\\share", true},
		{"//server/share", true},
		{"\\\\server\\share\\path", true},
		{"//server/share/path", true},
		{"\\server\\share", false},
		{"/server/share", false},
		{"C:\\path", false},
		{"", false},
		{"\\\\", false},
		{"//", false},
		{"\\\\s", false},
		{"//s", false},
	} {
		got := IsUNCPath(test.in)
		assert.Equal(t, test.want, got, test.in)
	}
}
