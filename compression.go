package main

import (
	// "archive/tar"
	// "bytes"
	// "compress/gzip"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bitrise-io/go-utils/log"
	"github.com/pierrec/lz4"
)


const (
	maxConcurrency	  = -1
)

type CompressionWriter struct {
	writer		io.Writer
	closer		io.Closer
}


func FastArchiveCompress(cacheArchivePath, compressor string) (int64, error) {
	var compressedArchiveSize int64
	compressStartTime := time.Now()

	in, err := os.Open(cacheArchivePath)
	if err != nil {
		return 0, fmt.Errorf("Fatal error in opening file: ", err.Error())
	}
	defer in.Close()

	compressionWriter, outputFile, err := getFastArchiveCompressionWriter(cacheArchivePath, compressor)
	if err != nil {
		return 0, fmt.Errorf("Error getting compressor writer: ", err.Error())
	}

	_, err = io.Copy(compressionWriter.writer, in)
	if err != nil {
		return 0, fmt.Errorf("Error compressing file:", err.Error())
	}

	defer compressionWriter.closer.Close()

	fileInfo, err := outputFile.Stat()
	if err == nil {
		compressedArchiveSize = fileInfo.Size()
	}

	err = os.Remove(cacheArchivePath)
	if err != nil {
		return 0, fmt.Errorf("Error deleting uncompressed archive file: ", err.Error())
	}

	log.Infof("Done compressing file using %s in %s", compressor, time.Since(compressStartTime))

	return compressedArchiveSize, nil
}

func getFastArchiveCompressionWriter(cacheArchivePath, compressor string) (*CompressionWriter, *os.File, error) {
	if compressor == "lz4" {
		compressedFilePath := cacheArchivePath + lz4.Extension
		compressedOutputFile, err := os.Create(compressedFilePath)
		if err != nil {
			return nil, compressedOutputFile, fmt.Errorf("Error creating compression output file:", err.Error())
		}

		log.Infof("Compressing file into: ", compressedFilePath)

		lz4Writer := lz4.NewWriter(compressedOutputFile)
		lz4Writer.Header = lz4.Header{
			BlockChecksum		: true,
			BlockMaxSize		: 256 << 10,
			CompressionLevel	: 5,
		}
		lz4Writer.WithConcurrency(maxConcurrency)

		return &CompressionWriter{
			writer: lz4Writer,
			closer: lz4Writer,
		}, compressedOutputFile, nil
	} 
	
	log.Errorf("Unsupported compressor algorithm in fast-archiver for: ", compressor)
	os.Exit(1)

	return nil, nil, nil
}