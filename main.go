package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/pflag"
)

// By default the version information is retrieved from VCS. If not available during build,
// override this variable using linker flags to change the displayed version.
// Example: `-ldflags "-X main.versionOverride=v1.2.3"`
var versionOverride = ""

func versionInfo() string {
	version := "(unknown)"
	if versionOverride != "" {
		version = versionOverride
	} else if buildInfo, ok := debug.ReadBuildInfo(); ok {
		version = buildInfo.Main.Version
	}
	return fmt.Sprintf("git-pages-cli %s", version)
}

var passwordFlag = pflag.String("password", "", "password for DNS challenge authorization")
var tokenFlag = pflag.String("token", "", "token for forge authorization")
var challengeFlag = pflag.Bool("challenge", false, "compute DNS challenge entry from password (output zone file record)")
var challengeBareFlag = pflag.Bool("challenge-bare", false, "compute DNS challenge entry from password (output bare TXT value)")
var uploadGitFlag = pflag.String("upload-git", "", "replace site with contents of specified git repository")
var uploadDirFlag = pflag.String("upload-dir", "", "replace whole site or a path with contents of specified directory")
var deleteFlag = pflag.Bool("delete", false, "delete whole site or a path")
var debugManifestFlag = pflag.Bool("debug-manifest", false, "retrieve site manifest as ProtoJSON, for debugging")
var serverFlag = pflag.String("server", "", "hostname of server to connect to")
var pathFlag = pflag.String("path", "", "partially update site at specified path")
var parentsFlag = pflag.Bool("parents", false, "create parent directories of --path")
var atomicFlag = pflag.Bool("atomic", false, "require partial updates to be atomic")
var incrementalFlag = pflag.Bool("incremental", false, "only upload changed files")
var verboseFlag = pflag.BoolP("verbose", "v", false, "display more information for debugging")
var versionFlag = pflag.BoolP("version", "V", false, "display version information")

func singleOperation() bool {
	operations := 0
	if *challengeFlag {
		operations++
	}
	if *challengeBareFlag {
		operations++
	}
	if *uploadDirFlag != "" {
		operations++
	}
	if *uploadGitFlag != "" {
		operations++
	}
	if *deleteFlag {
		operations++
	}
	if *debugManifestFlag {
		operations++
	}
	if *versionFlag {
		operations++
	}
	return operations == 1
}

func gitBlobSHA256(data []byte) string {
	h := crypto.SHA256.New()
	h.Write([]byte("blob "))
	h.Write([]byte(strconv.FormatInt(int64(len(data)), 10)))
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func displayFS(root fs.FS, prefix string) error {
	return fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.Type().IsDir():
			fmt.Fprintf(os.Stderr, "dir     %s%s\n", prefix, name)
		case entry.Type().IsRegular():
			fmt.Fprintf(os.Stderr, "file    %s%s\n", prefix, name)
		case entry.Type() == fs.ModeSymlink:
			fmt.Fprintf(os.Stderr, "symlink %s%s\n", prefix, name)
		default:
			fmt.Fprintf(os.Stderr, "other   %s%s\n", prefix, name)
		}
		return nil
	})
}

// It doesn't make sense to use incremental updates for very small files since the cost of
// repeating a request to fill in a missing blob is likely to be higher than any savings gained.
const incrementalSizeThreshold = 256

func archiveFS(writer io.Writer, root fs.FS, prefix string, needBlobs []string) (err error) {
	requestedSet := make(map[string]struct{})
	for _, hash := range needBlobs {
		requestedSet[hash] = struct{}{}
	}
	zstdWriter, _ := zstd.NewWriter(writer)
	tarWriter := tar.NewWriter(zstdWriter)
	if err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		header := &tar.Header{}
		data := []byte{}
		if prefix == "" && name == "." {
			return nil
		} else if name == "." {
			header.Name = prefix
		} else {
			header.Name = prefix + name
		}
		switch {
		case entry.Type().IsDir():
			header.Typeflag = tar.TypeDir
			header.Name += "/"
		case entry.Type().IsRegular():
			header.Typeflag = tar.TypeReg
			if data, err = fs.ReadFile(root, name); err != nil {
				return err
			}
			if *incrementalFlag && len(data) > incrementalSizeThreshold {
				hash := gitBlobSHA256(data)
				if _, requested := requestedSet[hash]; !requested {
					header.Typeflag = tar.TypeSymlink
					header.Linkname = "/git/blobs/" + hash
					data = nil
				}
			}
		case entry.Type() == fs.ModeSymlink:
			header.Typeflag = tar.TypeSymlink
			if header.Linkname, err = fs.ReadLink(root, name); err != nil {
				return err
			}
		default:
			return errors.New("tar: cannot add non-regular file")
		}
		header.Size = int64(len(data))
		if err = tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err = tarWriter.Write(data); err != nil {
			return err
		}
		return err
	}); err != nil {
		return
	}
	if err = tarWriter.Close(); err != nil {
		return
	}
	if err = zstdWriter.Close(); err != nil {
		return
	}
	return
}

// Stream archive data without ever loading the entire working set into RAM.
func streamArchiveFS(root fs.FS, prefix string, needBlobs []string) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		err := archiveFS(writer, root, prefix, needBlobs)
		if err != nil {
			writer.CloseWithError(err)
		} else {
			writer.Close()
		}
	}()
	return reader
}

func makeWhiteout(path string) (reader io.Reader) {
	buffer := &bytes.Buffer{}
	tarWriter := tar.NewWriter(buffer)
	tarWriter.WriteHeader(&tar.Header{
		Typeflag: tar.TypeChar,
		Name:     path,
	})
	tarWriter.Flush()
	return buffer
}

const usageExitCode = 125

func usage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s <site-url> {--challenge|--upload-git url|--upload-dir path|--delete} [options...]\n",
		os.Args[0],
	)
	pflag.PrintDefaults()
}

func main() {
	pflag.Usage = usage
	pflag.Parse()
	if !singleOperation() || (!*versionFlag && len(pflag.Args()) != 1) {
		pflag.Usage()
		os.Exit(usageExitCode)
	}

	if *versionFlag {
		fmt.Fprintln(os.Stdout, versionInfo())
		os.Exit(0)
	}

	if *passwordFlag != "" && *tokenFlag != "" {
		fmt.Fprintf(os.Stderr, "--password and --token are mutually exclusive")
		os.Exit(usageExitCode)
	}

	var pathPrefix string
	if *pathFlag != "" {
		if *uploadDirFlag == "" && !*deleteFlag {
			fmt.Fprintf(os.Stderr, "--path requires --upload-dir or --delete")
			os.Exit(usageExitCode)
		} else {
			pathPrefix = strings.Trim(*pathFlag, "/") + "/"
		}
	}

	if *incrementalFlag && *uploadDirFlag == "" {
		fmt.Fprintf(os.Stderr, "--incremental requires --upload-dir")
		os.Exit(usageExitCode)
	}

	var err error
	siteURL, err := url.Parse(pflag.Args()[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid site URL: %s\n", err)
		os.Exit(1)
	}

	var request *http.Request
	var uploadDir *os.Root
	switch {
	case *challengeFlag || *challengeBareFlag:
		if *passwordFlag == "" {
			*passwordFlag = uuid.NewString()
			fmt.Fprintf(os.Stderr, "password: %s\n", *passwordFlag)
		}

		challenge := sha256.Sum256(fmt.Appendf(nil, "%s %s", siteURL.Hostname(), *passwordFlag))
		if *challengeBareFlag {
			fmt.Fprintf(os.Stdout, "%x\n", challenge)
		} else {
			fmt.Fprintf(os.Stdout, "_git-pages-challenge.%s. 3600 IN TXT \"%x\"\n", siteURL.Hostname(), challenge)
		}
		os.Exit(0)

	case *uploadGitFlag != "":
		uploadGitUrl, err := url.Parse(*uploadGitFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid repository URL: %s\n", err)
			os.Exit(1)
		}

		requestBody := []byte(uploadGitUrl.String())
		request, err = http.NewRequest("PUT", siteURL.String(), bytes.NewReader(requestBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	case *uploadDirFlag != "":
		uploadDir, err = os.OpenRoot(*uploadDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid directory: %s\n", err)
			os.Exit(1)
		}

		if *verboseFlag {
			err := displayFS(uploadDir.FS(), pathPrefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		}

		if *pathFlag == "" {
			request, err = http.NewRequest("PUT", siteURL.String(), nil)
		} else {
			request, err = http.NewRequest("PATCH", siteURL.String(), nil)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.Body = streamArchiveFS(uploadDir.FS(), pathPrefix, []string{})
		request.ContentLength = -1
		request.Header.Add("Content-Type", "application/x-tar+zstd")
		request.Header.Add("Accept", "application/vnd.git-pages.unresolved;q=1.0, text/plain;q=0.9")
		if *parentsFlag {
			request.Header.Add("Create-Parents", "yes")
		} else {
			request.Header.Add("Create-Parents", "no")
		}

	case *deleteFlag:
		if *pathFlag == "" {
			request, err = http.NewRequest("DELETE", siteURL.String(), nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		} else {
			request, err = http.NewRequest("PATCH", siteURL.String(), makeWhiteout(pathPrefix))
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			request.Header.Add("Content-Type", "application/x-tar")
		}

	case *debugManifestFlag:
		manifestURL := siteURL.ResolveReference(&url.URL{Path: ".git-pages/manifest.json"})
		request, err = http.NewRequest("GET", manifestURL.String(), nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

	default:
		panic("no operation chosen")
	}
	request.Header.Add("User-Agent", versionInfo())
	if request.Method == "PATCH" {
		if *atomicFlag {
			request.Header.Add("Atomic", "yes")
			request.Header.Add("Race-Free", "yes") // deprecated name, to be removed soon
		} else {
			request.Header.Add("Atomic", "no")
			request.Header.Add("Race-Free", "no") // deprecated name, to be removed soon
		}
	}
	switch {
	case *passwordFlag != "":
		request.Header.Add("Authorization", fmt.Sprintf("Pages %s", *passwordFlag))
	case *tokenFlag != "":
		request.Header.Add("Forge-Authorization", fmt.Sprintf("token %s", *tokenFlag))
	}
	if *serverFlag != "" {
		// Send the request to `--server` host, but set the `Host:` header to the site host.
		// This allows first-time publishing to proceed without the git-pages server yet having
		// a TLS certificate for the site host (which has a circular dependency on completion of
		// first-time publishing).
		newURL := *request.URL
		newURL.Host = *serverFlag
		request.URL = &newURL
		request.Header.Set("Host", siteURL.Host)
	}

	displayServer := *verboseFlag
	for {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		if displayServer {
			fmt.Fprintf(os.Stderr, "server: %s\n", response.Header.Get("Server"))
			displayServer = false
		}
		if *debugManifestFlag {
			if response.StatusCode == http.StatusOK {
				io.Copy(os.Stdout, response.Body)
				fmt.Fprintf(os.Stdout, "\n")
			} else {
				io.Copy(os.Stderr, response.Body)
				os.Exit(1)
			}
		} else { // an update operation
			if *verboseFlag {
				fmt.Fprintf(os.Stderr, "response: %d %s\n",
					response.StatusCode, response.Header.Get("Content-Type"))
			}
			if response.StatusCode == http.StatusUnprocessableEntity &&
				response.Header.Get("Content-Type") == "application/vnd.git-pages.unresolved" {
				needBlobs := []string{}
				scanner := bufio.NewScanner(response.Body)
				for scanner.Scan() {
					needBlobs = append(needBlobs, scanner.Text())
				}
				response.Body.Close()
				if *verboseFlag {
					fmt.Fprintf(os.Stderr, "incremental: need %d blobs\n", len(needBlobs))
				}
				request.Body = streamArchiveFS(uploadDir.FS(), pathPrefix, needBlobs)
				continue // resubmit
			} else if response.StatusCode == http.StatusOK {
				fmt.Fprintf(os.Stdout, "result: %s\n", response.Header.Get("Update-Result"))
				io.Copy(os.Stdout, response.Body)
			} else {
				fmt.Fprintf(os.Stderr, "result: error\n")
				io.Copy(os.Stderr, response.Body)
				os.Exit(1)
			}
		}
		break
	}
}
