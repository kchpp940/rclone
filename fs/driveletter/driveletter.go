//go:build !windows

// Package driveletter returns whether a name is a valid drive letter
package driveletter

// IsDriveLetter returns a bool indicating whether name is a valid
// Windows drive letter
//
// On non windows platforms we don't have drive letters so we always
// return false
func IsDriveLetter(name string) bool {
	return false
}

// IsWindowsDrivePath returns true if the path looks like a Windows drive
// path such as C:\, C:/, C:path, \\server\share, etc.
//
// This function works on all platforms to allow consistent path parsing.
func IsWindowsDrivePath(path string) bool {
	if len(path) < 2 {
		return false
	}
	c := path[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	if path[1] != ':' {
		return false
	}
	if len(path) == 2 {
		return true
	}
	next := path[2]
	return next == '/' || next == '\\' || next != 0
}

// IsUNCPath returns true if the path looks like a Windows UNC path
// such as \\server\share or //server/share
//
// This function works on all platforms.
func IsUNCPath(path string) bool {
	if len(path) < 4 {
		return false
	}
	if (path[0] == '\\' || path[0] == '/') && (path[1] == '\\' || path[1] == '/') {
		return true
	}
	return false
}
