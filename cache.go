package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	glob "github.com/ryanuber/go-glob"
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
		pth, ignore := parseIgnoreListItem(item)
		if len(pth) == 0 {
			continue
		}
		ignoreByPath[pth] = ignore
	}
	return ignoreByPath
}

// expandPath returns every file included in pth (recursively) if it is a dir,
// if pth is a file it will be returned as an array.
func expandPath(pth string) ([]string, error) {
	info, err := os.Lstat(pth)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return []string{pth}, nil
	}

	var subPaths []string
	if err := filepath.Walk(pth, func(p string, i os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if i.IsDir() {
			return nil
		}

		subPaths = append(subPaths, p)
		return nil
	}); err != nil {
		return nil, err
	}

	return subPaths, nil
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

		subPths, err := expandPath(pth)
		if err != nil {
			return nil, err
		}
		for _, p := range subPths {
			normalized[p] = indicator
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
func match(pth string, excludeByPattern map[string]bool) (bool, bool) {
	for pattern, exclude := range excludeByPattern {
		if strings.Contains(pattern, "*") && glob.Glob(pattern, pth) {
			return true, exclude
		}

		if !strings.Contains(pattern, "*") && strings.HasPrefix(pth, pattern) {
			return true, exclude
		}
	}
	return false, false
}

// interleave matches the given include items with the ignore items and returns which path needs to be cached:
// if an ignore item matches to a path, the path either will not affect the previous cache invalidation
// or will not be included in the cache.
// Otherwise a path will affect the previous cache invalidation:
// if the path has indicator, the indicator will affect the previous cache invalidation
// otherwise the file itself.
func interleave(indicatorByPth map[string]string, excludeByPattern map[string]bool) (map[string]string, error) {
	indicatorByCachePth := map[string]string{}

	for pth, indicator := range indicatorByPth {
		doNotTrack, exclude := match(pth, excludeByPattern)
		if exclude {
			// this file should not be included in the cache
			continue
		}

		if doNotTrack {
			// this file's changes does not fluctuates existing cache invalidation
			indicator = ""
		} else if len(indicator) == 0 {
			// the file's own content fluctuates existing cache invalidation
			indicator = pth
		} else {
			// the file's indicator content fluctuates existing cache invalidation
		}

		indicatorByCachePth[pth] = indicator
	}

	return indicatorByCachePth, nil
}
