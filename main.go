package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"

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

func displayFS(root fs.FS) error {
	return fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.Type() == 0:
			fmt.Fprintln(os.Stderr, "file", name)
		case entry.Type() == fs.ModeDir:
			fmt.Fprintln(os.Stderr, "dir", name)
		case entry.Type() == fs.ModeSymlink:
			fmt.Fprintln(os.Stderr, "symlink", name)
		default:
			fmt.Fprintln(os.Stderr, "other", name)
		}
		return nil
	})
}

func archiveFS(writer io.Writer, root fs.FS) (err error) {
	zstdWriter, _ := zstd.NewWriter(writer)
	tarWriter := tar.NewWriter(zstdWriter)
	err = tarWriter.AddFS(root)
	if err != nil {
		return
	}
	err = tarWriter.Close()
	if err != nil {
		return
	}
	err = zstdWriter.Close()
	if err != nil {
		return
	}
	return
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
			err := displayFS(uploadDirFS.FS())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		}

		// Stream archive data without ever loading the entire working set into RAM.
		reader, writer := io.Pipe()
		go func() {
			err = archiveFS(writer, uploadDirFS.FS())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			writer.Close()
		}()

		request, err = http.NewRequest("PUT", siteURL.String(), reader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.ContentLength = -1
		request.Header.Add("Content-Type", "application/x-tar+zstd")

	case *deleteFlag:
		request, err = http.NewRequest("DELETE", siteURL.String(), nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
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
