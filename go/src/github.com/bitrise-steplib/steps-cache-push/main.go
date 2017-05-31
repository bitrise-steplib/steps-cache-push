package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/cmdex"
	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/ryanuber/go-glob"
)

const (
	indicatorFileSeparator = "->"

	fingerprintMethodIDContentChecksum = "file-content-hash"
	fingerprintMethodIDFileModTime     = "file-mod-time"
)

var (
	gIsDebugMode = false
)

// StepParamsPathItemModel ...
type StepParamsPathItemModel struct {
	Path              string
	IndicatorFilePath string
}

// RedactedLog ...
type RedactedLog struct {
	counter int
}

// StepParamsModel ...
type StepParamsModel struct {
	PathItems            []StepParamsPathItemModel
	IgnoreCheckOnPaths   []string
	CacheAPIURL          string
	CompareCacheInfoPath string
	IsDebugMode          bool
	FingerprintMethodID  string
	CompressArchive      bool
}

// CacheContentModel ...
type CacheContentModel struct {
	DestinationPath       string `json:"destination_path"`
	RelativePathInArchive string `json:"relative_path_in_archive"`
}

// FingerprintMetaModel ...
type FingerprintMetaModel struct {
	FingerprintSource string `json:"fingerprint_source"`
}

// CacheInfosModel ...
type CacheInfosModel struct {
	Fingerprint      string                          `json:"fingerprint"`
	Contents         []CacheContentModel             `json:"cache_contents"`
	FingerprintsMeta map[string]FingerprintMetaModel `json:"fingerprint_meta"`
}

// PrintfInc ...
func (redactedLog *RedactedLog) PrintfInc(format string, v ...interface{}) {
	if redactedLog.counter < 10 {
		log.Printf(format, v)
	}
	if redactedLog.counter == 10 {
		log.Printf("List truncated...")
	}
	if !gIsDebugMode {
		redactedLog.counter++
	}
}

// PrintfExc ...
func (redactedLog *RedactedLog) PrintfExc(format string, v ...interface{}) {
	if redactedLog.counter < 10 {
		log.Printf(format, v)
	}
}

func readCacheInfoFromFile(filePth string) (CacheInfosModel, error) {
	jsonBytes, err := ioutil.ReadFile(filePth)
	if err != nil {
		return CacheInfosModel{}, fmt.Errorf("Failed to read file: %s", err)
	}
	var cacheInfo CacheInfosModel
	if err := json.Unmarshal(jsonBytes, &cacheInfo); err != nil {
		return CacheInfosModel{}, fmt.Errorf("Failed to parse JSON: %s", err)
	}
	return cacheInfo, nil
}

func parseStepParamsPathItemModelFromString(itmStr string) (StepParamsPathItemModel, error) {
	splits := strings.Split(itmStr, indicatorFileSeparator)
	if len(splits) > 2 {
		return StepParamsPathItemModel{}, fmt.Errorf("The indicator file separator (%s) is specified more than once: %s",
			indicatorFileSeparator, itmStr)
	}

	aCachePth := strings.TrimSpace(splits[0])
	if aCachePth == "" {
		return StepParamsPathItemModel{}, fmt.Errorf("No path specified in item: %s",
			itmStr)
	}
	anIndicatorFilePath := ""
	if len(splits) == 2 {
		anIndicatorFilePath = strings.TrimSpace(splits[1])
	}

	return StepParamsPathItemModel{
		Path:              aCachePth,
		IndicatorFilePath: anIndicatorFilePath,
	}, nil
}

// CreateStepParamsFromEnvs ...
func CreateStepParamsFromEnvs() (StepParamsModel, error) {
	cacheDirs := os.Getenv("cache_paths")
	globalCacheDirs := os.Getenv("bitrise_cache_include_paths")
	ignoreCheckOnPaths := os.Getenv("ignore_check_on_paths")
	globalIgnoreCheckOnPaths := os.Getenv("bitrise_cache_exclude_paths")
	cacheAPIURL := os.Getenv("cache_api_url")
	fingerprintMethodID := os.Getenv("fingerprint_method")
	compressArchive := os.Getenv("compress_archive")

	if cacheAPIURL == "" {
		return StepParamsModel{}, errors.New("No cache_api_url input specified")
	}
	if fingerprintMethodID != fingerprintMethodIDContentChecksum && fingerprintMethodID != fingerprintMethodIDFileModTime {
		return StepParamsModel{}, fmt.Errorf("fingerprint_method (%s) is invalid", fingerprintMethodID)
	}
	if compressArchive == "" {
		return StepParamsModel{}, errors.New("No compress_archive input specified")
	}
	if compressArchive != "true" && compressArchive != "false" {
		return StepParamsModel{}, fmt.Errorf("compress_archive (%s) is invalid", compressArchive)
	}

	stepParams := StepParamsModel{
		CacheAPIURL:          cacheAPIURL,
		CompareCacheInfoPath: os.Getenv("compare_cache_info_path"),
		IsDebugMode:          os.Getenv("is_debug_mode") == "true",
		CompressArchive:      compressArchive == "true",
		FingerprintMethodID:  fingerprintMethodID,
	}

	// Cache Path Items
	{
		scanner := bufio.NewScanner(strings.NewReader(cacheDirs + "\n" + globalCacheDirs))
		for scanner.Scan() {
			aCachePathItemDef := scanner.Text()
			aCachePathItemDef = strings.TrimSpace(aCachePathItemDef)
			if aCachePathItemDef == "" {
				continue
			}

			pthItm, err := parseStepParamsPathItemModelFromString(aCachePathItemDef)
			if err != nil {
				return StepParamsModel{}, fmt.Errorf("Invalid item: %s", err)
			}

			stepParams.PathItems = append(stepParams.PathItems, pthItm)
		}
		if err := scanner.Err(); err != nil {
			return StepParamsModel{}, fmt.Errorf("Failed to scan the the cache_paths input: %s", err)
		}
	}

	// Ignore Check on Paths items
	{
		scanner := bufio.NewScanner(strings.NewReader(ignoreCheckOnPaths + "\n" + globalIgnoreCheckOnPaths))
		for scanner.Scan() {
			aPthItmDef := scanner.Text()
			aPthItmDef = strings.TrimSpace(aPthItmDef)
			if aPthItmDef == "" {
				continue
			}

			stepParams.IgnoreCheckOnPaths = append(stepParams.IgnoreCheckOnPaths, aPthItmDef)
		}
		if err := scanner.Err(); err != nil {
			return StepParamsModel{}, fmt.Errorf("Failed to scan the the cache_paths input: %s", err)
		}
	}

	return stepParams, nil
}

func sha1ChecksumOfFile(pth string) ([]byte, error) {
	fileHasher := sha1.New()

	f, err := os.Open(pth)
	if err != nil {
		return nil, fmt.Errorf("Failed to open file to create checksum, error: %s", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf(" (!) Failed to close file (%s) after creating its checksum, error: %s", pth, err)
		}
	}()

	if _, err := io.Copy(fileHasher, f); err != nil {
		return nil, fmt.Errorf("Failed create hash of file, error: %s", err)
	}

	return fileHasher.Sum(nil), nil
}

func fingerprintSourceStringOfFile(pth string, fileInfo os.FileInfo, fingerprintMethodID string) (string, error) {
	fpMethodResult := ""
	if fingerprintMethodID == fingerprintMethodIDContentChecksum {
		fileShaChecksum, err := sha1ChecksumOfFile(pth)
		if err != nil {
			return "", fmt.Errorf("Failed to calculate checksum of file: %s", err)
		}
		fpMethodResult = fmt.Sprintf("sha1:%x", fileShaChecksum)
	} else if fingerprintMethodID == fingerprintMethodIDFileModTime {
		fpMethodResult = fmt.Sprintf("@%d", fileInfo.ModTime().Unix())
	} else {
		return "", fmt.Errorf("Unsupported Fingerprint Method: %s", fingerprintMethodID)
	}

	return fmt.Sprintf("[%s]-[%dB]-[0x%o]-[%s]", pth, fileInfo.Size(), fileInfo.Mode(), fpMethodResult), nil
}

func isShouldIgnorePathFromFingerprint(aPth string, ignorePaths []string) bool {
	for _, anIgnorePth := range ignorePaths {
		if strings.Contains(anIgnorePth, "*") {
			// glob
			if glob.Glob(anIgnorePth, aPth) {
				// log.Printf(" [IGNORE:glob] %s (%s)", aPth, anIgnorePth)
				return true
			}
		} else {
			// prefix
			if strings.HasPrefix(aPth, anIgnorePth) {
				// log.Printf(" [IGNORE:prefix] %s (%s)", aPth, anIgnorePth)
				return true
			}
		}
	}
	return false
}

// fingerprintOfPaths ...
func fingerprintOfPaths(pathItms []StepParamsPathItemModel, ignorePaths []string, fingerprintMethodID string) ([]byte, map[string]FingerprintMetaModel, error) {
	fingerprintMeta := map[string]FingerprintMetaModel{}

	absIgnorePaths := []string{}
	for _, anIgnorePth := range ignorePaths {
		aAbsPth, err := pathutil.AbsPath(anIgnorePth)
		if err != nil {
			return []byte{}, fingerprintMeta, fmt.Errorf("Failed to get Absolute path for ignore path item: %s", anIgnorePth)
		}
		absIgnorePaths = append(absIgnorePaths, aAbsPth)
	}

	fingerprintHash := sha1.New()
	isFingerprintGenerated := false // at least one item from the path should generate a fingerprint for it!

	for _, aPathItem := range pathItms {
		isFingerprintGeneratedForPathItem := false

		theFingerprintSourcePath := aPathItem.Path
		isIndicatorFile := false
		if aPathItem.IndicatorFilePath != "" {
			theFingerprintSourcePath = aPathItem.IndicatorFilePath
			isIndicatorFile = true

			if gIsDebugMode {
				log.Printf(" ==> Using Indicator File as fingerprint source: %s", theFingerprintSourcePath)
			}
		}

		if aPathItem.Path == "" {
			continue
		}

		theFingerprintSourcePath = path.Clean(theFingerprintSourcePath)
		if theFingerprintSourcePath == "/" {
			return []byte{}, fingerprintMeta, errors.New("Failed to check the specified path: caching the whole root (/) is forbidden (path was '/')")
		}

		fingerprintSourceAbsPth, err := pathutil.AbsPath(theFingerprintSourcePath)
		if err != nil {
			return []byte{}, fingerprintMeta, fmt.Errorf("Failed to get Absolute path of item (%s): %s", theFingerprintSourcePath, err)
		}

		fileInfo, isExist, err := pathutil.PathCheckAndInfos(fingerprintSourceAbsPth)
		if err != nil {
			return []byte{}, fingerprintMeta, fmt.Errorf("Failed to check the specified path: %s", err)
		}
		if !isExist {
			return []byte{}, fingerprintMeta, errors.New("Specified path does not exist")
		}

		if fileInfo.IsDir() {
			err := filepath.Walk(fingerprintSourceAbsPth, func(aPath string, aFileInfo os.FileInfo, walkErr error) error {
				if walkErr != nil {
					log.Printf(" (!) Error checking file (%s): %s", aPath, walkErr)
				}
				if aFileInfo.IsDir() {
					// directory - skipping
					return nil
				}
				if !aFileInfo.Mode().IsRegular() {
					if gIsDebugMode {
						log.Printf(" (i) File (%s) is not a regular file (it's a symlink, a device, or something similar) - skipping", aPath)
					}
					return nil
				}

				if isShouldIgnorePathFromFingerprint(aPath, absIgnorePaths) {
					if gIsDebugMode {
						log.Printf(" [IGNORE] path from fingerprint: %s", aPath)
					}
					return nil
				}

				fileFingerprintSource, err := fingerprintSourceStringOfFile(aPath, aFileInfo, fingerprintMethodID)
				if err != nil {
					return fmt.Errorf("Failed to generate fingerprint source for file (%s), error: %s", aPath, err)
				}

				if gIsDebugMode {
					log.Printf(" * fileFingerprintSource (%s): %#v", aPath, fileFingerprintSource)
				}
				fingerprintMeta[aPath] = FingerprintMetaModel{FingerprintSource: fileFingerprintSource}

				if _, err := io.WriteString(fingerprintHash, fileFingerprintSource); err != nil {
					return fmt.Errorf("Failed to write fingerprint source string (%s) to fingerprint hash: %s",
						fileFingerprintSource, err)
				}

				isFingerprintGeneratedForPathItem = true
				return nil
			})
			if err != nil {
				return []byte{}, fingerprintMeta, fmt.Errorf("Failed to walk through the specified directory (%s): %s", theFingerprintSourcePath, err)
			}
		} else {
			if isShouldIgnorePathFromFingerprint(fingerprintSourceAbsPth, absIgnorePaths) {
				log.Printf(" [IGNORE] path from fingerprint: %s", fingerprintSourceAbsPth)
				return []byte{}, fingerprintMeta, fmt.Errorf("Failed to generate fingerprint for path - no file found to generate one: %s",
					theFingerprintSourcePath)
			}

			if isIndicatorFile {
				// for indicator files always use content checksum fingerprint
				fingerprintMethodID = fingerprintMethodIDContentChecksum
			}
			fileFingerprintSource, err := fingerprintSourceStringOfFile(fingerprintSourceAbsPth, fileInfo, fingerprintMethodID)
			if err != nil {
				return []byte{}, fingerprintMeta, fmt.Errorf("Failed to generate fingerprint source for file (%s), error: %s", theFingerprintSourcePath, err)
			}

			if gIsDebugMode {
				log.Printf(" -> fileFingerprintSource (%s): %#v", theFingerprintSourcePath, fileFingerprintSource)
			}
			fingerprintMeta[theFingerprintSourcePath] = FingerprintMetaModel{FingerprintSource: fileFingerprintSource}

			if _, err := io.WriteString(fingerprintHash, fileFingerprintSource); err != nil {
				return []byte{}, fingerprintMeta, fmt.Errorf("Failed to write fingerprint source string (%s) to fingerprint hash: %s",
					fileFingerprintSource, err)
			}
			isFingerprintGeneratedForPathItem = true
		}

		if isFingerprintGeneratedForPathItem {
			isFingerprintGenerated = true
		} else {
			log.Println(colorstring.Yellowf(" (i) No fingerprint generated for path: (%s) - no file found to generate one", theFingerprintSourcePath))
		}
	}

	if !isFingerprintGenerated {
		return []byte{}, fingerprintMeta, errors.New("Failed to generate fingerprint for paths - no file found to generate one")
	}

	return fingerprintHash.Sum(nil), fingerprintMeta, nil
}

func compareFingerprintMetas(currentMeta, previousMeta map[string]FingerprintMetaModel) {
	if currentMeta == nil {
		log.Printf(" (!) compareFingerprintMetas: Current Fingerprint Metas empty - can't compare")
		return
	}
	if previousMeta == nil {
		log.Printf(" (!) compareFingerprintMetas: Previous Fingerprint Metas empty - can't compare")
		return
	}

	fmt.Println()
	log.Println("=> Comparing cache meta information ...")

	prevItmsLookup := map[string]bool{}
	for aPath := range previousMeta {
		prevItmsLookup[aPath] = true
	}

	redactedLog := &RedactedLog{}

	for aPath, currValue := range currentMeta {
		prevValue, isFound := previousMeta[aPath]
		if !isFound {
			redactedLog.PrintfInc("   [ADDED] (No value found in the Previous Cache Meta for path): %s", aPath)
		} else {
			delete(prevItmsLookup, aPath)
			if currValue != prevValue {
				redactedLog.PrintfInc(" (i) File changed: %s", aPath)
				redactedLog.PrintfExc("     Previous fingerprint (source): %s", prevValue)
				redactedLog.PrintfExc("     Current  fingerprint (source): %s", currValue)
			} else if gIsDebugMode {
				redactedLog.PrintfExc(" (i) File fingerprint (source) match: %s", aPath)
			}
		}
	}

	for aPath := range prevItmsLookup {
		redactedLog.PrintfInc("   [REMOVED] (File (meta info about the file) is no longer in the cache, but it was in the previous one): %s", aPath)
	}

	fmt.Println()
}

func cleanupCachePaths(requestedCachePathItems []StepParamsPathItemModel) []StepParamsPathItemModel {
	filteredPathItems := []StepParamsPathItemModel{}
	for _, aOrigPthItm := range requestedCachePathItems {
		if aOrigPthItm.Path == "" {
			continue
		}
		aPath := path.Clean(aOrigPthItm.Path)
		if aPath == "/" {
			log.Println(" " + colorstring.Yellow("(!) Skipping") + ": Failed to check the specified path: path was '/' - caching the whole root (/) directory is")
			continue
		}

		absItemPath, err := pathutil.AbsPath(aPath)
		if err != nil {
			log.Printf("Failed to get Absolute path for item (%s): %s", aPath, err)
			continue
		}
		aPath = absItemPath

		// check the Path
		{
			fileInfo, isExist, err := pathutil.PathCheckAndInfos(aPath)
			if err != nil {
				log.Printf(" "+colorstring.Yellow("(!) Skipping")+" (%s): Failed to check the specified path: %s", aOrigPthItm.Path, err)
				continue
			}
			if !isExist {
				log.Printf(" "+colorstring.Yellow("(!) Skipping")+" (%s): Specified path does not exist", aOrigPthItm.Path)
				continue
			}

			if !fileInfo.IsDir() {
				if !fileInfo.Mode().IsRegular() {
					log.Printf(" (i) File (%s) is not a regular file (it's a symlink, a device, or something similar) - skipping", aOrigPthItm.Path)
					continue
				}
			}
		}

		// check the Indicator File, if specified
		if aOrigPthItm.IndicatorFilePath != "" {
			{
				fileInfo, isExist, err := pathutil.PathCheckAndInfos(aOrigPthItm.IndicatorFilePath)
				if err != nil {
					log.Printf(" "+colorstring.Yellow("(!) Skipping")+" (%s): Failed to check the specified path: %s", aOrigPthItm.IndicatorFilePath, err)
					continue
				}
				if !isExist {
					log.Printf(" "+colorstring.Yellow("(!) Skipping")+" (%s): Specified path does not exist", aOrigPthItm.IndicatorFilePath)
					continue
				}

				if fileInfo.IsDir() {
					log.Printf(" (i) The Indicator can only be a file, but a directory is specified (%s) - skipping", aOrigPthItm.IndicatorFilePath)
					continue
				}
				if !fileInfo.Mode().IsRegular() {
					log.Printf(" (i) The Indicator file (%s) is not a regular file (it's a symlink, a device, or something similar) - skipping", aOrigPthItm.IndicatorFilePath)
					continue
				}
			}
		}

		filteredPathItems = append(filteredPathItems, aOrigPthItm)
	}
	return filteredPathItems
}

func (stepParams *StepParamsModel) createCacheArchiveFromPaths(pathItemsToCache []StepParamsPathItemModel, archiveContentFingerprint string, fingerprintsMeta map[string]FingerprintMetaModel) (string, error) {
	cacheArchiveTmpBaseDirPth, err := pathutil.NormalizedOSTempDirPath("")
	if err != nil {
		return "", fmt.Errorf("Failed to create temporary Cache Archive directory: %s", err)
	}
	if gIsDebugMode {
		fmt.Println()
		log.Printf("=> cacheArchiveTmpBaseDirPth: %s", cacheArchiveTmpBaseDirPth)
		fmt.Println()
	}

	cacheContentDirName := "content"
	cacheContentDirPath := filepath.Join(cacheArchiveTmpBaseDirPth, cacheContentDirName)
	if err := pathutil.EnsureDirExist(cacheContentDirPath); err != nil {
		return "", fmt.Errorf("Failed to create Cache Content directory: %s", err)
	}

	cacheInfo := CacheInfosModel{
		Fingerprint:      archiveContentFingerprint,
		Contents:         []CacheContentModel{},
		FingerprintsMeta: fingerprintsMeta,
	}
	for idx, aPathItem := range pathItemsToCache {
		aPath := path.Clean(aPathItem.Path)
		absItemPath, err := pathutil.AbsPath(aPath)
		if err != nil {
			return "", fmt.Errorf("Failed to get Absolute path for item (%s): %s", aPath, err)
		}

		fileInfo, isExist, err := pathutil.PathCheckAndInfos(absItemPath)
		if err != nil {
			return "", fmt.Errorf("Failed to check path (%s): %s", absItemPath, err)
		}
		if !isExist {
			return "", fmt.Errorf("Path does not exist: %s", absItemPath)
		}

		archiveCopyRsyncParams := []string{}
		itemRelPathInArchive := fmt.Sprintf("c-%d", idx)

		if fileInfo.IsDir() {
			archiveCopyRsyncParams = []string{"-avhP", absItemPath + "/", filepath.Join(cacheContentDirPath, itemRelPathInArchive+"/")}
		} else {
			archiveCopyRsyncParams = []string{"-avhP", absItemPath, filepath.Join(cacheContentDirPath, itemRelPathInArchive)}
		}

		cacheInfo.Contents = append(cacheInfo.Contents, CacheContentModel{
			DestinationPath:       aPath,
			RelativePathInArchive: itemRelPathInArchive,
		})

		archiveCopyRsyncParams = append(archiveCopyRsyncParams, "--include", "*/")
		for _, ignorePth := range stepParams.IgnoreCheckOnPaths {
			archiveCopyRsyncParams = append(archiveCopyRsyncParams, "--exclude", ignorePth)
		}

		if gIsDebugMode {
			log.Printf(" $ rsync %s", archiveCopyRsyncParams)
		}

		if fullOut, err := cmdex.RunCommandAndReturnCombinedStdoutAndStderr("rsync", archiveCopyRsyncParams...); err != nil {
			log.Printf(" [!] Failed to sync archive target (%s), full output (stdout & stderr) was: %s", absItemPath, fullOut)
			return "", fmt.Errorf("Failed to sync archive target (%s): %s", absItemPath, err)
		}
	}

	// store CacheInfo into the cache content dir
	jsonBytes, err := json.Marshal(cacheInfo)
	if err != nil {
		return "", fmt.Errorf("Failed to generate Cache Info JSON: %s", err)
	}
	cacheInfoFilePath := filepath.Join(cacheContentDirPath, "cache-info.json")
	if err := ioutil.WriteFile(cacheInfoFilePath, jsonBytes, 0666); err != nil {
		return "", fmt.Errorf("Failed to write Cache Info JSON to file (%s): %s", cacheInfoFilePath, err)
	}

	cacheArchiveFileName := "cache.tar.gz"
	cacheArchiveFilePath := filepath.Join(cacheArchiveTmpBaseDirPth, cacheArchiveFileName)

	tarFlagsSlice := "-c"

	if gIsDebugMode {
		tarFlagsSlice += "v"
	}

	if stepParams.CompressArchive {
		tarFlagsSlice += "z"
	}

	tarCmdParams := []string{tarFlagsSlice + "f", cacheArchiveFilePath, "."}
	if gIsDebugMode {
		log.Printf(" $ tar %s", tarCmdParams)
	}
	if fullOut, err := cmdex.RunCommandInDirAndReturnCombinedStdoutAndStderr(cacheContentDirPath, "tar", tarCmdParams...); err != nil {
		if !gIsDebugMode {
			log.Printf(" [!] Failed to create cache archive, error: %s", fullOut)
		} else {
			log.Printf(" [!] Failed to create cache archive, full output (stdout & stderr) was: %s", fullOut)
		}
		return "", fmt.Errorf("Failed to create cache archive, error was: %s", err)
	}

	return cacheArchiveFilePath, nil
}

func _tryToUploadArchive(uploadURL string, archiveFilePath string) error {
	archFile, err := os.Open(archiveFilePath)
	if err != nil {
		return fmt.Errorf("Failed to open archive file for upload (%s): %s", archiveFilePath, err)
	}
	isFileCloseRequired := true
	defer func() {
		if !isFileCloseRequired {
			return
		}
		if err := archFile.Close(); err != nil {
			log.Printf(" (!) Failed to close archive file (%s): %s", archiveFilePath, err)
		}
	}()

	fileInfo, err := archFile.Stat()
	if err != nil {
		return fmt.Errorf("Failed to get File Stats of the Archive file (%s): %s", archiveFilePath, err)
	}
	fileSize := fileInfo.Size()

	req, err := http.NewRequest("PUT", uploadURL, archFile)
	if err != nil {
		return fmt.Errorf("Failed to create upload request: %s", err)
	}

	// req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Add("Content-Length", strconv.FormatInt(fileSize, 10))
	req.ContentLength = fileSize
	if gIsDebugMode {
		log.Printf("=> req: %#v", req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to upload: %s", err)
	}
	isFileCloseRequired = false
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf(" [!] Failed to close response body: %s", err)
		}
	}()

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Failed to read response: %s", err)
	}

	if resp.StatusCode != 200 {
		log.Printf("=> Upload response: %s", responseBytes)
		return fmt.Errorf("Failed to upload file, response code was: %d", resp.StatusCode)
	}
	if gIsDebugMode {
		log.Printf("=> Upload response code: %d", resp.StatusCode)
	}

	return nil
}

// CacheUploadAPIRequestDataModel ...
type CacheUploadAPIRequestDataModel struct {
	FileSizeInBytes int64 `json:"file_size_in_bytes"`
}

// GenerateUploadURLRespModel ...
type GenerateUploadURLRespModel struct {
	UploadURL string `json:"upload_url"`
}

func getCacheUploadURL(cacheAPIURL string, fileSizeInBytes int64) (string, error) {
	requestDataModel := CacheUploadAPIRequestDataModel{
		FileSizeInBytes: fileSizeInBytes,
	}

	requestJSONBytes, err := json.Marshal(requestDataModel)
	if err != nil {
		return "", fmt.Errorf("Failed to JSON marshal CacheUploadAPIRequestDataModel: %s", err)
	}

	req, err := http.NewRequest("POST", cacheAPIURL, bytes.NewBuffer(requestJSONBytes))
	if err != nil {
		return "", fmt.Errorf("Failed to create request: %s", err)
	}
	// req.Header.Set("Content-Type", "application/json")
	// req.Header.Set("Api-Token", apiToken)
	// req.Header.Set("X-Bitrise-Event", "hook")

	client := &http.Client{
		Timeout: 20 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Failed to send request: %s", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf(" [!] Exception: Failed to close response body, error: %s", err)
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Request sent, but failed to read response body (http-code:%d): %s", resp.StatusCode, body)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 202 {
		return "", fmt.Errorf("Upload URL was rejected (http-code:%d): %s", resp.StatusCode, body)
	}

	var respModel GenerateUploadURLRespModel
	if err := json.Unmarshal(body, &respModel); err != nil {
		return "", fmt.Errorf("Request sent, but failed to parse JSON response (http-code:%d): %s", resp.StatusCode, body)
	}

	if respModel.UploadURL == "" {
		return "", fmt.Errorf("Request sent, but Upload URL is empty (http-code:%d): %s", resp.StatusCode, body)
	}

	return respModel.UploadURL, nil
}

func uploadArchive(stepParams StepParamsModel, archiveFilePath string) error {
	fi, err := os.Stat(archiveFilePath)
	if err != nil {
		return fmt.Errorf("Failed to get File Infos of Archive (%s): %s", archiveFilePath, err)
	}
	sizeInBytes := fi.Size()
	log.Printf("   Archive file size: %d bytes / %f MB", sizeInBytes, (float64(sizeInBytes) / 1024.0 / 1024.0))

	uploadURL, err := getCacheUploadURL(stepParams.CacheAPIURL, sizeInBytes)
	if err != nil {
		return fmt.Errorf("Failed to generate Upload URL: %s", err)
	}
	if gIsDebugMode {
		log.Printf("   [DEBUG] uploadURL: %s", uploadURL)
	}

	if err := _tryToUploadArchive(uploadURL, archiveFilePath); err != nil {
		fmt.Println()
		log.Printf(" ===> (!) First upload attempt failed, retrying...")
		fmt.Println()
		time.Sleep(3000 * time.Millisecond)
		return _tryToUploadArchive(uploadURL, archiveFilePath)
	}
	return nil
}

func main() {
	stepParams, err := CreateStepParamsFromEnvs()
	if err != nil {
		log.Fatalf(" [!] Input error : %s", err)
	}
	gIsDebugMode = stepParams.IsDebugMode

	if gIsDebugMode {
		log.Printf("=> stepParams: %#v", stepParams)
	}

	log.Println(colorstring.Blue("=> Provided list of paths to cache:"))
	for _, aPathItm := range stepParams.PathItems {
		log.Printf(" * %s", aPathItm.Path)
	}
	stepParams.PathItems = cleanupCachePaths(stepParams.PathItems)
	fmt.Println()
	log.Println(colorstring.Green("=> (Filtered) Paths to cache:"))
	for _, aPathItm := range stepParams.PathItems {
		itmLogStr := " " + colorstring.Green("*") + " " + aPathItm.Path
		if aPathItm.IndicatorFilePath != "" {
			itmLogStr = itmLogStr + " " + colorstring.Green("->") + " " + aPathItm.IndicatorFilePath
		}
		log.Println(itmLogStr)
	}
	fmt.Println()

	if len(stepParams.IgnoreCheckOnPaths) > 0 {
		log.Println(colorstring.Yellow("=> Ignore change-check on paths:"))
		for _, aIgnorePth := range stepParams.IgnoreCheckOnPaths {
			log.Printf(" "+colorstring.Yellow("x")+" %s", aIgnorePth)
		}
	}

	if len(stepParams.PathItems) < 1 {
		log.Println("No paths specified to be cached, stopping.")
		os.Exit(3)
	}

	//
	// Load Previous Cache Info, if any
	//

	fmt.Println()
	previousCacheInfo := CacheInfosModel{}
	if stepParams.CompareCacheInfoPath != "" {
		if gIsDebugMode {
			log.Printf("=> Loading Previous Cache Info from: %s", stepParams.CompareCacheInfoPath)
		}
		cacheInfo, err := readCacheInfoFromFile(stepParams.CompareCacheInfoPath)
		if err != nil {
			log.Printf(" "+colorstring.Red("[!] Failed to read Cache Info for compare")+": %s", err)
		} else {
			previousCacheInfo = cacheInfo
		}
	} else {
		log.Println(colorstring.Blue("No base Cache Info found for compare"))
	}
	log.Println(colorstring.Blue("New cache will be created ..."))
	// normalize
	if previousCacheInfo.FingerprintsMeta == nil {
		previousCacheInfo.FingerprintsMeta = map[string]FingerprintMetaModel{}
	}

	//
	// Fingerprint
	//
	startTime := time.Now()

	fmt.Println()
	log.Println(colorstring.Blue("=> Calculating Fingerprint ..."))
	log.Printf("==> Fingerprint method: %s", stepParams.FingerprintMethodID)
	pthsFingerprint, fingerprintsMeta, err := fingerprintOfPaths(stepParams.PathItems, stepParams.IgnoreCheckOnPaths, stepParams.FingerprintMethodID)
	if err != nil {
		log.Fatalf(" [!] Failed to calculate fingerprint: %s", err)
	}
	if len(pthsFingerprint) < 1 {
		log.Fatal(" [!] Failed to calculate fingerprint: empty fingerprint generated")
	}
	fingerprintBase16Str := fmt.Sprintf("%x", pthsFingerprint)
	log.Printf("=> Calculated Fingerprint (base 16): %s", fingerprintBase16Str)
	log.Printf("=> Took: %s", time.Since(startTime))

	if gIsDebugMode {
		log.Printf("Comparing fingerprint with cache info from: %s", previousCacheInfo.Fingerprint)
	}
	if previousCacheInfo.Fingerprint == fingerprintBase16Str {
		log.Println(colorstring.Green(" => (i) Fingerprint matches the original one, no need to update Cache - DONE"))
		return
	}
	if previousCacheInfo.Fingerprint != "" {
		log.Printf(" (i) Fingerprint (%s) does not match the previous one (%s), Cache update required", fingerprintBase16Str, previousCacheInfo.Fingerprint)
	}

	// Print a diff, which files changed in the cache
	if previousCacheInfo.Fingerprint != "" || gIsDebugMode {
		compareFingerprintMetas(fingerprintsMeta, previousCacheInfo.FingerprintsMeta)
	}

	//
	// Archive
	//
	startTime = time.Now()

	fmt.Println()
	log.Println(colorstring.Blue("=> Creating Archive ..."))
	archiveFilePath, err := stepParams.createCacheArchiveFromPaths(stepParams.PathItems, fingerprintBase16Str, fingerprintsMeta)
	if err != nil {
		log.Fatalf(" [!] Failed to create Cache Archive: %s", err)
	}
	if gIsDebugMode {
		log.Printf(" => archiveFilePath: %s", archiveFilePath)
	}
	log.Println(colorstring.Green("=> Creating Archive - DONE"))
	log.Printf("=> Took: %s", time.Since(startTime))

	//
	// Upload
	//
	startTime = time.Now()
	fmt.Println()
	log.Println(colorstring.Blue("=> Uploading ..."))
	if err := uploadArchive(stepParams, archiveFilePath); err != nil {
		log.Fatalf(" [!] Failed to upload Cache Archive: %s", err)
	}
	log.Println(colorstring.Green("=> Upload - DONE"))
	log.Printf("=> Took: %s", time.Since(startTime))

	log.Println(colorstring.Green("=> FINISHED"))
}
