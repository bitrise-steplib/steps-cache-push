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
	"github.com/bitrise-io/go-utils/pathutil"
)

const (
	indicatorFileSeparator = "->"
)

var (
	gIsDebugMode = false
)

// StepParamsPathItemModel ...
type StepParamsPathItemModel struct {
	Path              string
	IndicatorFilePath string
}

// StepParamsModel ...
type StepParamsModel struct {
	PathItems            []StepParamsPathItemModel
	CacheAPIURL          string
	CompareCacheInfoPath string
	IsDebugMode          bool
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
	cacheAPIURL := os.Getenv("cache_api_url")

	if cacheDirs == "" {
		return StepParamsModel{}, errors.New("No cache_paths input specified")
	}
	if cacheAPIURL == "" {
		return StepParamsModel{}, errors.New("No cache_api_url input specified")
	}

	stepParams := StepParamsModel{
		CacheAPIURL:          cacheAPIURL,
		CompareCacheInfoPath: os.Getenv("compare_cache_info_path"),
		IsDebugMode:          os.Getenv("is_debug_mode") == "true",
	}

	scanner := bufio.NewScanner(strings.NewReader(cacheDirs))
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

func fingerprintSourceStringOfFile(pth string, fileInfo os.FileInfo) (string, error) {
	fileShaChecksum, err := sha1ChecksumOfFile(pth)
	if err != nil {
		return "", fmt.Errorf("Failed to calculate checksum of file: %s", err)
	}
	return fmt.Sprintf("[%s]-[sha1:%x]-[%dB]-[0x%o]", pth, fileShaChecksum, fileInfo.Size(), fileInfo.Mode()), nil
}

// fingerprintOfPaths ...
func fingerprintOfPaths(pathItms []StepParamsPathItemModel) ([]byte, map[string]FingerprintMetaModel, error) {
	fingerprintMeta := map[string]FingerprintMetaModel{}
	fingerprintHash := sha1.New()
	for _, aPathItem := range pathItms {
		theFingerprintSourcePath := aPathItem.Path
		if aPathItem.IndicatorFilePath != "" {
			theFingerprintSourcePath = aPathItem.IndicatorFilePath

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

		absPth, err := pathutil.AbsPath(theFingerprintSourcePath)
		if err != nil {
			return []byte{}, fingerprintMeta, fmt.Errorf("Failed to get Absolute path of item (%s): %s", theFingerprintSourcePath, err)
		}

		fileInfo, isExist, err := pathutil.PathCheckAndInfos(absPth)
		if err != nil {
			return []byte{}, fingerprintMeta, fmt.Errorf("Failed to check the specified path: %s", err)
		}
		if !isExist {
			return []byte{}, fingerprintMeta, errors.New("Specified path does not exist")
		}

		if fileInfo.IsDir() {
			err := filepath.Walk(theFingerprintSourcePath, func(aPath string, aFileInfo os.FileInfo, walkErr error) error {
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
				fileFingerprintSource, err := fingerprintSourceStringOfFile(aPath, aFileInfo)
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
				return nil
			})
			if err != nil {
				return []byte{}, fingerprintMeta, fmt.Errorf("Failed to walk through the specified directory (%s): %s", theFingerprintSourcePath, err)
			}
		} else {
			fileFingerprintSource, err := fingerprintSourceStringOfFile(theFingerprintSourcePath, fileInfo)
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
		}
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

	for aPath, currValue := range currentMeta {
		prevValue, isFound := previousMeta[aPath]
		if !isFound {
			log.Printf("   [FILE ADDED] (No value found in the Previous Cache Meta for path): %s", aPath)
		} else {
			delete(prevItmsLookup, aPath)
			if currValue != prevValue {
				log.Printf(" (i) File changed: %s", aPath)
				log.Printf("     Previous fingerprint (source): %s", prevValue)
				log.Printf("     Current  fingerprint (source): %s", currValue)
			} else if gIsDebugMode {
				log.Printf(" (i) File fingerprint (source) match: %s", aPath)
			}
		}
	}

	for aPath := range prevItmsLookup {
		log.Printf("   [FILE REMOVED] (File is no longer in the cache, but it was in the previous one): %s", aPath)
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
			log.Println(" (!) Skipping: Failed to check the specified path: path was '/' - caching the whole root (/) directory is")
			continue
		}

		// check the Path
		{
			fileInfo, isExist, err := pathutil.PathCheckAndInfos(aPath)
			if err != nil {
				log.Printf(" (!) Skipping (%s): Failed to check the specified path: %s", aOrigPthItm.Path, err)
				continue
			}
			if !isExist {
				log.Printf(" (!) Skipping (%s): Specified path does not exist", aOrigPthItm.Path)
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
					log.Printf(" (!) Skipping (%s): Failed to check the specified path: %s", aOrigPthItm.IndicatorFilePath, err)
					continue
				}
				if !isExist {
					log.Printf(" (!) Skipping (%s): Specified path does not exist", aOrigPthItm.IndicatorFilePath)
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

func createCacheArchiveFromPaths(pathItemsToCache []StepParamsPathItemModel, archiveContentFingerprint string, fingerprintsMeta map[string]FingerprintMetaModel) (string, error) {
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
	tarCmdParams := []string{"-cvzf", cacheArchiveFilePath, "."}
	if gIsDebugMode {
		log.Printf(" $ tar %s", tarCmdParams)
	}
	if fullOut, err := cmdex.RunCommandInDirAndReturnCombinedStdoutAndStderr(cacheContentDirPath, "tar", tarCmdParams...); err != nil {
		log.Printf(" [!] Failed to create cache archive, full output (stdout & stderr) was: %s", fullOut)
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
	log.Printf("=> Upload response: %s", responseBytes)

	if resp.StatusCode != 200 {
		return fmt.Errorf("Failed to upload file, response code was: %d", resp.StatusCode)
	}
	log.Printf("=> Upload response code: %d", resp.StatusCode)

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
	log.Println("=> Uploading ...")

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

	log.Printf("=> Oritinal list of paths to cache: %v", stepParams.PathItems)
	stepParams.PathItems = cleanupCachePaths(stepParams.PathItems)
	log.Printf("=> Filtered paths to cache: %s", stepParams.PathItems)

	if len(stepParams.PathItems) < 1 {
		log.Println("No paths specified to be cached, stopping.")
		os.Exit(3)
	}

	//
	// Load Previous Cache Info, if any
	//

	previousCacheInfo := CacheInfosModel{}
	if stepParams.CompareCacheInfoPath != "" {
		if gIsDebugMode {
			log.Printf("=> Loading Previous Cache Info from: %s", stepParams.CompareCacheInfoPath)
		}
		cacheInfo, err := readCacheInfoFromFile(stepParams.CompareCacheInfoPath)
		if err != nil {
			log.Printf(" [!] Failed to read Cache Info for compare: %s", err)
		} else {
			previousCacheInfo = cacheInfo
		}
	} else {
		log.Println("No base Cache Info found for compare - New cache will be created")
	}
	// normalize
	if previousCacheInfo.FingerprintsMeta == nil {
		previousCacheInfo.FingerprintsMeta = map[string]FingerprintMetaModel{}
	}

	//
	// Fingerprint
	//

	pthsFingerprint, fingerprintsMeta, err := fingerprintOfPaths(stepParams.PathItems)
	if err != nil {
		log.Fatalf(" [!] Failed to calculate fingerprint: %s", err)
	}
	if len(pthsFingerprint) < 1 {
		log.Fatal(" [!] Failed to calculate fingerprint: empty fingerprint generated")
	}
	fingerprintBase16Str := fmt.Sprintf("%x", pthsFingerprint)
	log.Printf("=> Calculated Fingerprint (base 16): %s", fingerprintBase16Str)

	if gIsDebugMode {
		log.Printf("Comparing fingerprint with cache info from: %s", previousCacheInfo.Fingerprint)
	}
	if previousCacheInfo.Fingerprint == fingerprintBase16Str {
		log.Println(" => (i) Fingerprint matches the original one, no need to update Cache - DONE")
		return
	}
	log.Printf(" (i) Fingerprint (%s) does not match the previous one (%s), Cache update required", fingerprintBase16Str, previousCacheInfo.Fingerprint)

	// Print a diff, which files changed in the cache
	if previousCacheInfo.Fingerprint != "" || gIsDebugMode {
		compareFingerprintMetas(fingerprintsMeta, previousCacheInfo.FingerprintsMeta)
	}

	//
	// Archive
	//

	archiveFilePath, err := createCacheArchiveFromPaths(stepParams.PathItems, fingerprintBase16Str, fingerprintsMeta)
	if err != nil {
		log.Fatalf(" [!] Failed to create Cache Archive: %s", err)
	}
	if gIsDebugMode {
		log.Printf(" => archiveFilePath: %s", archiveFilePath)
	}

	//
	// Upload
	//
	if err := uploadArchive(stepParams, archiveFilePath); err != nil {
		log.Fatalf(" [!] Failed to upload Cache Archive: %s", err)
	}

	log.Println("=> DONE")
}
