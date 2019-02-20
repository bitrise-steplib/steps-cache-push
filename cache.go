package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	glob "github.com/ryanuber/go-glob"
)

func parseIncludeListItem(item string) (string, string) {
	// file/or/dir/to/cache -> indicator/file
	// file/or/dir/to/cache
	i := strings.Index(item, "->")
	if i == -1 {
		return strings.TrimSpace(item), ""
	}
	var pth string
	if i > 0 {
		pth = item[:i]
	}

	var indicator string
	if i+2 < len(item) {
		indicator = item[i+2:]
	}

	return strings.TrimSpace(pth), strings.TrimSpace(indicator)
}

func parseIgnoreListItem(item string) (string, bool) {
	// path/or/patter/to/exclude
	// !path/or/patter/to/exclude
	item = strings.TrimSpace(item)
	if len(item) == 0 {
		return "", false
	}

	ignore := false
	if strings.HasPrefix(item, "!") {
		ignore = true
		item = strings.TrimPrefix(item, "!")
		item = strings.TrimSpace(item)
		if len(item) == 0 {
			return "", false
		}
	}

	return item, ignore
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

func expandPath(pth string) ([]string, error) {
	info, err := os.Lstat(pth)
	if err != nil {
		return nil, err
	}

	var includeSubPaths []string
	if info.IsDir() {
		if err := filepath.Walk(pth, func(p string, i os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if i.IsDir() {
				return nil
			}

			includeSubPaths = append(includeSubPaths, p)
			return nil
		}); err != nil {
			return nil, err
		}
	} else {
		includeSubPaths = append(includeSubPaths, pth)
	}

	return includeSubPaths, nil
}

func normalizeIndicatorByPath(indicatorByPath map[string]string) (map[string]string, error) {
	normalized := map[string]string{}
	for pth, indicator := range indicatorByPath {
		if len(indicator) > 0 {
			var err error
			indicator, err = pathutil.AbsPath(indicator)
			if err != nil {
				return nil, err
			}

			info, exist, err := pathutil.PathCheckAndInfos(indicator)
			switch {
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

func interleave(indicatorByPth map[string]string, excludeByPattern map[string]bool) (map[string]string, error) {
	indicatorByCahcePth := map[string]string{}

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

		indicatorByCahcePth[pth] = indicator
	}

	return indicatorByCahcePth, nil
}
