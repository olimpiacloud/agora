package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (m *Manager) uploadPaths(ws string, files []string) ([]string, error) {
	home, err := filepath.EvalSymlinks(filepath.Join(m.wsDir(ws), "home"))
	if err != nil {
		return nil, fmt.Errorf("workspace home: %w", err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(home, p)
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil, fmt.Errorf("upload %s: %w", f, err)
		}
		if !strings.HasPrefix(real, home+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: upload %s: files must be inside the workspace home %s, for example in %s", ErrForbidden, f, home, filepath.Join(home, "Downloads"))
		}
		out = append(out, real)
	}
	return out, nil
}
