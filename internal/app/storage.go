package app

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bidos/internal/store"
)

// Files is the local file store. Paths are stored relative to Root so the root can move
// (or be replaced by object storage with the same three methods).
type Files struct{ Root string }

var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SafeName strips path separators and odd characters from an upload name.
func SafeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = reUnsafe.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = "file"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

// Put stores data under <root>/<orgID>/<kind>/<id>-<name> and returns the relative path.
func (f *Files) Put(orgID, kind, id, name string, data []byte) (string, error) {
	if id == "" {
		id = store.NewID()
	}
	rel := filepath.ToSlash(filepath.Join(SafeName(orgID), SafeName(kind), id+"-"+SafeName(name)))
	abs := filepath.Join(f.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// Get reads a relative path, refusing traversal outside the root.
func (f *Files) Get(rel string) ([]byte, error) {
	abs, err := f.resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// Delete removes a stored file (missing files are not an error).
func (f *Files) Delete(rel string) error {
	abs, err := f.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Abs returns the absolute path for a relative stored path.
func (f *Files) Abs(rel string) (string, error) { return f.resolve(rel) }

func (f *Files) resolve(rel string) (string, error) {
	root, err := filepath.Abs(f.Root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(filepath.Clean("/"+rel)))
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
		return "", errors.New("invalid file path")
	}
	return abs, nil
}
