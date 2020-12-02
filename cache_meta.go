package main

import (
	"encoding/json"
	"fmt"
	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/djherbis/atime"
	"os"
	"strconv"
	"time"
)

const (
	cacheMetaPath              = "/tmp/cache-meta.json"
	cachePullEndTimePath       = "/tmp/cache_pull_end_time"
	maxAge               int64 = 7 * 24 * 60 * 60 * 1000
)

// region cacheMetaGenerator

type cacheMetaGenerator struct {
	cacheMetaReader        cacheMetaReader
	cachePullEndTimeReader cachePullEndTimeReader
	accessTimeProvider     accessTimeProvider
	timeProvider           timeProvider
	fileInfoProvider       fileInfoProvider
}

func newCacheMetaGenerator() cacheMetaGenerator {
	return cacheMetaGenerator{
		cacheMetaReader:        defaultCacheMetaReader{},
		cachePullEndTimeReader: defaultCachePullEndTimeReader{},
		accessTimeProvider:     defaultAccessTimeProvider{},
		timeProvider:           defaultTimeProvider{},
		fileInfoProvider:       defaultFileInfoProvider{},
	}
}

func (g cacheMetaGenerator) filterOldPathsAndUpdateMeta(oldPathToIndicatorPath map[string]string) (CacheMeta, map[string]string, error) {
	oldCacheMeta, err := g.cacheMetaReader.readCacheMeta(cacheMetaPath)
	if err != nil {
		switch err.(type) {
		case fileNotFoundError:
			oldCacheMeta = CacheMeta{}
		default:
			return nil, nil, err
		}
	}

	cachePullEndTime, err := g.cachePullEndTimeReader.readCachePullEndTime()
	if err != nil {
		switch err.(type) {
		case fileNotFoundError:
			cachePullEndTime = -1
		default:
			return nil, nil, err
		}
	}

	newCacheMeta := CacheMeta{}
	newPathToIndicatorPath := map[string]string{}
	for path := range oldPathToIndicatorPath {
		at, skip := g.getAccessTime(path)

		if skip {
			newPathToIndicatorPath[path] = oldPathToIndicatorPath[path]
			continue
		}

		metaAdded := g.setMeta(at, cachePullEndTime, newCacheMeta, path, oldCacheMeta)
		if metaAdded {
			newPathToIndicatorPath[path] = oldPathToIndicatorPath[path]
		}
	}
	return newCacheMeta, newPathToIndicatorPath, nil
}

func (g cacheMetaGenerator) getAccessTime(path string) (int64, bool) {
	info, err := g.fileInfoProvider.lstat(path)
	if err != nil {
		return 0, true
	}
	isSymlink := info.Mode()&os.ModeSymlink != 0
	isDir := info.IsDir()
	if isSymlink || isDir {
		return 0, true
	}
	at, err := g.accessTimeProvider.accessTime(path)
	if err != nil {
		return 0, true
	}
	return at, false
}

func (g cacheMetaGenerator) setMeta(at int64, cachePullEndTime int64, newCacheMeta CacheMeta, path string, oldCacheMeta CacheMeta) bool {
	fileAccessedSinceLastPull := at > cachePullEndTime
	if fileAccessedSinceLastPull {
		newCacheMeta[path] = newMeta(at)
		return true
	}

	m, oldMetaExists := oldCacheMeta[path]
	if oldMetaExists {
		isEntryExpired := m.AccessTime+maxAge < g.timeProvider.now()
		if isEntryExpired {
			return false
		}
		newCacheMeta[path] = m
		return true
	}

	newCacheMeta[path] = newMeta(at)
	return true
}

// endregion

// region cacheMetaReader

type cacheMetaReader interface {
	readCacheMeta(pth string) (CacheMeta, error)
}

type defaultCacheMetaReader struct{}

// readCacheMeta reads cache descriptor from pth if it exists.
func (r defaultCacheMetaReader) readCacheMeta(pth string) (CacheMeta, error) {
	if exists, err := pathutil.IsPathExists(pth); err != nil {
		return nil, err
	} else if !exists {
		return nil, fileNotFoundError{filepath: pth}
	}

	b, err := fileutil.ReadBytesFromFile(pth)
	if err != nil {
		return nil, err
	}

	var descriptor CacheMeta
	if err := json.Unmarshal(b, &descriptor); err != nil {
		return nil, err
	}

	return descriptor, nil
}

// endregion

// region cachePullEndTimeReader

type cachePullEndTimeReader interface {
	readCachePullEndTime() (int64, error)
}

type defaultCachePullEndTimeReader struct{}

func (r defaultCachePullEndTimeReader) readCachePullEndTime() (int64, error) {
	return readCachePullEndTime()
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

// endregion

// region accessTimeProvider

type accessTimeProvider interface {
	accessTime(pth string) (int64, error)
}

type defaultAccessTimeProvider struct{}

func (p defaultAccessTimeProvider) accessTime(pth string) (int64, error) {
	t, err := atime.Stat(pth)
	if err != nil {
		return 0, err
	}
	return timeToEpoch(t), nil
}

// endregion

// region timeProvider

type timeProvider interface {
	now() int64
}

type defaultTimeProvider struct{}

func (p defaultTimeProvider) now() int64 {
	t := time.Now()
	return timeToEpoch(t)
}

// endregion

// region CacheMeta

// CacheMeta ...
type CacheMeta map[string]Meta

func newMeta(at int64) Meta {
	return Meta{at}
}

// Meta ...
type Meta struct {
	AccessTime int64 `json:"access_time"`
}

// endregion

// region fileNotFoundError

type fileNotFoundError struct {
	filepath string
}

func (f fileNotFoundError) Error() string {
	return fmt.Sprintf("%s path was not found", f.filepath)
}

// endregion

// region fileInfoProvider

type fileInfoProvider interface {
	lstat(name string) (os.FileInfo, error)
}

type defaultFileInfoProvider struct{}

func (p defaultFileInfoProvider) lstat(name string) (os.FileInfo, error) {
	return os.Lstat(name)
}

// endregion

func timeToEpoch(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}
