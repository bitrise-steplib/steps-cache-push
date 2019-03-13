// Cache Push step keeps the project's cache in sync with the project's current state based on the defined files to be cached and ignored.
//
// Files to be cached are described by a path and an optional descriptor file path.
// Files to be cached can be referred by direct file path while multiple files can be selected by referring the container directory.
// Optional indicator represents a files, based on which the step synchronizes the given file(s).
// Syntax: file/path/to/cache, dir/to/cache, file/path/to/cache -> based/on/this/file, dir/to/cache -> based/on/this/file
//
// Ignore items are used to ignore certain file(s) from a directory to be cached or to mark that certain file(s) not relevant in cache synchronization.
// Syntax: not/relevant/file/or/pattern, !file/or/pattern/to/remove/from/cache
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/log"
)

const (
	cacheInfoFilePath = "/tmp/cache-info.json"
	cacheArchivePath  = "/tmp/cache-archive.tar"
)

func logErrorfAndExit(format string, args ...interface{}) {
	log.Errorf(format, args...)
	os.Exit(1)
}

func main() {
	stepStartedAt := time.Now()

	configs, err := ParseConfig()
	if err != nil {
		log.Errorf(err.Error())
		os.Exit(1)
	}

	configs.Print()
	fmt.Println()

	if configs.CacheDescriptorPath == "" {
		configs.CacheDescriptorPath = cacheInfoFilePath
	}

	// Cleaning paths
	startTime := time.Now()

	log.Infof("Cleaning paths")

	indicatorByPth := parseIncludeList(strings.Split(configs.Paths, "\n"))
	if len(indicatorByPth) == 0 {
		log.Warnf("No path to cache, skip caching...")
		os.Exit(0)
	}

	indicatorByPth, err = normalizeIndicatorByPath(indicatorByPth)
	if err != nil {
		logErrorfAndExit("Failed to parse include list: %s", err)
	}

	excludeByPattern := parseIgnoreList(strings.Split(configs.IgnoredPaths, "\n"))
	excludeByPattern, err = normalizeExcludeByPattern(excludeByPattern)
	if err != nil {
		logErrorfAndExit("Failed to parse ignore list: %s", err)
	}

	indicatorByPth, err = interleave(indicatorByPth, excludeByPattern)
	if err != nil {
		logErrorfAndExit("Failed to interleave include and ignore list: %s", err)
	}

	log.Donef("Done in %s\n", time.Now().Sub(startTime))

	if len(indicatorByPth) == 0 {
		log.Warnf("No path to cache, skip caching...")
		os.Exit(0)
	}

	// Check previous cache
	startTime = time.Now()

	log.Infof("Checking previous cache status")

	prevDescriptor, err := readCacheDescriptor(configs.CacheDescriptorPath)
	if err != nil {
		logErrorfAndExit("Failed to read previous cache descriptor: %s", err)
	}

	if prevDescriptor != nil {
		log.Printf("Previous cache info found")
	} else {
		log.Printf("No previous cache info found")
	}

	curDescriptor, err := cacheDescriptor(indicatorByPth, ChangeIndicator(configs.FingerprintMethodID))
	if err != nil {
		logErrorfAndExit("Failed to create current cache descriptor: %s", err)
	}

	log.Donef("Done in %s\n", time.Now().Sub(startTime))

	// Checking file changes
	if prevDescriptor != nil {
		startTime = time.Now()

		log.Infof("Checking for file changes")

		logDebugPaths := func(paths []string) {
			if configs.DebugMode == "true" {
				for _, pth := range paths {
					log.Debugf("- %s", pth)
				}
			}
		}

		result := compare(prevDescriptor, curDescriptor)

		log.Warnf("Previous cache is invalid, new cache will be generated:")
		log.Warnf("%d files needs to be removed", len(result.removed))
		logDebugPaths(result.removed)
		log.Warnf("%d files has changed", len(result.changed))
		logDebugPaths(result.changed)
		log.Warnf("%d files added", len(result.added))
		logDebugPaths(result.added)
		log.Debugf("%d ignored files removed", len(result.removedIgnored))
		logDebugPaths(result.removedIgnored)
		log.Debugf("%d files did not change", len(result.matching))
		logDebugPaths(result.matching)
		log.Debugf("%d ignored files added", len(result.addedIgnored))
		logDebugPaths(result.addedIgnored)

		if result.hasChanges() {
			log.Donef("File changes found in %s\n", time.Now().Sub(startTime))
		} else {
			log.Donef("No files found in %s\n", time.Now().Sub(startTime))
			log.Printf("Total time: %s", time.Now().Sub(stepStartedAt))
			os.Exit(0)
		}
	}

	// Generate cache archive
	startTime = time.Now()

	log.Infof("Generating cache archive")

	archive, err := NewArchive(cacheArchivePath, configs.CompressArchive == "true")
	if err != nil {
		logErrorfAndExit("Failed to create archive: %s", err)
	}

	var pths []string
	for pth := range indicatorByPth {
		pths = append(pths, pth)
	}

	if err := archive.Write(pths); err != nil {
		logErrorfAndExit("Failed to populate archive: %s", err)
	}

	if err := archive.WriteHeader(curDescriptor, configs.CacheDescriptorPath); err != nil {
		logErrorfAndExit("Failed to write archive header: %s", err)
	}

	if err := archive.Close(); err != nil {
		logErrorfAndExit("Failed to close archive: %s", err)
	}

	log.Donef("Done in %s\n", time.Now().Sub(startTime))

	// Upload cache archive
	startTime = time.Now()

	log.Infof("Uploading cache archive")

	if err := uploadArchive(cacheArchivePath, configs.CacheAPIURL); err != nil {
		logErrorfAndExit("Failed to upload archive: %s", err)
	}
	log.Donef("Done in %s\n", time.Now().Sub(startTime))
	log.Donef("Total time: %s", time.Now().Sub(stepStartedAt))
}
