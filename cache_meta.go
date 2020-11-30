package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/xcode-project/pretty"
	"github.com/djherbis/atime"
)

const (
	cachePullEndTimePath       = "/tmp/cache_pull_end_time"
	maxAge               int64 = 7 * 24 * 60 * 60 * 1000
)

// Meta ...
type Meta struct {
	AccessTime int64 `json:"access_time"`
}

// CacheMeta ...
type CacheMeta map[string]Meta

type fileNotFoundError struct {
	filepath string
}

func (f fileNotFoundError) Error() string {
	return fmt.Sprintf("%s path was not found", f.filepath)
}

// readCacheMeta reads cache descriptor from pth is exists.
func readCacheMeta(pth string) (CacheMeta, error) {
	if exists, err := pathutil.IsPathExists(pth); err != nil {
		return nil, err
	} else if !exists {
		return nil, fileNotFoundError{filepath: pth}
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
		return 0, fileNotFoundError{filepath: cachePullEndTimePath}
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
		switch err.(type) {
		case *fileNotFoundError:
			fmt.Printf("Cache meta file was not found at %s\n", cacheMetaPath)
			return nil, oldPathToIndicatorPath, nil
		default:
			return nil, nil, err
		}
	}

	cachePullEndTime, err := readCachePullEndTime()
	if err != nil {
		switch err.(type) {
		case *fileNotFoundError:
			fmt.Printf("Cache Pull endtime file was not found at %s\n", cachePullEndTimePath)
			return nil, oldPathToIndicatorPath, nil
		default:
			return nil, nil, err
		}
	}

	fmt.Printf("Pull end time: %d\n", cachePullEndTime)
	fmt.Printf("Old cache meta: %s\n", pretty.Object(oldCacheMeta))

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
					newCacheMeta[path] = m
				}
			} else {
				// this file was in cache but was not in meta and we not touched it in this workflow
				newCacheMeta[path] = createMeta(at)
			}
		}
		newPathToIndicatorPath[path] = oldPathToIndicatorPath[path]
	}
	fmt.Printf("New cache meta: %s\n", pretty.Object(newCacheMeta))
	return newCacheMeta, newPathToIndicatorPath, nil
}

func createMeta(at int64) Meta {
	return Meta{at}
}

func timeToEpoch(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}
