package gogopls

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/benbenbenbenbenben/gogo/internal/sourcegen"
)

type fileState struct {
	modTime time.Time
	size    int64
	genPath string
}

type Syncer struct {
	root  string
	files map[string]fileState
}

func NewSyncer(root string) *Syncer {
	return &Syncer{
		root:  root,
		files: map[string]fileState{},
	}
}

func (s *Syncer) Sync() error {
	current := make(map[string]fileState, len(s.files))
	var errs []error

	if err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, walkErr)
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != sourcegen.SourceExt {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			errs = append(errs, err)
			return nil
		}

		state := fileState{
			modTime: info.ModTime(),
			size:    info.Size(),
			genPath: sourcegen.GeneratedPath(path),
		}
		prev, ok := s.files[path]
		if !ok || prev.modTime != state.modTime || prev.size != state.size {
			genPath, err := sourcegen.WriteGeneratedSibling(path)
			if err != nil {
				errs = append(errs, err)
				if ok {
					current[path] = prev
				} else {
					current[path] = fileState{genPath: state.genPath}
				}
				return nil
			}
			state.genPath = genPath
		}

		current[path] = state
		return nil
	}); err != nil {
		errs = append(errs, err)
	}

	for path, state := range s.files {
		if _, ok := current[path]; ok {
			continue
		}
		if err := removeIfExists(state.genPath); err != nil {
			errs = append(errs, err)
		}
	}

	s.files = current
	return errors.Join(errs...)
}

func (s *Syncer) Cleanup() error {
	var errs []error
	for _, state := range s.files {
		if err := removeIfExists(state.genPath); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func removeIfExists(path string) error {
	if path == "" {
		return nil
	}

	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
