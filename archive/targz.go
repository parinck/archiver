package archive

import (
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// TarGz facilitates gzip compression
// (RFC 1952) of tarball archives.
type TarGz struct {
	*Tar

	// The compression level to use, as described
	// in the compress/gzip package.
	CompressionLevel int
}

// Archive creates a compressed tar file at destination
// containing the files listed in sources. The destination
// must end with ".tar.gz" or ".tgz". File paths can be
// those of regular files or directories; directories will
// be recursively added.
func (tgz *TarGz) Archive(sources []string, destination string) error {
	if !strings.HasSuffix(destination, ".tar.gz") &&
		!strings.HasSuffix(destination, ".tgz") {
		return fmt.Errorf("output filename must have .tar.gz or .tgz extension")
	}
	tgz.wrapWriter()
	return tgz.Tar.Archive(sources, destination)
}

// Unarchive unpacks the compressed tarball at
// source to destination. Destination will be
// treated as a folder name.
func (tgz *TarGz) Unarchive(source, destination string) error {
	tgz.wrapReader()
	return tgz.Tar.Unarchive(source, destination)
}

// Walk calls walkFn for each visited item in archive.
func (tgz *TarGz) Walk(archive string, walkFn WalkFunc) error {
	tgz.wrapReader()
	return tgz.Tar.Walk(archive, walkFn)
}

// Create opens txz for writing a compressed
// tar archive to out.
func (tgz *TarGz) Create(out io.Writer) error {
	tgz.wrapWriter()
	return tgz.Create(out)
}

// Open opens t for reading a compressed archive from
// in. The size parameter is not used.
func (tgz *TarGz) Open(in io.Reader, size int64) error {
	tgz.wrapReader()
	return tgz.Tar.Open(in, size)
}

// Extract extracts a single file from the tar archive.
// If the target is a directory, the entire folder will
// be extracted into destination.
func (tgz *TarGz) Extract(source, target, destination string) error {
	tgz.wrapReader()
	return tgz.Tar.Extract(source, target, destination)
}

func (tgz *TarGz) wrapWriter() {
	var gzw *gzip.Writer
	tgz.Tar.writerWrapFn = func(w io.Writer) (io.Writer, error) {
		var err error
		gzw, err = gzip.NewWriterLevel(w, tgz.CompressionLevel)
		return gzw, err
	}
	tgz.Tar.cleanupWrapFn = func() {
		gzw.Close()
	}
}

func (tgz *TarGz) wrapReader() {
	var gzr *gzip.Reader
	tgz.Tar.readerWrapFn = func(r io.Reader) (io.Reader, error) {
		var err error
		gzr, err = gzip.NewReader(r)
		return gzr, err
	}
	tgz.Tar.cleanupWrapFn = func() {
		gzr.Close()
	}
}

// Compile-time checks to ensure type implements desired interfaces.
var (
	_ = Reader(new(TarGz))
	_ = Writer(new(TarGz))
	_ = Archiver(new(TarGz))
	_ = Unarchiver(new(TarGz))
	_ = Walker(new(TarGz))
	_ = Extractor(new(TarGz))
)

// DefaultTarGz is a convenient archiver ready to use.
var DefaultTarGz = &TarGz{
	CompressionLevel: gzip.DefaultCompression,
	Tar:              DefaultTar,
}