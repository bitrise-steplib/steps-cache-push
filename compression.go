package main

import (
	"compress/gzip"
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

	compressionWriter, outputFile, err := NewCompressionWriter(cacheArchivePath, compressor)
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

func NewCompressionWriter(cacheArchivePath, compressor string) (*CompressionWriter, *os.File, error) {
	if compressor == "lz4" {
		compressedOutputFile := createOutputFile(cacheArchivePath, lz4.Extension)
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
	} else if compressor == "gzip" {
		compressedOutputFile := createOutputFile(cacheArchivePath, "gz")
		gzipWriter, err := gzip.NewWriterLevel(compressedOutputFile, gzip.BestCompression)
		if err != nil {
			return nil, compressedOutputFile, err
		}

		return  &CompressionWriter{
			writer: gzipWriter,
			closer: gzipWriter,
		}, compressedOutputFile, nil
	}
	
	log.Errorf("Unsupported compressor algorithm in fast-archiver for: ", compressor)
	os.Exit(1)

	return nil, nil, nil
}

func createOutputFile(cacheArchivePath, extension string) (*os.File) {
	compressedFilePath := cacheArchivePath + extension
	compressedOutputFile, err := os.Create(compressedFilePath)

	log.Infof("Compressing file into: ", compressedFilePath)

	if err != nil {
		log.Errorf("Error when creating new compressed file", err.Error())
		os.Exit(1)

		return nil
	}

	return compressedOutputFile
}