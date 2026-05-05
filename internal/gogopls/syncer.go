package gogopls

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/benbenbenbenbenben/gogo/internal/sourcegen"
)

type fileState struct {
	modTime    time.Time
	size       int64
	genPath    string
	genModTime time.Time
	genSize    int64
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
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, sourcegen.GeneratedSuffix) {
			if err := removeOrphanedGeneratedFile(path); err != nil {
				errs = append(errs, err)
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
		genInfo, genExists, err := generatedFileInfo(state.genPath)
		if err != nil {
			handleSyncError(&errs, current, path, state.genPath, prev, ok, err)
			return nil
		}

		// Use both modTime and size because some filesystems have coarse timestamp
		// resolution, so a size change can still catch missed source edits.
		needsRegeneration := !ok ||
			!prev.modTime.Equal(state.modTime) ||
			prev.size != state.size ||
			!genExists ||
			(ok && generatedStateChanged(prev, genInfo))
		if needsRegeneration {
			genPath, err := sourcegen.WriteGeneratedSibling(path)
			if err != nil {
				handleSyncError(&errs, current, path, state.genPath, prev, ok, err)
				return nil
			}
			state.genPath = genPath
			genInfo, _, err = generatedFileInfo(genPath)
			if err != nil {
				handleSyncError(&errs, current, path, state.genPath, prev, ok, err)
				return nil
			}
		}
		if genInfo != nil {
			state.genModTime = genInfo.ModTime()
			state.genSize = genInfo.Size()
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

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".idea", ".svn", ".vscode", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func generatedFileInfo(path string) (fs.FileInfo, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return info, true, nil
}

func generatedStateChanged(prev fileState, info fs.FileInfo) bool {
	if info == nil {
		return true
	}
	return !prev.genModTime.Equal(info.ModTime()) || prev.genSize != info.Size()
}

func handleSyncError(errs *[]error, current map[string]fileState, path, genPath string, prev fileState, ok bool, err error) {
	*errs = append(*errs, err)
	if ok {
		current[path] = prev
		return
	}
	current[path] = fileState{genPath: genPath}
}

func removeOrphanedGeneratedFile(path string) error {
	srcPath := strings.TrimSuffix(path, sourcegen.GeneratedSuffix) + sourcegen.SourceExt
	_, err := os.Stat(srcPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeIfExists(path)
}
