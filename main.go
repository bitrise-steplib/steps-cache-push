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
	syslog "log"
	"path/filepath"

	"github.com/bitrise-io/go-utils/log"
	"github.com/hendych/fast-archiver/falib"
)

const (
	cacheInfoFilePath = "/tmp/cache-info.json"
	stackVersionsPath = "/tmp/archive_info.json"
	stepID            = "cache-push"
)

type MultiLevelLogger struct {
	logger  *syslog.Logger
	verbose bool
}

func (l *MultiLevelLogger) Verbose(v ...interface{}) {
	if l.verbose {
		l.logger.Println(v...)
	}
}
func (l *MultiLevelLogger) Warning(v ...interface{}) {
	l.logger.Println(v...)
}

func logErrorfAndExit(format string, args ...interface{}) {
	log.Errorf(format, args...)
	os.Exit(1)
}

func main() {
	stepStartedAt := time.Now()

	configs, err := ParseConfig()
	if err != nil {
		logErrorfAndExit(err.Error())
	}

	configs.Print()
	fmt.Println()

	log.SetEnableDebugLog(configs.DebugMode)

    cacheArchivePath := ""
    startTime := time.Now()

	if configs.UseFastArchiver == "true" {
	    // Use Fast Archiver

        log.Infof("Using fast archive... Generating archive")
		cacheArchivePath = "/tmp/cache-archive.fast-archive"
		fastArchiveStartTime := time.Now()

		var fastArchiveSize int64
        var outputFile *os.File
        if cacheArchivePath != "" {
        	file, err := os.Create(cacheArchivePath)
        	if err != nil {
        		logErrorfAndExit("Error creating output file:", err.Error())
        	}
        	outputFile = file
        } else {
        	outputFile = os.Stdout
		}

        archive := falib.NewArchiver(outputFile)
        archive.BlockSize = uint16(4096)
		archive.DirScanQueueSize = 128
		archive.FileReadQueueSize = 128
		archive.BlockQueueSize = 128
		archive.ExcludePatterns = filepath.SplitList(configs.IgnoredPaths)
		archive.DirReaderCount = 16
		archive.FileReaderCount = 16
		archive.Logger = &MultiLevelLogger{syslog.New(os.Stderr, "", 0), true}

        for pth := range parseIncludeList(strings.Split(configs.Paths, "\n")) {
        	archive.AddDir(pth)
		}
        err := archive.Run()
        if err != nil {
        	logErrorfAndExit("Fatal error in fast archiver: ", err.Error())
		}

		fileInfo, err := outputFile.Stat()
		if err == nil {
			fastArchiveSize = fileInfo.Size()
		}

		outputFile.Close()

		log.Infof("Done Generating Archive in %s\n", time.Since(fastArchiveStartTime))

		if configs.CompressArchive != "false" {
			compressedSize, err := FastArchiveCompress(cacheArchivePath, "lz4")//configs.CompressArchive)
			if err != nil {
				logErrorfAndExit("Error when compressing file: ", err.Error())
			}
			log.Infof("Archive compressed by %s%", (100 - (compressedSize / fastArchiveSize * 100)))
		}

        log.Donef("Total done in %s\n", time.Since(startTime))
	} else {
	    // Use Tar Archiver

        // Cleaning paths
    	log.Infof("Cleaning paths")

		pathToIndicatorPath := parseIncludeList(strings.Split(configs.Paths, "\n"))
    	if len(pathToIndicatorPath) == 0 {
    		log.Warnf("No path to cache, skip caching...")
    		os.Exit(0)
    	}

    	pathToIndicatorPath, err := normalizeIndicatorByPath(pathToIndicatorPath)
    	if err != nil {
    		logErrorfAndExit("Failed to parse include list: %s", err)
    	}

		excludeByPattern := parseIgnoreList(strings.Split(configs.IgnoredPaths, "\n"))
    	excludeByPattern, err = normalizeExcludeByPattern(excludeByPattern)
    	if err != nil {
    		logErrorfAndExit("Failed to parse ignore list: %s", err)
    	}

    	pathToIndicatorPath = interleave(pathToIndicatorPath, excludeByPattern)

    	log.Donef("Done in %s\n", time.Since(startTime))

    	if len(pathToIndicatorPath) == 0 {
    		log.Warnf("No path to cache, skip caching...")
    		os.Exit(0)
    	}

    	// Check previous cache
    	startTime = time.Now()

    	log.Infof("Checking previous cache status")

    	prevDescriptor, err := readCacheDescriptor(cacheInfoFilePath)
    	if err != nil {
    		logErrorfAndExit("Failed to read previous cache descriptor: %s", err)
    	}

    	if prevDescriptor != nil {
    		log.Printf("Previous cache info found at: %s", cacheInfoFilePath)
    	} else {
    		log.Printf("No previous cache info found")
    	}

		curDescriptor, err := cacheDescriptor(pathToIndicatorPath, ChangeIndicator(configs.FingerprintMethodID))
    	if err != nil {
    		logErrorfAndExit("Failed to create current cache descriptor: %s", err)
    	}

    	log.Donef("Check previous cache done in %s\n", time.Since(startTime))

    	// Checking file changes
    	if prevDescriptor != nil {
    		startTime = time.Now()

    		log.Infof("Checking for file changes")

    		logDebugPaths := func(paths []string) {
    			for _, pth := range paths {
    				log.Debugf("- %s", pth)
    			}
    		}

    		result := compare(prevDescriptor, curDescriptor)

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
    			log.Donef("File changes found in %s\n", time.Since(startTime))
    		} else {
    			log.Donef("No files found in %s\n", time.Since(startTime))
    			log.Printf("Total time: %s", time.Since(stepStartedAt))
    			os.Exit(0)
    		}
    	}

    	// Generate cache archive
    	startTime = time.Now()

    	log.Infof("Generating cache archive")
		cacheArchivePath = "/tmp/cache-archive.tar"

		archive, err := NewArchive(cacheArchivePath, configs.CompressArchive)
        if err != nil {
            logErrorfAndExit("Failed to create archive: %s", err)
        }

		stackData, err := stackVersionData(configs.StackID)
        if err != nil {
            logErrorfAndExit("Failed to get stack version info: %s", err)
        }
        // This is the first file written, to speed up reading it in subsequent builds
        if err = archive.writeData(stackData, stackVersionsPath); err != nil {
            logErrorfAndExit("Failed to write cache info to archive, error: %s", err)
        }

        if err := archive.Write(pathToIndicatorPath); err != nil {
            logErrorfAndExit("Failed to populate archive: %s", err)
        }

        if err := archive.WriteHeader(curDescriptor, cacheInfoFilePath); err != nil {
            logErrorfAndExit("Failed to write archive header: %s", err)
        }

        if err := archive.Close(); err != nil {
            logErrorfAndExit("Failed to close archive: %s", err)
        }

        log.Donef("Generating Archive (plus compress if any) Done in %s\n", time.Since(startTime))
	}

	// Upload cache archive
	startTime = time.Now()

	log.Infof("Uploading cache archive")

	if err := uploadArchive(cacheArchivePath, configs.CacheAPIURL); err != nil {
		logErrorfAndExit("Failed to upload archive: %s", err)
	}
	log.Donef("Done in %s\n", time.Since(startTime))
	log.Donef("Total Archive + Upload time: %s", time.Since(stepStartedAt))
}
