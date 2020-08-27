// Cache path and ignore path related functions.
//
// Ignoring symlink target changes for cache invalidation, as we expect
// the symlinks to be yarn workspace symlink: https://yarnpkg.com/blog/2018/02/15/nohoist/.
// The symlinks are included in the cache, just not chhecked if the target they point to is changed.
// If case it is a link to a directory outside of the cached paths (e.g. yarn workspaces),
// will not add the linked directory to the cache, and will not invalidate the cache if it changes,
// as we expect them to be part of the repository.
// If it links to a directory included in the cache already, then also ignoring it.
// The directory contents will be added to the cache as regular files, no need to check them twice.
// Symlinks to files are also ignored.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/ryanuber/go-glob"
)

// parseIncludeListItem separates path to cache and change indicator path.
func parseIncludeListItem(item string) (string, string) {
	// file/or/dir/to/cache -> indicator/file
	// file/or/dir/to/cache
	if parts := strings.Split(item, "->"); len(parts) > 1 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(item), ""
}

// parseIgnoreListItem separates ignore pattern and if pattern match removes item from cache or not.
func parseIgnoreListItem(item string) (string, bool) {
	// path/or/patter/to/exclude
	// !path/or/patter/to/exclude
	item = strings.TrimSpace(item)
	if len(item) > 1 && item[0] == '!' {
		return strings.TrimSpace(item[1:]), true
	}
	return strings.TrimPrefix(item, "!"), false
}

func parseIncludeList(list []string) map[string]string {
	indicatorByPath := map[string]string{}
	for _, item := range list {
		pth, indicator := parseIncludeListItem(item)
		if len(pth) == 0 {
			continue
		}
		indicatorByPath[pth] = indicator
	}
	return indicatorByPath
}

func parseIgnoreList(list []string) map[string]bool {
	ignoreByPath := map[string]bool{}
	for _, item := range list {
		pth, exclude := parseIgnoreListItem(item)
		if len(pth) == 0 {
			continue
		}

		ex, ok := ignoreByPath[pth]
		if ok && ex {
			continue
		}

		ignoreByPath[pth] = exclude
	}
	return ignoreByPath
}

func isSymlink(pth string) (bool, error) {
	linkFileInfo, err := os.Lstat(pth)
	if err != nil {
		return false, fmt.Errorf("failed to get file info, error: %s", err)
	}

	return linkFileInfo.Mode()&os.ModeSymlink != 0, nil
}

// expandPath returns cacheable files inside a directory recursively.
// If parameter root is a file, it returns that file.
// An array of regural files, directories and symlinks is returned, other irregural files (named pipe, socket) are ignored.
func expandPath(root string) (regularFiles []string, symlinkPaths []string, dirPaths []string, err error) {
	if err := filepath.Walk(root, func(path string, i os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		isLink, err := isSymlink(path)
		if err != nil {
			return err
		}
		if isLink {
			symlinkPaths = append(symlinkPaths, path)
			return nil
		}

		// Adding directories, in case a directory is empty, it will still be included
		if i.Mode().IsDir() {
			dirPaths = append(dirPaths, path)
			return nil
		}

		// Not adding directories and non symlink irregural files to the cache
		// ModeNamedPipe | ModeSocket | ModeDevice | ModeCharDevice | ModeIrregular & i.Mode() != 0
		if !i.Mode().IsRegular() {
			return nil
		}

		regularFiles = append(regularFiles, path)
		return nil
	}); err != nil {
		return nil, nil, nil, err
	}

	return regularFiles, symlinkPaths, dirPaths, nil
}

// normalizeIndicatorByPath modifies indicatorByPath:
// expands both path to cache and indicator path
// removes the item if any of path to cache or indicator path is not exist or if the indicator is a dir
// replaces path to cache (if it is a directory) by every file (recursively) in the directory.
func normalizeIndicatorByPath(indicatorByPath map[string]string) (map[string]string, error) {
	normalized := map[string]string{}
	for pth, indicator := range indicatorByPath {
		if len(indicator) > 0 {
			var err error
			indicator, err = pathutil.AbsPath(indicator)
			if err != nil {
				return nil, err
			}

			switch info, exist, err := pathutil.PathCheckAndInfos(indicator); {
			case err != nil:
				return nil, err
			case !exist:
				log.Warnf("indicator does not exists at: %s", indicator)
				continue
			case info.IsDir():
				log.Warnf("indicator is a directory: %s", indicator)
				continue
			}
		}

		var err error
		pth, err = pathutil.AbsPath(pth)
		if err != nil {
			return nil, err
		}

		exist, err := pathutil.IsPathExists(pth)
		if err != nil {
			return nil, err
		}
		if !exist {
			log.Warnf("path does not exists at: %s", pth)
			continue
		}

		regularFiles, symlinkPaths, dirPaths, err := expandPath(pth)
		if err != nil {
			return nil, err
		}
		for _, dir := range dirPaths {
			normalized[dir] = "-"
		}
		for _, file := range regularFiles {
			normalized[file] = indicator
		}
		for _, file := range symlinkPaths {
			// this file's changes does not fluctuates existing cache invalidation
			normalized[file] = "-"
		}
	}
	return normalized, nil
}

// normalizeExcludeByPattern modifies excludeByPattern:
// expands patterns.
func normalizeExcludeByPattern(excludeByPattern map[string]bool) (map[string]bool, error) {
	normalized := map[string]bool{}
	for pattern, exclude := range excludeByPattern {
		pattern, err := pathutil.AbsPath(pattern)
		if err != nil {
			return nil, err
		}

		normalized[pattern] = exclude
	}
	return normalized, nil
}

// match reports whether the path matches to any of the given ignore items
// and returns the exclude property of the matching ignore item.
func match(pth string, excludeByPattern map[string]bool) (exclude bool, ok bool) {
	matchFn := func(patternOrPath, subject string) bool {
		if strings.Contains(patternOrPath, "*") {
			return glob.Glob(patternOrPath, subject)
		}
		return strings.HasPrefix(subject, patternOrPath)
	}

	for s, ex := range excludeByPattern {
		if matchFn(s, pth) {
			ok = true
			exclude = ex
		}
	}

	return
}

// interleave matches the given include items with the ignore items and returns which path needs to be cached:
// if an ignore item matches to a path, the path either will not affect the previous cache invalidation
// or will not be included in the cache.
// Otherwise a path will affect the previous cache invalidation:
// if the path has indicator, the indicator will affect the previous cache invalidation
// otherwise the file itself.
func interleave(indicatorByPth map[string]string, excludeByPattern map[string]bool) map[string]string {
	indicatorByCachePth := map[string]string{}

	for pth, indicator := range indicatorByPth {
		exclude, ok := match(pth, excludeByPattern)
		if exclude {
			// this file should not be included in the cache
			continue
		}

		if ok || indicator == "-" {
			// this file's changes does not invalidate existing cache
			indicator = ""
		} else if len(indicator) == 0 {
			// the file's content is used to invalidate existing cache
			indicator = pth
		} // else: the file's indicator content is used to invalidate existing cache

		indicatorByCachePth[pth] = indicator
	}

	return indicatorByCachePth
}
