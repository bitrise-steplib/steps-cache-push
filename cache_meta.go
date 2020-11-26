package main

import (
	"encoding/json"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/djherbis/atime"
)

// Meta ...
type Meta struct {
	AccessTime int64
}

// CacheMeta ...
type CacheMeta map[string]Meta

// readCacheMeta reads cache descriptor from pth is exists.
func readCacheMeta(pth string) (CacheMeta, error) {
	if exists, err := pathutil.IsPathExists(pth); err != nil {
		return nil, err
	} else if !exists {
		return nil, nil
	}

	b, err := fileutil.ReadBytesFromFile(pth)
	if err != nil {
		return nil, err
	}

	return parseCacheMeta(b)
}

func parseCacheMeta(b []byte) (CacheMeta, error) {
	var descriptor CacheMeta
	if err := json.Unmarshal(b, &descriptor); err != nil {
		return nil, err
	}

	return descriptor, nil
}

func generateCacheMeta(pathToIndicatorPath map[string]string) (CacheMeta, error) {

	for _ := range pathToIndicatorPath {

	}
	return nil, nil
}

// read previous meta
// if nil -> done
// else
// previous meta + timestamp + pathToIndicatorPath -> accessed file
// accessed file -> update access time in the new meta
// 				 -> access time did not change
//						-> remove if outdated

func processCacheMeta(cacheMetaPath string, cachePullEndTime int64, pathToIndicatorPath map[string]string, maxAge int64) (CacheMeta, error) {
	oldCacheMeta, err := readCacheMeta(cacheMetaPath)
	if err != nil {
		return nil, err
	}
	if oldCacheMeta == nil {

	}

	newCacheMeta := CacheMeta{}
	for path := range pathToIndicatorPath {
		t, err := atime.Stat(path)
		if err != nil {
			return nil, err
		}

		// cleanup outdated cache entries
		at := t.UnixNano() / int64(time.Millisecond)

		if at <= cachePullEndTime { // was not access during build
			curr := time.Now().UnixNano() / int64(time.Millisecond)

			if at+maxAge < curr {
				// delete outdated cache entry
				delete(pathToIndicatorPath, path)
				continue
			}

			if oldCacheMeta == nil {
				newCacheMeta[path] = Meta{AccessTime: at}
			} else {
				oldAccessTime := oldCacheMeta[path]
				newCacheMeta[path] = oldAccessTime
			}
			continue
		}

		newCacheMeta[path] = Meta{AccessTime: at}
	}

}
