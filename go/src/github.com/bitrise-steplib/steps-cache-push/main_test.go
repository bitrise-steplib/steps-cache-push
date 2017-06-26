package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	glob "github.com/ryanuber/go-glob"
	"github.com/stretchr/testify/require"
)

func Test_sandbox(t *testing.T) {
	{
		res := strings.TrimSuffix("a//", "/")
		require.Equal(t, "a/", res)
	}

	{
		res := path.Clean("a//")
		require.Equal(t, "a", res)
	}
}

func Test_filterPaths(t *testing.T) {
	paths := []StepParamsPathItemModel{
		StepParamsPathItemModel{Path: "/test/path/sub1/sub3"},
		StepParamsPathItemModel{Path: "/test/path"},
		StepParamsPathItemModel{Path: "/test/path/sub1"},
		StepParamsPathItemModel{Path: "/test/path/sub1/sub2"},
	}

	filteredPaths, err := flattenPathItems(paths)
	require.NoError(t, err)

	expectedPaths := []StepParamsPathItemModel{
		StepParamsPathItemModel{Path: "/test/path"},
	}

	require.Equal(t, expectedPaths, filteredPaths)
}

func Test_filterPaths_random(t *testing.T) {
	paths := []StepParamsPathItemModel{
		StepParamsPathItemModel{Path: "/test/path"},
		StepParamsPathItemModel{Path: "/test/path/sub1"},
		StepParamsPathItemModel{Path: "/test/another/sub1"},
		StepParamsPathItemModel{Path: "/test/another/sub1/sub2"},
		StepParamsPathItemModel{Path: "/test/another/sub2"},
		StepParamsPathItemModel{Path: "/test/path/sub1/sub2"},
	}

	filteredPaths, err := flattenPathItems(paths)
	require.NoError(t, err)

	expectedPaths := []StepParamsPathItemModel{
		StepParamsPathItemModel{Path: "/test/path"},
		StepParamsPathItemModel{Path: "/test/another/sub1"},
		StepParamsPathItemModel{Path: "/test/another/sub2"},
	}

	require.Equal(t, expectedPaths, filteredPaths)
}

func Test_parseStepParamsPathItemModelFromString(t *testing.T) {
	// simple
	{
		res, err := parseStepParamsPathItemModelFromString("./a/path")
		require.NoError(t, err)
		require.Equal(t, "./a/path", res.Path)
		require.Equal(t, "", res.IndicatorFilePath)
	}

	// indicator
	{
		res, err := parseStepParamsPathItemModelFromString("./a/path -> an/indicator")
		require.NoError(t, err)
		require.Equal(t, "./a/path", res.Path)
		require.Equal(t, "an/indicator", res.IndicatorFilePath)
	}
	{
		res, err := parseStepParamsPathItemModelFromString("no/space->around/indicator")
		require.NoError(t, err)
		require.Equal(t, "no/space", res.Path)
		require.Equal(t, "around/indicator", res.IndicatorFilePath)
	}

	// whitespaces
	{
		res, err := parseStepParamsPathItemModelFromString("     ./a/path   ")
		require.NoError(t, err)
		require.Equal(t, "./a/path", res.Path)
		require.Equal(t, "", res.IndicatorFilePath)
	}
	{
		res, err := parseStepParamsPathItemModelFromString("     ./a/path  ->   an/indicator ")
		require.NoError(t, err)
		require.Equal(t, "./a/path", res.Path)
		require.Equal(t, "an/indicator", res.IndicatorFilePath)
	}

	// invalid
	{
		_, err := parseStepParamsPathItemModelFromString("multiple -> indicator -> in/item")
		require.EqualError(t, err, "The indicator file separator (->) is specified more than once: multiple -> indicator -> in/item")
	}
	{
		_, err := parseStepParamsPathItemModelFromString(" -> only/indicator")
		require.EqualError(t, err, "No path specified in item:  -> only/indicator")
	}
}

func Test_sha1ChecksumOfFile(t *testing.T) {
	{
		checksumBytes, err := sha1ChecksumOfFile("./_samples/simple_text_file.txt")
		require.NoError(t, err)
		require.Equal(t, "002de9a34df93e596a387b440fd83023452e6ec5", fmt.Sprintf("%x", checksumBytes))
	}
}

func Test_fingerprintSourceStringOfFile(t *testing.T) {
	{
		sampleFilePth := "./_samples/simple_text_file.txt"
		fileInfo, err := os.Stat(sampleFilePth)
		require.NoError(t, err)

		// file content hash method
		fingerprint, err := fingerprintSourceStringOfFile(sampleFilePth, fileInfo, fingerprintMethodIDContentChecksum)
		require.NoError(t, err)
		expectedFingerprint := "[./_samples/simple_text_file.txt]-[26B]-[0x644]-[sha1:002de9a34df93e596a387b440fd83023452e6ec5]"
		require.Equal(t, expectedFingerprint, fingerprint)

		// file mod method
		fingerprint, err = fingerprintSourceStringOfFile(sampleFilePth, fileInfo, fingerprintMethodIDFileModTime)
		require.NoError(t, err)
		expectedFingerprint = "[./_samples/simple_text_file.txt]-[26B]-[0x644]-[@1498475012]"
		require.Equal(t, expectedFingerprint, fingerprint)
	}
}

func Test_createCacheArchiveFromPaths(t *testing.T) {
	pathItems := []StepParamsPathItemModel{
		StepParamsPathItemModel{Path: "./_sample_artifacts/filestructure"},
	}
	ignoreCheckOnPaths := []string{
		"*.ap_",
		"!*.apk",
	}
	stepParams := &StepParamsModel{
		PathItems:          pathItems,
		IgnoreCheckOnPaths: ignoreCheckOnPaths,
	}

	pthsFingerprint, fingerprintsMeta, err := fingerprintOfPaths(stepParams.PathItems, stepParams.IgnoreCheckOnPaths, "file-content-hash")
	require.NoError(t, err)

	fingerprintBase16Str := fmt.Sprintf("%x", pthsFingerprint)

	archiveFilePath, err := stepParams.createCacheArchiveFromPaths(stepParams.PathItems, fingerprintBase16Str, fingerprintsMeta)
	require.NoError(t, err)

	errorMsg := ""
	err = filepath.Walk(filepath.Join(filepath.Dir(archiveFilePath), "content"), func(aPath string, aFileInfo os.FileInfo, walkErr error) error {
		for _, ignorePattern := range stepParams.IgnoreCheckOnPaths {
			if glob.Glob(ignorePattern, aPath) && strings.HasSuffix(aPath, ".apk") {
				errorMsg += fmt.Sprintf("\n(pattern: %s) (path: %s)", ignorePattern, aPath)
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "", errorMsg, errorMsg)
}

func Test_isShouldIgnorePathFromFingerprint(t *testing.T) {
	{
		ignorePths := []string{
			"ignore/rel",
			"./ignore/exp-rel",
			"~/ignore/rel-to-home",
			"ignore/glob/*.ext",
			"ignore/glob/a/*/b/*",
		}

		// Prefix - don't ignore

		isShould := isShouldIgnorePathFromFingerprint("not/ignored", ignorePths)
		require.Equal(t, false, isShould)

		isShould = isShouldIgnorePathFromFingerprint("not/ignore/rel", ignorePths)
		require.Equal(t, false, isShould)

		// Prefix - ignore

		isShould = isShouldIgnorePathFromFingerprint("ignore/rel", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/rel/under", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("./ignore/exp-rel/under", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("~/ignore/rel-to-home", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("~/ignore/rel-to-home/", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("~/ignore/rel-to-home/under", ignorePths)
		require.Equal(t, true, isShould)

		// Glob - don't ignore
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a.2ext", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/b.2ext", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a.ext2", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/b.ext2", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/b.ext/2", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a.ext/2", ignorePths)
		require.Equal(t, false, isShould)

		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/x/b", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/b/x/a/x", ignorePths)
		require.Equal(t, false, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/b/x/y/a/x", ignorePths)
		require.Equal(t, false, isShould)

		// Glob - ignore
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a.ext", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/b.ext", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/x/b/x", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/x/y/b/x", ignorePths)
		require.Equal(t, true, isShould)
		isShould = isShouldIgnorePathFromFingerprint("ignore/glob/a/x/y/b/x/y", ignorePths)
		require.Equal(t, true, isShould)
	}
}
