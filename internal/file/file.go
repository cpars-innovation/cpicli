package file

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
)

func CopyFile(src, dst string) (err error) {
	// Reference - https://gist.github.com/r0l1/92462b38df26839a3ca324697c8cba04
	in, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer in.Close()

	// Create directory for target file if it doesn't exist yet
	err = os.MkdirAll(filepath.Dir(dst), os.ModePerm)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	out, err := os.Create(dst)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = errors.Wrap(e, 0)
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	err = out.Sync()
	if err != nil {
		return errors.Wrap(err, 0)
	}

	si, err := os.Stat(src)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return errors.Wrap(err, 0)
	}

	return
}

func copyDir(src string, dst string) (err error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	if !si.IsDir() {
		return errors.Wrap(fmt.Errorf("source %v is not a directory", src), 0)
	}

	_, err = os.Stat(dst)
	if err != nil && errors.Is(err, fs.ErrExist) {
		return errors.Wrap(err, 0)
	}
	if err == nil {
		return errors.Wrap(fmt.Errorf("destination %v already exists", dst), 0)
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return errors.Wrap(err, 0)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return
			}
		} else {
			// Skip symlinks.
			if entry.Type() == fs.ModeSymlink {
				continue
			}

			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return
			}
		}
	}

	return
}
func UnzipSource(source, destination string) (err error) {
	// 1. Open the zip file
	reader, err := zip.OpenReader(source)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer reader.Close()

	// 2. Get the absolute destination path
	destination, err = filepath.Abs(destination)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	// 3. Iterate over zip files inside the archive and unzip each of them;
	// a symlinked destination is resolved first, so the prefix checks below
	// compare real paths
	if err = os.MkdirAll(destination, os.ModePerm); err != nil {
		return errors.Wrap(err, 0)
	}
	if destination, err = filepath.EvalSymlinks(destination); err != nil {
		return errors.Wrap(err, 0)
	}
	if len(reader.File) > MaxZipEntries {
		return errors.Wrap(fmt.Errorf("archive has %d entries, more than %d", len(reader.File), MaxZipEntries), 0)
	}
	var total int64
	for _, f := range reader.File {
		var n int64
		n, err = unzipFile(f, destination, MaxZipBytes-total)
		if err != nil {
			return
		}
		total += n
	}
	return
}

// Limits for unzipping an artifact: a hostile archive cannot fill the disk
// (zip bomb) or create more files than any CPI artifact has.
var (
	MaxZipBytes   int64 = 1 << 30 // 1 GiB uncompressed in total
	MaxZipEntries       = 100000
)

func unzipFile(f *zip.File, destination string, budget int64) (written int64, err error) {
	// 4. Check if file paths are not vulnerable to Zip Slip
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return 0, errors.Wrap(fmt.Errorf("invalid file path: %s", filePath), 0)
	}
	if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() && !f.FileInfo().IsDir() {
		return 0, errors.Wrap(fmt.Errorf("refusing %s: not a regular file or directory", f.Name), 0)
	}

	// 5. Create directory tree
	if f.FileInfo().IsDir() {
		if err = os.MkdirAll(filePath, 0o755); err != nil {
			return 0, errors.Wrap(err, 0)
		}
		return 0, inside(destination, filePath)
	}

	if err = os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return 0, errors.Wrap(err, 0)
	}
	// an existing directory inside destination may be a symlink pointing out
	if err = inside(destination, filepath.Dir(filePath)); err != nil {
		return 0, err
	}

	// 6. Create a destination file for unzipped content; permissions are not
	// taken from the archive (no setuid, no world-writable files)
	mode := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		mode = 0o755
	}
	destinationFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow, mode)
	if err != nil {
		return 0, errors.Wrap(err, 0)
	}
	defer destinationFile.Close()

	// 7. Unzip the content of a file and copy it to the destination file,
	// at most budget bytes
	zippedFile, err := f.Open()
	if err != nil {
		return 0, errors.Wrap(err, 0)
	}
	defer zippedFile.Close()

	written, err = io.Copy(destinationFile, io.LimitReader(zippedFile, budget+1))
	if err != nil {
		return written, errors.Wrap(err, 0)
	}
	if written > budget {
		return written, errors.Wrap(fmt.Errorf("archive larger than %d bytes uncompressed", MaxZipBytes), 0)
	}
	return written, nil
}

// inside reports an error unless path, with symlinks resolved, lies in root.
func inside(root, path string) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return errors.Wrap(fmt.Errorf("invalid file path: %s leaves the destination", path), 0)
	}
	return nil
}

func Exists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !errors.Is(err, os.ErrNotExist)
}

func ReplaceDir(src string, dst string) (err error) {
	err = os.RemoveAll(dst)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	return copyDir(src, dst)
}

// ZipDir Compress a directory into a zip file
func ZipDir(src string, dst string, includeSrc bool) error {
	// Create directory for target file if it doesn't exist yet
	err := os.MkdirAll(filepath.Dir(dst), os.ModePerm)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	zipfile, err := os.Create(dst)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, 0)
		}

		var name string
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return errors.Wrap(err, 0)
		}

		if includeSrc {
			name, err = filepath.Rel(filepath.Dir(src), path)
			if err != nil {
				return errors.Wrap(err, 0)
			}
		} else {
			if path == src {
				return nil
			}
			name, err = filepath.Rel(src, path)
			if err != nil {
				return errors.Wrap(err, 0)
			}
		}

		header.Name = filepath.ToSlash(name)

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return errors.Wrap(err, 0)
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return errors.Wrap(err, 0)
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
			if err != nil {
				return errors.Wrap(err, 0)
			}
		}
		return err
	})

	return err
}

func ZipDirToBase64(src string) (string, error) {
	zipFile := src + ".zip"
	err := ZipDir(src, zipFile, false)
	if err != nil {
		return "", err
	}
	fileContent, err := os.ReadFile(zipFile)
	if err != nil {
		return "", errors.Wrap(err, 0)
	}
	err = os.Remove(zipFile)
	if err != nil {
		return "", errors.Wrap(err, 0)
	}
	return base64.StdEncoding.EncodeToString(fileContent), nil
}

// ReadManifest reads a MANIFEST.MF file and returns key-value pairs
func ReadManifest(manifestPath string) (map[string]string, error) {
	metadata := make(map[string]string)

	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, errors.Wrap(err, 0)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	lines := strings.Split(string(content), "\n")
	var currentKey string
	var currentValue strings.Builder

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")

		// Multi-line values start with a space
		if len(line) > 0 && line[0] == ' ' {
			if currentKey != "" {
				currentValue.WriteString(strings.TrimPrefix(line, " "))
			}
			continue
		}

		// Save previous key-value pair if exists
		if currentKey != "" {
			metadata[currentKey] = currentValue.String()
			currentValue.Reset()
		}

		// Parse new key-value pair
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			currentKey = strings.TrimSpace(parts[0])
			currentValue.WriteString(strings.TrimSpace(parts[1]))
		} else {
			currentKey = ""
		}
	}

	// Save last key-value pair
	if currentKey != "" {
		metadata[currentKey] = currentValue.String()
	}

	return metadata, nil
}
