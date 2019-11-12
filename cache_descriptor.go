// Cache descriptor file related models and functions.
package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
)

// ChangeIndicator ...
type ChangeIndicator string

const (
	// MD5 ...
	MD5 = ChangeIndicator("file-content-hash")
	// MODTIME ...
	MODTIME = ChangeIndicator("file-mod-time")
)

// result stores how the keys are different in two cache descriptor.
type result struct {
	removedIgnored []string
	removed        []string
	changed        []string
	matching       []string
	addedIgnored   []string
	added          []string
}

// hasChanges reports whether a new cache needs to be generated or not.
func (r result) hasChanges() bool {
	return len(r.removed) > 0 || len(r.changed) > 0 || len(r.added) > 0
}

// compare compares two cache descriptor file and return the differences.
func compare(old map[string]map[string]bool, new map[string]map[string]bool) (r result) {
	oldCopy := transformToIndicatorByPth(old)
	newCopy := transformToIndicatorByPth(new)

	for oldPth, oldIndicator := range oldCopy {
		newIndicator, ok := newCopy[oldPth]
		switch {
		case !ok && oldIndicator == "-":
			r.removedIgnored = append(r.removedIgnored, oldPth)
		case !ok:
			r.removed = append(r.removed, oldPth)
		case oldIndicator != newIndicator:
			r.changed = append(r.changed, oldPth)
		default:
			r.matching = append(r.matching, oldPth)
		}

		delete(newCopy, oldPth)
	}

	for newPth, newIndicator := range newCopy {
		if newIndicator == "-" {
			r.addedIgnored = append(r.addedIgnored, newPth)
		} else {
			r.added = append(r.added, newPth)
		}
	}

	return
}

// transformToIndicatorByPth transforms an indicator map to an indicatorByPth map.
func transformToIndicatorByPth(indicatorMap map[string]map[string]bool) map[string]string {
	var indicatorByPth = map[string]string{}
	for indicator, pthMap := range indicatorMap {
		for pth := range pthMap {
			indicatorByPth[pth] = indicator
		}
	}
	return indicatorByPth
}

// cacheDescriptor creates a cache descriptor for a given change_indicator_path - cache_path (single-multiple) mapping.
func cacheDescriptor(indicatorMap map[string]map[string]bool, method ChangeIndicator) (map[string]map[string]bool, error) {
	descriptor := map[string]map[string]bool{}
	for indicatorPth, pthMap := range indicatorMap {
		var indicator string
		var err error
		if len(indicatorPth) == 0 {
			indicator = "-"
		} else if method == MD5 {
			indicator, err = fileContentHash(indicatorPth)
		} else {
			indicator, err = fileModtime(indicatorPth)
		}
		if err != nil {
			return nil, err
		}
		for pth := range pthMap {
			if len(descriptor[indicator]) == 0 {
				descriptor[indicator] = map[string]bool{}
			}
			descriptor[indicator][pth] = true
		}
	}
	return descriptor, nil
}

// fileContentHash returns file's md5 content hash.
func fileContentHash(pth string) (string, error) {
	f, err := os.Open(pth)
	if err != nil {
		return "", err
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Errorf("Failed to close file (%s), error: %+v", pth, err)
		}
	}()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// fileModtime returns a file's modtime as a Unix timestamp representation.
func fileModtime(pth string) (string, error) {
	fi, err := os.Stat(pth)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", fi.ModTime().Unix()), nil
}

// readCacheDescriptor reads cache descriptor from pth is exists.
func readCacheDescriptor(pth string) (map[string]string, error) {
	if exists, err := pathutil.IsPathExists(pth); err != nil {
		return nil, err
	} else if !exists {
		return nil, nil
	}

	fileBytes, err := fileutil.ReadBytesFromFile(pth)
	if err != nil {
		return nil, err
	}

	var previousFilePathMap map[string]string
	err = json.Unmarshal(fileBytes, &previousFilePathMap)
	if err != nil {
		return nil, err
	}

	return previousFilePathMap, nil
}

// convertDescriptorToIndicatorMap converts the descriptor to an indicator map (from path(S) - indicator(S) to
// indicator(S) - paths(M)).
func convertDescriptorToIndicatorMap(indicatorByPth map[string]string) map[string]map[string]bool {
	var indicatorMap = map[string]map[string]bool{}
	for pth, indicator := range indicatorByPth {
		if indicatorMap[indicator] == nil {
			indicatorMap[indicator] = map[string]bool{pth: true}
		} else {
			indicatorMap[indicator][pth] = true
		}
	}
	return indicatorMap
}

// convertDescriptorToIndicatorByPath converts to an indicator by path map (from indicator(S) - paths(M) to
// path(S) - indicator(S))
func convertDescriptorToIndicatorByPath(indicatorMap map[string]map[string]bool) map[string]string {
	var indicatorByPth = map[string]string{}
	for indicator, pthmap := range indicatorMap {
		for pth := range pthmap {
			indicatorByPth[pth] = indicator
		}
	}
	return indicatorByPth
}
