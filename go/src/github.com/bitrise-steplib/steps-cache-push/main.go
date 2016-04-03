package main

import (
	"bufio"
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
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/cmdex"
	"github.com/bitrise-io/go-utils/pathutil"
)

var (
	gIsDebugMode = false
)

// StepParamsModel ...
type StepParamsModel struct {
	Paths                []string
	CacheUploadURL       string
	CompareCacheInfoPath string
	IsDebugMode          bool
}

// CacheContentModel ...
type CacheContentModel struct {
	DestinationPath       string `json:"destination_path"`
	RelativePathInArchive string `json:"relative_path_in_archive"`
}

// CacheInfosModel ...
type CacheInfosModel struct {
	Fingerprint string              `json:"fingerprint"`
	Contents    []CacheContentModel `json:"cache_contents"`
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

// CreateStepParamsFromEnvs ...
func CreateStepParamsFromEnvs() (StepParamsModel, error) {
	cacheDirs := os.Getenv("cache_paths")
	cacheUploadURL := os.Getenv("cache_upload_url")

	if cacheDirs == "" {
		return StepParamsModel{}, errors.New("No cache_paths input specified")
	}
	if cacheUploadURL == "" {
		return StepParamsModel{}, errors.New("No cache_upload_url input specified")
	}

	stepParams := StepParamsModel{
		CacheUploadURL:       cacheUploadURL,
		CompareCacheInfoPath: os.Getenv("compare_cache_info_path"),
		IsDebugMode:          os.Getenv("is_debug_mode") == "true",
	}

	scanner := bufio.NewScanner(strings.NewReader(cacheDirs))
	for scanner.Scan() {
		aCachePth := scanner.Text()
		if aCachePth != "" {
			stepParams.Paths = append(stepParams.Paths, aCachePth)
		}
	}
	if err := scanner.Err(); err != nil {
		return StepParamsModel{}, fmt.Errorf("Failed to scan the the cache_paths input: %s", err)
	}

	return stepParams, nil
}

func fingerprintSourceStringOfFile(pth string, fileInfo os.FileInfo) string {
	return fmt.Sprintf("[%s]-[%d]-[%dB]-[0x%o]", pth, fileInfo.ModTime().Unix(), fileInfo.Size(), fileInfo.Mode())
}

// fingerprintOfPaths ...
func fingerprintOfPaths(pths []string) ([]byte, error) {
	fingerprintHash := sha1.New()
	for _, aPath := range pths {
		if aPath == "" {
			continue
		}
		aPath = path.Clean(aPath)
		if aPath == "/" {
			return []byte{}, errors.New("Failed to check the specified path: caching the whole root (/) is forbidden (path was '/')")
		}
		fileInfo, isExist, err := pathutil.PathCheckAndInfos(aPath)
		if err != nil {
			return []byte{}, fmt.Errorf("Failed to check the specified path: %s", err)
		}
		if !isExist {
			return []byte{}, errors.New("Specified path does not exist")
		}

		if fileInfo.IsDir() {
			err := filepath.Walk(aPath, func(aPath string, aFileInfo os.FileInfo, walkErr error) error {
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
				fileFingerprintSource := fingerprintSourceStringOfFile(aPath, aFileInfo)
				if gIsDebugMode {
					log.Printf(" * fileFingerprintSource (%s): %#v", aPath, fileFingerprintSource)
				}
				if _, err := io.WriteString(fingerprintHash, fileFingerprintSource); err != nil {
					return fmt.Errorf("Failed to write fingerprint source string (%s) to fingerprint hash: %s",
						fileFingerprintSource, err)
				}
				return nil
			})
			if err != nil {
				return []byte{}, fmt.Errorf("Failed to walk through the specified directory (%s): %s", aPath, err)
			}
		} else {
			fileFingerprintSource := fingerprintSourceStringOfFile(aPath, fileInfo)
			if gIsDebugMode {
				log.Printf(" -> fileFingerprintSource (%s): %#v", aPath, fileFingerprintSource)
			}
			if _, err := io.WriteString(fingerprintHash, fileFingerprintSource); err != nil {
				return []byte{}, fmt.Errorf("Failed to write fingerprint source string (%s) to fingerprint hash: %s",
					fileFingerprintSource, err)
			}
		}
	}

	return fingerprintHash.Sum(nil), nil
}

func cleanupCachePaths(requestedCachePaths []string) []string {
	filteredPaths := []string{}
	for _, aOrigPth := range requestedCachePaths {
		if aOrigPth == "" {
			continue
		}
		aPath := path.Clean(aOrigPth)
		if aPath == "/" {
			log.Println(" (!) Skipping: Failed to check the specified path: path was '/' - caching the whole root (/) directory is")
			continue
		}
		fileInfo, isExist, err := pathutil.PathCheckAndInfos(aPath)
		if err != nil {
			log.Printf(" (!) Skipping (%s): Failed to check the specified path: %s", aOrigPth, err)
			continue
		}
		if !isExist {
			log.Printf(" (!) Skipping (%s): Specified path does not exist", aOrigPth)
			continue
		}
		if !fileInfo.IsDir() {
			if !fileInfo.Mode().IsRegular() {
				log.Printf(" (i) File (%s) is not a regular file (it's a symlink, a device, or something similar) - skipping", aOrigPth)
				continue
			}
		}

		filteredPaths = append(filteredPaths, aPath)
	}
	return filteredPaths
}

func createCacheArchiveFromPaths(pathsToCache []string, archiveContentFingerprint string) (string, error) {
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
		Fingerprint: archiveContentFingerprint,
		Contents:    []CacheContentModel{},
	}
	for idx, aPath := range pathsToCache {
		aPath = path.Clean(aPath)
		fileInfo, isExist, err := pathutil.PathCheckAndInfos(aPath)
		if err != nil {
			return "", fmt.Errorf("Failed to check path (%s): %s", aPath, err)
		}
		if !isExist {
			return "", fmt.Errorf("Path does not exist: %s", aPath)
		}

		archiveCopyRsyncParams := []string{}
		itemRelPathInArchive := fmt.Sprintf("c-%d", idx)

		if fileInfo.IsDir() {
			archiveCopyRsyncParams = []string{"-avhP", aPath + "/", filepath.Join(cacheContentDirPath, itemRelPathInArchive+"/")}
		} else {
			archiveCopyRsyncParams = []string{"-avhP", aPath, filepath.Join(cacheContentDirPath, itemRelPathInArchive)}
		}

		cacheInfo.Contents = append(cacheInfo.Contents, CacheContentModel{
			DestinationPath:       aPath,
			RelativePathInArchive: itemRelPathInArchive,
		})

		if gIsDebugMode {
			log.Printf(" $ rsync %s", archiveCopyRsyncParams)
		}
		if fullOut, err := cmdex.RunCommandAndReturnCombinedStdoutAndStderr("rsync", archiveCopyRsyncParams...); err != nil {
			log.Printf(" [!] Failed to sync archive target (%s), full output (stdout & stderr) was: %s", aPath, fullOut)
			return "", fmt.Errorf("Failed to sync archive target (%s): %s", aPath, err)
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

func _tryToUploadArchive(stepParams StepParamsModel, archiveFilePath string) error {
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

	req, err := http.NewRequest("PUT", stepParams.CacheUploadURL, archFile)
	if err != nil {
		return fmt.Errorf("Failed to create upload request: %s", err)
	}

	// req.Header.Set("Content-Type", "application/octet-stream")
	// req.Header.Add("Content-Length", strconv.FormatInt(fileSize, 10))
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

func uploadArchive(stepParams StepParamsModel, archiveFilePath string) error {
	log.Println("=> Uploading ...")
	{
		fi, err := os.Stat(archiveFilePath)
		if err != nil {
			log.Printf(" [!] Failed to get File Infos of Archive (%s): %s", archiveFilePath, err)
		} else {
			sizeInBytes := fi.Size()
			log.Printf("   Archive file size: %d bytes / %f MB", sizeInBytes, (float64(sizeInBytes) / 1024.0 / 1024.0))
		}
	}
	if err := _tryToUploadArchive(stepParams, archiveFilePath); err != nil {
		fmt.Println()
		log.Printf(" ===> (!) First upload attempt failed, retrying...")
		fmt.Println()
		time.Sleep(3000 * time.Millisecond)
		return _tryToUploadArchive(stepParams, archiveFilePath)
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

	log.Printf("=> Oritinal list of paths to cache: %s", stepParams.Paths)
	stepParams.Paths = cleanupCachePaths(stepParams.Paths)
	log.Printf("=> Filtered paths to cache: %s", stepParams.Paths)

	if len(stepParams.Paths) < 1 {
		log.Println("No paths specified to be cached, stopping.")
		os.Exit(3)
	}

	// fingerprint
	pthsFingerprint, err := fingerprintOfPaths(stepParams.Paths)
	if err != nil {
		log.Fatalf(" [!] Failed to calculate fingerprint: %s", err)
	}
	if len(pthsFingerprint) < 1 {
		log.Fatal(" [!] Failed to calculate fingerprint: empty fingerprint generated")
	}
	fingerprintBase16Str := fmt.Sprintf("%x", pthsFingerprint)
	log.Printf("=> Calculated Fingerprint (base 16): %s", fingerprintBase16Str)

	// compare fingerprints
	if stepParams.CompareCacheInfoPath != "" {
		if gIsDebugMode {
			log.Printf("Comparing fingerprint with cache info from: %s", stepParams.CompareCacheInfoPath)
		}
		cacheInfo, err := readCacheInfoFromFile(stepParams.CompareCacheInfoPath)
		if err != nil {
			log.Printf(" [!] Failed to read Cache Info for compare: %s", err)
		} else {
			if cacheInfo.Fingerprint == fingerprintBase16Str {
				log.Println(" => (i) Fingerprint matches the original one, no need to update Cache - DONE")
				return
			}
			log.Printf(" (i) Fingerprint (%s) does not match the original one (%s), Cache update required", fingerprintBase16Str, cacheInfo.Fingerprint)
		}
	} else {
		log.Println("No base Cache Info found for compare - New cache will be created")
	}

	archiveFilePath, err := createCacheArchiveFromPaths(stepParams.Paths, fingerprintBase16Str)
	if err != nil {
		log.Fatalf(" [!] Failed to create Cache Archive: %s", err)
	}
	log.Printf(" => archiveFilePath: %s", archiveFilePath)

	if err := uploadArchive(stepParams, archiveFilePath); err != nil {
		log.Fatalf(" [!] Failed to upload Cache Archive: %s", err)
	}

	log.Println(" => DONE")
}
