package main

import (
	"bufio"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/cmdex"
	"github.com/bitrise-io/go-utils/pathutil"
)

// StepParams ...
type StepParams struct {
	Paths []string
}

// CreateStepParamsFromEnvs ...
func CreateStepParamsFromEnvs() (StepParams, error) {
	stepParams := StepParams{}
	cacheDirs := os.Getenv("cache_paths")

	if cacheDirs == "" {
		return StepParams{}, errors.New("No cache_paths input specified")
	}

	scanner := bufio.NewScanner(strings.NewReader(cacheDirs))
	for scanner.Scan() {
		aCachePth := scanner.Text()
		if aCachePth != "" {
			stepParams.Paths = append(stepParams.Paths, aCachePth)
		}
	}
	if err := scanner.Err(); err != nil {
		return StepParams{}, fmt.Errorf("Failed to scan the the cache_paths input: %s", err)
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
					log.Printf(" (i) File (%s) is not a regular file (it's a symlink, a device, or something similar) - skipping", aPath)
					return nil
				}
				fileFingerprintSource := fingerprintSourceStringOfFile(aPath, aFileInfo)
				log.Printf(" * fileFingerprintSource (%s): %#v", aPath, fileFingerprintSource)
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
			log.Printf(" -> fileFingerprintSource (%s): %#v", aPath, fileFingerprintSource)
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

func createCacheArchiveFromPaths(pathsToCache []string) (string, error) {
	cacheArchiveTmpBaseDirPth, err := pathutil.NormalizedOSTempDirPath("")
	if err != nil {
		return "", fmt.Errorf("Failed to create temporary Cache Archive directory: %s", err)
	}
	fmt.Println()
	log.Printf("=> cacheArchiveTmpBaseDirPth: %s", cacheArchiveTmpBaseDirPth)
	fmt.Println()

	cacheContentDirName := "content"
	cacheContentDirPath := filepath.Join(cacheArchiveTmpBaseDirPth, cacheContentDirName)
	if err := pathutil.EnsureDirExist(cacheContentDirPath); err != nil {
		return "", fmt.Errorf("Failed to create Cache Content directory: %s", err)
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
		if fileInfo.IsDir() {
			archiveCopyRsyncParams = []string{"-avhP", aPath + "/", filepath.Join(cacheContentDirPath, fmt.Sprintf("c-%d", idx)) + "/"}
		} else {
			archiveCopyRsyncParams = []string{"-avhP", aPath, filepath.Join(cacheContentDirPath, fmt.Sprintf("c-%d", idx))}
		}

		log.Printf(" $ rsync %s", archiveCopyRsyncParams)
		if fullOut, err := cmdex.RunCommandAndReturnCombinedStdoutAndStderr("rsync", archiveCopyRsyncParams...); err != nil {
			log.Printf(" [!] Failed to sync archive target (%s), full output (stdout & stderr) was: %s", aPath, fullOut)
			return "", fmt.Errorf("Failed to sync archive target (%s): %s", aPath, err)
		}
	}

	cacheArchiveFileName := "cache.tar.gz"
	tarCmdParams := []string{"-cvzf", cacheArchiveFileName, cacheContentDirName}
	log.Printf(" $ tar %s", tarCmdParams)
	if fullOut, err := cmdex.RunCommandInDirAndReturnCombinedStdoutAndStderr(cacheArchiveTmpBaseDirPth, "tar", tarCmdParams...); err != nil {
		log.Printf(" [!] Failed to create cache archive, full output (stdout & stderr) was: %s", fullOut)
		return "", fmt.Errorf("Failed to create cache archive, error was: %s", err)
	}
	cacheArchiveFilePath := filepath.Join(cacheArchiveTmpBaseDirPth, cacheArchiveFileName)

	return cacheArchiveFilePath, nil
}

func compressArchiveDir(sourceArchiveDirPth, targetCompressedFilePth string) error {
	// tar -cvzf tarballname.tar.gz itemtocompress

	return nil
}

func main() {
	fmt.Println("Caching...")

	stepParams, err := CreateStepParamsFromEnvs()
	if err != nil {
		log.Fatalf(" [!] Input error : %s", err)
	}
	fmt.Printf("stepParams: %#v\n", stepParams)
	stepParams.Paths = cleanupCachePaths(stepParams.Paths)
	log.Printf("Filtered cache paths: %#v", stepParams.Paths)

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
	fmt.Printf("pthsFingerprint (base 16): %x\n", pthsFingerprint)
	// for _, aPath := range stepParams.Paths {
	// 	pthFingerprint, err := FingerprintOfPaths(aPath)
	// 	if err != nil {
	// 		log.Fatalf(" [!] Failed to calculate fingerprint of path (%s): %s", aPath, err)
	// 	}
	// 	fmt.Printf("pthFingerprint: %#v\n", pthFingerprint)
	// }

	// compare fingerprints

	// if changed:
	//  * rsync
	//  * compress (tar.gz)
	//  * upload
	archiveFilePath, err := createCacheArchiveFromPaths(stepParams.Paths)
	if err != nil {
		log.Fatalf(" [!] Failed to create Cache Archive: %s", err)
	}
	log.Printf(" => archiveFilePath: %s", archiveFilePath)
}
