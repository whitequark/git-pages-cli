package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
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
var uploadDirFlag = pflag.String("upload-dir", "", "replace site with contents of specified directory")
var deleteFlag = pflag.Bool("delete", false, "delete site")
var debugManifestFlag = pflag.Bool("debug-manifest", false, "retrieve site manifest as ProtoJSON, for debugging")
var serverFlag = pflag.String("server", "", "hostname of server to connect to")
var pathFlag = pflag.String("path", "", "partially update site at specified path")
var atomicFlag = pflag.Bool("atomic", false, "require partial updates to be atomic")
var verboseFlag = pflag.Bool("verbose", false, "display more information for debugging")
var versionFlag = pflag.Bool("version", false, "display version information")

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

func archiveFS(writer io.Writer, root fs.FS, prefix string) (err error) {
	zstdWriter, _ := zstd.NewWriter(writer)
	tarWriter := tar.NewWriter(zstdWriter)
	if err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		var tarName string
		if prefix == "" && name == "." {
			return nil
		} else if name == "." {
			tarName = prefix
		} else {
			tarName = prefix + name
		}
		var file io.ReadCloser
		var linkTarget string
		switch {
		case entry.Type().IsDir():
			name += "/"
		case entry.Type().IsRegular():
			if file, err = root.Open(name); err != nil {
				return err
			}
			defer file.Close()
		case entry.Type() == fs.ModeSymlink:
			if linkTarget, err = fs.ReadLink(root, name); err != nil {
				return err
			}
		default:
			return errors.New("tar: cannot add non-regular file")
		}
		header, err := tar.FileInfoHeader(fileInfo, linkTarget)
		if err != nil {
			return err
		}
		header.Name = tarName
		if err = tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if file != nil {
			_, err = io.Copy(tarWriter, file)
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

	var err error
	siteURL, err := url.Parse(pflag.Args()[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid site URL: %s\n", err)
		os.Exit(1)
	}

	var request *http.Request
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
		uploadDirFS, err := os.OpenRoot(*uploadDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid directory: %s\n", err)
			os.Exit(1)
		}

		if *verboseFlag {
			err := displayFS(uploadDirFS.FS(), pathPrefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		}

		// Stream archive data without ever loading the entire working set into RAM.
		reader, writer := io.Pipe()
		go func() {
			err = archiveFS(writer, uploadDirFS.FS(), pathPrefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			writer.Close()
		}()

		if *pathFlag == "" {
			request, err = http.NewRequest("PUT", siteURL.String(), reader)
		} else {
			request, err = http.NewRequest("PATCH", siteURL.String(), reader)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.ContentLength = -1
		request.Header.Add("Content-Type", "application/x-tar+zstd")

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

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	if *verboseFlag {
		fmt.Fprintf(os.Stderr, "server: %s\n", response.Header.Get("Server"))
	}
	if *debugManifestFlag {
		if response.StatusCode == 200 {
			io.Copy(os.Stdout, response.Body)
			fmt.Fprintf(os.Stdout, "\n")
		} else {
			io.Copy(os.Stderr, response.Body)
			os.Exit(1)
		}
	} else { // an update operation
		if response.StatusCode == 200 {
			fmt.Fprintf(os.Stdout, "result: %s\n", response.Header.Get("Update-Result"))
			io.Copy(os.Stdout, response.Body)
		} else {
			fmt.Fprintf(os.Stderr, "result: error\n")
			io.Copy(os.Stderr, response.Body)
			os.Exit(1)
		}
	}
}
