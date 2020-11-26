package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/djherbis/atime"
)

const (
	cachePullEndTimePath       = "/tmp/cache_pull_end_time"
	maxAge               int64 = 7 * 24 * 60 * 60 * 1000
)

// Meta ...
type Meta struct {
	AccessTime int64
}

// TODO don't we need `json` attributes?
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

func readCachePullEndTime() (int64, error) {
	if exists, err := pathutil.IsPathExists(cachePullEndTimePath); err != nil {
		return 0, err
	} else if !exists {
		return 0, errors.New("end of pull time was not found")
	}

	ts, err := fileutil.ReadStringFromFile(cachePullEndTimePath)
	if err != nil {
		return 0, err
	}
	t, err := strconv.ParseInt(ts, 10, 64)

	if err != nil {
		return 0, err
	}
	return t, nil
}

// read previous meta
// if nil -> done
// else
// previous meta + timestamp + pathToIndicatorPath -> accessed file
// accessed file -> update access time in the new meta
// 				 -> access time did not change
//						-> remove if outdated
func generateCacheMeta(cacheMetaPath string, oldPathToIndicatorPath map[string]string) (CacheMeta, map[string]string, error) {
	oldCacheMeta, err := readCacheMeta(cacheMetaPath)
	if err != nil {
		return nil, nil, err
	}

	cachePullEndTime, err := readCachePullEndTime()
	// TODO if we can't read do we stop?
	if err != nil {
		return nil, nil, err
	}

	newCacheMeta := CacheMeta{}
	newPathToIndicatorPath := map[string]string{}
	for path := range oldPathToIndicatorPath {
		t, err := atime.Stat(path)
		if err != nil {
			return nil, nil, err
		}
		at := timeToEpoch(t)

		if at > cachePullEndTime {
			// we touched this file now so we update its access timestamp in the meta
			newCacheMeta[path] = createMeta(at)
		} else {
			if m, ok := oldCacheMeta[path]; ok {
				if m.AccessTime+maxAge < timeToEpoch(time.Now()) {
					// this file was not touched and it expired in the meta
					continue
				} else {
					// this file was not touched but hasn't expired so we keep its original access time
					newCacheMeta[path] = createMeta(m.AccessTime)
				}
			} else {
				// this file was in cache but was not in meta and we not touched it in this workflow
				// TODO decide whether we add or delete here
				newCacheMeta[path] = createMeta(at)
			}
		}
		newPathToIndicatorPath[path] = oldPathToIndicatorPath[path]
	}
	return newCacheMeta, newPathToIndicatorPath, nil
}

func createMeta(at int64) Meta {
	return Meta{at}
}

func timeToEpoch(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}
