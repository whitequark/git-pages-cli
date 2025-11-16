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

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/pflag"
)

var passwordFlag = pflag.String("password", "", "password for DNS challenge authorization")
var challengeFlag = pflag.Bool("challenge", false, "compute DNS challenge entry from password (output zone file record)")
var challengeBareFlag = pflag.Bool("challenge-bare", false, "compute DNS challenge entry from password (output bare TXT value)")
var uploadGitFlag = pflag.String("upload-git", "", "replace site with contents of specified git repository")
var uploadDirFlag = pflag.String("upload-dir", "", "replace site with contents of specified directory")
var deleteFlag = pflag.Bool("delete", false, "delete site")
var debugManifestFlag = pflag.Bool("debug-manifest", false, "retrieve site manifest as ProtoJSON, for debugging")
var serverFlag = pflag.String("server", "", "hostname of server to connect to")
var verboseFlag = pflag.Bool("verbose", false, "display more information for debugging")

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

func archiveFS(root fs.FS) (result []byte, err error) {
	buffer := bytes.Buffer{}
	zstdWriter, _ := zstd.NewWriter(&buffer)
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
	result = buffer.Bytes()
	return
}

func main() {
	pflag.Parse()
	if !singleOperation() || len(pflag.Args()) != 1 {
		fmt.Fprintf(os.Stderr,
			"Usage: %s <site-url> [--challenge|--upload-git url|--upload-dir path|--delete]\n",
			os.Args[0],
		)
		os.Exit(125)
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
			fmt.Fprintf(os.Stderr, "error: no --password option specified\n")
			os.Exit(1)
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

		requestBody, err := archiveFS(uploadDirFS.FS())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

		request, err = http.NewRequest("PUT", siteURL.String(), bytes.NewReader(requestBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.Header.Add("Content-Type", "application/x-tar+zstd")

	case *deleteFlag:
		request, err = http.NewRequest("DELETE", siteURL.String(), bytes.NewReader([]byte{}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

	case *debugManifestFlag:
		manifestURL := siteURL.ResolveReference(&url.URL{Path: ".git-pages/manifest.json"})
		request, err = http.NewRequest("GET", manifestURL.String(), bytes.NewReader([]byte{}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

	default:
		panic("no operation chosen")
	}
	if *passwordFlag != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Pages %s", *passwordFlag))
	}
	if *serverFlag != "" {
		// Send the request to `--server` host, but set the `Host:` header to the site host.
		// This allows first-time publishing to proceed without the git-pages server yet having
		// a TLS certificate for the site host (which has a circular dependency on completion of
		// first-time publishing).
		newURL := *siteURL
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
