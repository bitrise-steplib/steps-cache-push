package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"testing"

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
		expectedFingerprint = "[./_samples/simple_text_file.txt]-[26B]-[0x644]-[@1458937589]"
		require.Equal(t, expectedFingerprint, fingerprint)
	}
}

func Test_isShouldIgnorePathFromFingerprint(t *testing.T) {
	{
		ignorePths := []string{
			"ignore/rel",
			"./ignore/exp-rel",
			"~/ignore/rel-to-home",
		}

		isShould := isShouldIgnorePathFromFingerprint("not/ignored", ignorePths)
		require.Equal(t, false, isShould)

		isShould = isShouldIgnorePathFromFingerprint("not/ignore/rel", ignorePths)
		require.Equal(t, false, isShould)

		// ignore

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
	}
}
