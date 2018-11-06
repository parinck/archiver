package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Tar provides facilities for operating TAR archives.
// See http://www.gnu.org/software/tar/manual/html_node/Standard.html.
type Tar struct {
	// Whether to overwrite existing files; if false,
	// an error is returned if the file exists.
	OverwriteExisting bool

	// Whether to make all the directories necessary
	// to create a tar archive in the desired path.
	MkdirAll bool

	// A single top-level folder can be implicitly
	// created by the Archive or Unarchive methods
	// if the files to be added to the archive
	// or the files to be extracted from the archive
	// do not all have a common root. This roughly
	// mimics the behavior of archival tools integrated
	// into OS file browsers which create a subfolder
	// to avoid unexpectedly littering the destination
	// folder with potentially many files, causing a
	// problematic cleanup/organization situation.
	// This feature is available for both creation
	// and extraction of archives, but may be slightly
	// inefficient with lots and lots of files,
	// especially on extraction.
	ImplicitTopLevelFolder bool

	// If true, errors encountered during reading
	// or writing a single file will be logged and
	// the operation will continue on remaining files.
	ContinueOnError bool

	tw *tar.Writer
	tr *tar.Reader
}

// Archive creates a .tar file at destination containing
// the files listed in sources. The destination must end
// with ".tar". File paths can be those of regular files
// or directories. Regular files are stored at the 'root'
// of the archive, and directories are recursively added.
func (t *Tar) Archive(sources []string, destination string) error {
	if !strings.HasSuffix(destination, ".tar") {
		return fmt.Errorf("output filename must have .tar extension")
	}
	if !t.OverwriteExisting && fileExists(destination) {
		return fmt.Errorf("file already exists: %s", destination)
	}

	out, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("creating %s: %v", destination, err)
	}
	defer out.Close()

	err = t.Create(out)
	if err != nil {
		return fmt.Errorf("creating tar: %v", err)
	}
	defer t.Close()

	var topLevelFolder string
	if t.ImplicitTopLevelFolder && multipleTopLevels(sources) {
		topLevelFolder = folderNameFromFileName(destination)
	}

	for _, source := range sources {
		err := t.writeWalk(source, topLevelFolder)
		if err != nil {
			return fmt.Errorf("walking %s: %v", source, err)
		}
	}

	return nil
}

// Unarchive unpacks the .tar file at source to destination.
// Destination will be treated as a folder name.
func (t *Tar) Unarchive(source, destination string) error {
	if !fileExists(destination) && t.MkdirAll {
		err := mkdir(destination)
		if err != nil {
			return fmt.Errorf("preparing destination: %v", err)
		}
	}

	// if the files in the archive do not all share a common
	// root, then make sure we extract to a single subfolder
	// rather than potentially littering the destination...
	if t.ImplicitTopLevelFolder {
		var err error
		destination, err = t.addTopLevelFolder(source, destination)
		if err != nil {
			return fmt.Errorf("scanning source archive: %v", err)
		}
	}

	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("opening source archive: %v", err)
	}
	defer file.Close()

	err = t.Open(file, 0)
	if err != nil {
		return fmt.Errorf("opening tar archive for reading: %v", err)
	}
	defer t.Close()

	for {
		err := t.untarNext(destination)
		if err == io.EOF {
			break
		}
		if err != nil {
			if t.ContinueOnError {
				log.Printf("[ERROR] Reading file in tar archive: %v", err)
				continue
			}
			return fmt.Errorf("reading file in tar archive: %v", err)
		}
	}

	return nil
}

// addTopLevelFolder scans the files contained inside
// the tarball named sourceArchive and returns a modified
// destination if all the files do not share the same
// top-level folder.
func (t *Tar) addTopLevelFolder(sourceArchive, destination string) (string, error) {
	file, err := os.Open(sourceArchive)
	if err != nil {
		return "", fmt.Errorf("opening source archive: %v", err)
	}
	defer file.Close()

	tr := tar.NewReader(file)

	var files []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("scanning tarball's file listing: %v", err)
		}
		files = append(files, hdr.Name)
	}

	if multipleTopLevels(files) {
		destination = filepath.Join(destination, folderNameFromFileName(sourceArchive))
	}

	return destination, nil
}

func (t *Tar) untarNext(to string) error {
	f, err := t.Read()
	if err != nil {
		return err // don't wrap error; calling loop must break on io.EOF
	}
	header, ok := f.Header.(*tar.Header)
	if !ok {
		return fmt.Errorf("expected header to be *tar.Header but was %T", f.Header)
	}
	return t.untarFile(f, filepath.Join(to, header.Name))
}

func (t *Tar) untarFile(f File, to string) error {
	// do not overwrite existing files, if configured
	if !f.IsDir() && !t.OverwriteExisting && fileExists(to) {
		return fmt.Errorf("file already exists: %s", to)
	}

	hdr, ok := f.Header.(*tar.Header)
	if !ok {
		return fmt.Errorf("expected header to be *tar.Header but was %T", f.Header)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return mkdir(to)
	case tar.TypeReg, tar.TypeRegA, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		return writeNewFile(to, f, f.Mode())
	case tar.TypeSymlink:
		return writeNewSymbolicLink(to, hdr.Linkname)
	case tar.TypeLink:
		return writeNewHardLink(to, filepath.Join(to, hdr.Linkname))
	case tar.TypeXGlobalHeader:
		return nil // ignore the pax global header from git-generated tarballs
	default:
		return fmt.Errorf("%s: unknown type flag: %c", hdr.Name, hdr.Typeflag)
	}
}

func (t *Tar) writeWalk(source, topLevelFolder string) error {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("getting absolute path: %v", err)
	}
	sourceInfo, err := os.Stat(sourceAbs)
	if err != nil {
		return fmt.Errorf("%s: stat: %v", source, err)
	}

	var baseDir string
	if topLevelFolder != "" {
		baseDir = topLevelFolder
	}
	if sourceInfo.IsDir() {
		baseDir = path.Join(baseDir, sourceInfo.Name())
	}

	return filepath.Walk(source, func(fpath string, info os.FileInfo, err error) error {
		handleErr := func(err error) error {
			if t.ContinueOnError {
				log.Printf("[ERROR] Walking %s: %v", fpath, err)
				return nil
			}
			return err
		}
		if err != nil {
			return handleErr(fmt.Errorf("traversing %s: %v", fpath, err))
		}
		if info == nil {
			return handleErr(fmt.Errorf("no file info"))
		}

		name := source
		if source != fpath {
			name, err = filepath.Rel(source, fpath)
			if err != nil {
				return handleErr(err)
			}
		}

		nameInArchive := path.Join(baseDir, filepath.ToSlash(name))

		file, err := os.Open(fpath)
		if err != nil {
			return handleErr(fmt.Errorf("%s: opening: %v", fpath, err))
		}
		defer file.Close()

		err = t.Write(File{
			FileInfo: FileInfo{
				FileInfo:   info,
				CustomName: nameInArchive,
			},
			ReadCloser: file,
		})
		if err != nil {
			return handleErr(fmt.Errorf("%s: writing: %s", fpath, err))
		}

		return nil
	})
}

// Create opens t for writing a tar archive to out.
func (t *Tar) Create(out io.Writer) error {
	if t.tw != nil {
		return fmt.Errorf("tar archive is already created for writing")
	}
	t.tw = tar.NewWriter(out)
	return nil
}

// Write writes f to t, which must have been opened for writing first.
func (t *Tar) Write(f File) error {
	if t.tw == nil {
		return fmt.Errorf("tar archive was not created for writing first")
	}
	if f.FileInfo == nil {
		return fmt.Errorf("no file info")
	}
	if f.FileInfo.Name() == "" {
		return fmt.Errorf("missing file name")
	}
	if f.ReadCloser == nil {
		return fmt.Errorf("%s: no way to read file contents", f.Name())
	}

	hdr, err := tar.FileInfoHeader(f, f.Name())
	if err != nil {
		return fmt.Errorf("%s: making header: %v", f.Name(), err)
	}

	err = t.tw.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("%s: writing header: %v", hdr.Name, err)
	}

	if f.IsDir() {
		return nil
	}

	if hdr.Typeflag == tar.TypeReg {
		_, err := io.Copy(t.tw, f)
		if err != nil {
			return fmt.Errorf("%s: copying contents: %v", f.Name(), err)
		}
	}

	return nil
}

// Open opens t for reading an archive from in.
// The size parameter is not needed.
func (t *Tar) Open(in io.Reader, size int64) error {
	if t.tr != nil {
		return fmt.Errorf("tar archive is already open for reading")
	}
	t.tr = tar.NewReader(in)
	return nil
}

// Read reads the next file from t, which must have
// already been opened for reading. If there are no
// more files, the error is io.EOF. The File must
// be closed when finished reading from it.
func (t *Tar) Read() (File, error) {
	if t.tr == nil {
		return File{}, fmt.Errorf("tar archive is not open")
	}

	hdr, err := t.tr.Next()
	if err != nil {
		return File{}, err // don't wrap error; preserve io.EOF
	}

	file := File{
		FileInfo:   hdr.FileInfo(),
		Header:     hdr,
		ReadCloser: ReadFakeCloser{t.tr},
	}

	return file, nil
}

// Close closes the tar archive(s) opened by Create and Open.
func (t *Tar) Close() error {
	if t.tr != nil {
		t.tr = nil
	}
	if t.tw != nil {
		tw := t.tw
		t.tw = nil
		return tw.Close()
	}
	return nil
}

// Walk calls walkFn for each visited item in archive.
func (t *Tar) Walk(archive string, walkFn WalkFunc) error {
	file, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("opening archive file: %v", err)
	}
	defer file.Close()

	tr := tar.NewReader(file)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if t.ContinueOnError {
				log.Printf("[ERROR] Opening next file: %v", err)
				continue
			}
			return fmt.Errorf("opening next file: %v", err)
		}
		err = walkFn(File{
			FileInfo:   hdr.FileInfo(),
			Header:     hdr,
			ReadCloser: ReadFakeCloser{tr},
		})
		if err != nil {
			if err == ErrStopWalk {
				break
			}
			if t.ContinueOnError {
				log.Printf("[ERROR] Walking %s: %v", hdr.Name, err)
				continue
			}
			return fmt.Errorf("walking %s: %v", hdr.Name, err)
		}
	}

	return nil
}

// Extract extracts a single file from the tar archive.
// If the target is a directory, the entire folder will
// be extracted into destination.
func (t *Tar) Extract(source, target, destination string) error {
	// target refers to a path inside the archive, which should be clean also
	target = path.Clean(target)

	// if the target ends up being a directory, then
	// we will continue walking and extracting files
	// until we are no longer within that directory
	var targetDirPath string

	return t.Walk(source, func(f File) error {
		th, ok := f.Header.(*tar.Header)
		if !ok {
			return fmt.Errorf("expected header to be *tar.Header but was %T", f.Header)
		}

		// importantly, cleaning the path strips tailing slash,
		// which must be appended to folders within the archive
		name := path.Clean(th.Name)
		if f.IsDir() && target == name {
			targetDirPath = path.Dir(name)
		}

		if within(target, th.Name) {
			// either this is the exact file we want, or is
			// in the directory we want to extract

			// build the filename we will extract to
			end, err := filepath.Rel(targetDirPath, th.Name)
			if err != nil {
				return fmt.Errorf("relativizing paths: %v", err)
			}
			joined := filepath.Join(destination, end)

			err = t.untarFile(f, joined)
			if err != nil {
				return fmt.Errorf("extracting file %s: %v", th.Name, err)
			}

			// if our target was not a directory, stop walk
			if targetDirPath == "" {
				return ErrStopWalk
			}
		} else if targetDirPath != "" {
			// finished walking the entire directory
			return ErrStopWalk
		}

		return nil
	})
}

// Compile-time checks to ensure type implements desired interfaces.
var (
	_ = Reader(new(Tar))
	_ = Writer(new(Tar))
	_ = Archiver(new(Tar))
	_ = Unarchiver(new(Tar))
	_ = Walker(new(Tar))
	_ = Extractor(new(Tar))
)

// DefaultTar is a convenient Tar archiver ready to use.
var DefaultTar = &Tar{
	MkdirAll: true,
}