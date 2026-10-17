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
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"codeberg.org/git-pages/go-redirects"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/pflag"
	"golang.org/x/net/idna"
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

var passwordFlag = pflag.String("password", "", "use `<password>` for DNS challenge authorization")
var passwordFileFlag = pflag.String("password-file", "", "read `<filename>` for DNS challenge authorization")
var tokenFlag = pflag.String("token", "", "use `<token>` for forge authorization")
var challengeFlag = pflag.Bool("challenge", false, "compute DNS challenge entry from password (output zone file record)")
var challengeBareFlag = pflag.Bool("challenge-bare", false, "compute DNS challenge entry from password (output bare TXT value)")
var uploadGitFlag = pflag.String("upload-git", "", "replace site with contents of git `<repository>`")
var uploadDirFlag = pflag.String("upload-dir", "", "replace whole site or a path with contents of `<directory>`")
var redirectFlag = pflag.String("redirect", "", "redirect from <site-url>/... to `<redir-url>`/... (307 Temporary Redirect)")
var deleteFlag = pflag.Bool("delete", false, "delete whole site or a path")
var debugManifestFlag = pflag.Bool("debug-manifest", false, "retrieve site manifest as ProtoJSON, for debugging")
var serverFlag = pflag.String("server", "", "hostname of server to connect to")
var pathFlag = pflag.String("path", "", "upload contents to `<path>` without modifying the rest")
var parentsFlag = pflag.Bool("parents", false, "create parent directories of --path")
var atomicFlag = pflag.Bool("atomic", false, "require partial updates to be atomic")
var incrementalFlag = pflag.Bool("incremental", true, "make --upload-dir only upload changed files")
var expiresFlag = pflag.Uint("expires", 0, "schedule site to be expired after `<lifetime>` days")
var prettyURLsFlag = pflag.Bool("pretty-urls", false, "enable Netlify-style URL redirects")
var dryRunFlag = pflag.BoolP("dry-run", "n", false, "only check authorization, don't update")
var verboseFlag = pflag.BoolP("verbose", "v", false, "display more information for debugging")
var debugRequestFlag = pflag.Bool("debug-body", false, "dump request body for debugging")
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
	if *redirectFlag != "" {
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

func isEntryIgnored(entry fs.DirEntry) error {
	if entry.IsDir() && slices.Contains([]string{".git", ".jj"}, entry.Name()) {
		return fs.SkipDir
	}
	return nil
}

func expandSymlink(root *os.Root, entry fs.DirEntry) (
	linkTarget, symbolicPath, expandPath string, err error,
) {
	if linkTarget, err = root.Readlink(entry.Name()); err != nil {
		return
	}

	expandPath = linkTarget
	if filepath.IsAbs(expandPath) {
		// Absolute path.
	} else {
		expandPath = filepath.Clean(filepath.Join(entry.Name(), "..", expandPath))
		if strings.HasPrefix(expandPath, "../") {
			// Points outside of `root`; convert to an absolute path.
			expandPath = filepath.Join(root.Name(), expandPath)
		} else {
			// Points within `root`; keep relative.
			symbolicPath = expandPath
			expandPath = ""
		}
	}
	return
}

func displayFS(root *os.Root, prefix string) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := isEntryIgnored(entry); err != nil {
			return err
		}
		fullName := filepath.Join(prefix, name)
		switch {
		case entry.Type().IsDir():
			fmt.Fprintf(os.Stderr, "dir     %s\n", fullName)
		case entry.Type().IsRegular():
			fmt.Fprintf(os.Stderr, "file    %s\n", fullName)
		case entry.Type() == fs.ModeSymlink:
			linkTarget, symbolicPath, expandPath, err := expandSymlink(root, entry)
			if err != nil {
				return err
			}
			if symbolicPath != "" {
				fmt.Fprintf(os.Stderr, "symlink %s -> %s\n", fullName, linkTarget)
			}
			if expandPath != "" {
				fmt.Fprintf(os.Stderr, "follow  %s -> %s\n", fullName, linkTarget)
				expandRoot, err := os.OpenRoot(expandPath)
				if err != nil {
					return err
				}
				displayFS(expandRoot, prefix+entry.Name()+"/")
			}
		default:
			fmt.Fprintf(os.Stderr, "other   %s\n", fullName)
		}
		return nil
	})
}

type Unit = struct{}

func prettyURLsFS(root fs.FS, rootPath string) ([]redirects.Rule, error) {
	rootPath = path.Clean(rootPath)
	if rootPath != "/" {
		rootPath += "/"
	}

	entries := map[string]Unit{}
	err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := isEntryIgnored(entry); err != nil {
			return err
		}
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".html") {
			entries[name] = Unit{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	redirectRules := []redirects.Rule{}
	addRedirect := func(from string, to string, status int, force bool) {
		redirectRules = append(redirectRules, redirects.Rule{
			From:   rootPath + from,
			To:     rootPath + to,
			Status: status,
			Force:  force,
		})
	}

	// See https://answers.netlify.com/t/31191
	// The rules we use differ in one way: they are strongly normalizing (there is one canonical
	// URL and every other recognized URL returns a 3xx redirect to the user agent).
	for name := range entries {
		noExtName, _ := strings.CutSuffix(name, ".html")
		baseName := filepath.Base(name)
		dirName := filepath.Dir(name)
		if baseName == "index.html" && dirName == "." {
			// Navigating to /index.html will 301 redirect and serve /
			addRedirect("index.html", "", 301, true)
		} else if baseName == "index.html" { // With a file at /blog/index.html
			if _, found := entries[dirName+".html"]; dirName != "." && found {
				continue // (and no file at /blog.html!)
			}
			// Navigating to /blog.html will 301 redirect and serve /blog/
			addRedirect(dirName+".html", dirName+"/", 301, true)
			// Navigating to /blog/index.html will 301 redirect and serve /blog/
			addRedirect(dirName+"/index.html", dirName+"/", 301, true)
			// Navigating to /blog will 301 redirect and serve /blog/
			// (built-in)
			// Navigating to /blog/ will serve /blog/
			// (built-in)
		} else { // With a file at /blog.html
			// Navigating to /blog.html will ~~serve /blog.html~~ redirect and serve /blog
			addRedirect(name, noExtName, 301, true)
			// Navigating to /blog will serve /blog
			addRedirect(noExtName, name, 200, true)
			// Navigating to /blog/ will 301 redirect and serve /blog
			addRedirect(noExtName+"/", noExtName, 301, true)
		}
	}

	return redirectRules, nil
}

type fsSource struct {
	root           *os.Root
	prefix         string
	needBlobs      []string
	extraRedirects []redirects.Rule
}

// It doesn't make sense to use incremental updates for very small files since the cost of
// repeating a request to fill in a missing blob is likely to be higher than any savings gained.
const incrementalSizeThreshold = 256

func archiveFS(writer io.Writer, src *fsSource) (err error) {
	zstdWriter, _ := zstd.NewWriter(writer)
	tarWriter := tar.NewWriter(zstdWriter)

	requestedSet := make(map[string]struct{})
	for _, hash := range src.needBlobs {
		requestedSet[hash] = struct{}{}
	}

	redirectsText := redirects.Must(redirects.UnparseString(src.extraRedirects))
	redirectsFound := false

	var worker func(root *os.Root, prefix string) error
	worker = func(root *os.Root, prefix string) error {
		return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := isEntryIgnored(entry); err != nil {
				return err
			}
			header := &tar.Header{}
			data := []byte{}
			if src.prefix == "" && name == "." {
				return nil
			} else if name == "." {
				header.Name = src.prefix
			} else {
				header.Name = src.prefix + name
			}
			switch {
			case entry.Type().IsDir():
				header.Typeflag = tar.TypeDir
				header.Name += "/"
			case entry.Type().IsRegular():
				header.Typeflag = tar.TypeReg
				if data, err = fs.ReadFile(root.FS(), name); err != nil {
					return err
				}
				if src.prefix == "" && name == "_redirects" {
					redirectsFound = true
					// Append the auto-generated rules so that user-defined ones take priority.
					data = append(data, []byte("\n"+redirectsText)...)
				} else if *incrementalFlag && len(data) > incrementalSizeThreshold {
					hash := gitBlobSHA256(data)
					if _, requested := requestedSet[hash]; !requested {
						header.Typeflag = tar.TypeSymlink
						header.Linkname = "/git/blobs/" + hash
						data = nil
					}
				}
			case entry.Type() == fs.ModeSymlink:
				_, symbolicPath, expandPath, err := expandSymlink(root, entry)
				if err != nil {
					return err
				}
				if symbolicPath != "" {
					header.Typeflag = tar.TypeSymlink
					header.Linkname = symbolicPath
				}
				if expandPath != "" {
					header.Typeflag = tar.TypeDir
					expandRoot, err := os.OpenRoot(expandPath)
					if err != nil {
						return err
					}
					worker(expandRoot, prefix+entry.Name()+"/")
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
		})
	}
	if err = worker(src.root, src.prefix); err != nil {
		return
	}
	if !redirectsFound && len(redirectsText) > 0 {
		header := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     "_redirects",
			Size:     int64(len(redirectsText)),
		}
		if err = tarWriter.WriteHeader(header); err != nil {
			return
		}
		if _, err = tarWriter.Write([]byte(redirectsText)); err != nil {
			return
		}
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
func streamArchiveFS(src *fsSource) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		err := archiveFS(writer, src)
		if err != nil {
			writer.CloseWithError(err)
		} else {
			writer.Close()
		}
	}()
	return reader
}

func makeRedirects(rules []redirects.Rule) (reader io.Reader) {
	redirectsData := redirects.Must(redirects.UnparseString(rules))

	buffer := &bytes.Buffer{}
	tarWriter := tar.NewWriter(buffer)
	if err := tarWriter.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "_redirects",
		Size:     int64(len(redirectsData)),
	}); err != nil {
		panic(err)
	}
	if _, err := tarWriter.Write([]byte(redirectsData)); err != nil {
		panic(err)
	}
	if err := tarWriter.Close(); err != nil {
		panic(err)
	}
	return buffer
}

func makeWhiteout(path string) (reader io.Reader) {
	buffer := &bytes.Buffer{}
	tarWriter := tar.NewWriter(buffer)
	if err := tarWriter.WriteHeader(&tar.Header{
		Typeflag: tar.TypeChar,
		Name:     strings.TrimSuffix(path, "/"),
	}); err != nil {
		panic(err)
	}
	if err := tarWriter.Close(); err != nil {
		panic(err)
	}
	return buffer
}

const usageExitCode = 125

func usage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s <site-url> {--challenge|--upload-git url|--upload-dir path|--redirect url|--delete} [options...]\n",
		os.Args[0],
	)
	pflag.PrintDefaults()
}

func main() {
	pflag.CommandLine.MarkHidden("debug-body")
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

	if *passwordFlag == "" {
		*passwordFlag = os.Getenv("GIT_PAGES_PASSWORD")
	}

	if *tokenFlag == "" {
		*tokenFlag = os.Getenv("GIT_PAGES_TOKEN")
	}

	if *passwordFileFlag != "" && *passwordFlag != "" {
		fmt.Fprintf(os.Stderr, "error: --password-file and --password/$GIT_PAGES_PASSWORD are mutually exclusive\n")
		os.Exit(usageExitCode)
	}

	if *passwordFlag != "" && *tokenFlag != "" {
		fmt.Fprintf(os.Stderr, "error: --password-file/--password/$GIT_PAGES_PASSWORD and --token/$GIT_PAGES_TOKEN are mutually exclusive\n")
		os.Exit(usageExitCode)
	}

	if *passwordFileFlag != "" {
		contents, err := os.ReadFile(*passwordFileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid password file: %s\n", err)
			os.Exit(1)
		}
		// Trim all trailing newlines; there's no legitimate reason to have one in a password.
		*passwordFlag = strings.TrimRight(string(contents), "\n")
	}

	if *serverFlag != "" {
		var idnaProfile = idna.New(idna.MapForLookup(), idna.BidiRule())
		// See an explanation in git-pages auth.go for why this replacement is done.
		if _, err := idnaProfile.ToASCII(strings.ReplaceAll(*serverFlag, "_", "a")); err != nil {
			fmt.Fprintf(os.Stderr, "error: --server must be used with a domain name: %q\n", *serverFlag)
			os.Exit(usageExitCode)
		}
	}

	var pathPrefix string
	if *pathFlag != "" {
		if *uploadDirFlag == "" && !*deleteFlag {
			fmt.Fprintf(os.Stderr, "error: --path requires --upload-dir or --delete\n")
			os.Exit(usageExitCode)
		} else {
			pathPrefix = strings.Trim(*pathFlag, "/") + "/"
		}
	}

	var expiresAt time.Time
	if *expiresFlag != 0 {
		if *uploadGitFlag == "" && *uploadDirFlag == "" && *redirectFlag == "" {
			fmt.Fprintf(os.Stderr, "error: --expires requires --upload-dir\n")
			os.Exit(usageExitCode)
		} else {
			expiresAt = time.Now().AddDate(0, 0, int(*expiresFlag))
		}
	}

	if *prettyURLsFlag {
		if *pathFlag != "" {
			// Uploading `_redirects` clears all old redirects, so this can't be made to work.
			fmt.Fprintf(os.Stderr, "error: --pretty-urls is incompatible with --path\n")
			os.Exit(usageExitCode)
		}
		if *uploadDirFlag == "" {
			// Have to be able to enumerate files to generate redirects for them.
			fmt.Fprintf(os.Stderr, "error: --pretty-urls requires --upload-dir\n")
			os.Exit(usageExitCode)
		}
	}

	var err error
	siteURL, err := url.Parse(pflag.Args()[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid site URL: %s\n", err)
		os.Exit(1)
	}

	var request *http.Request
	var dirSource *fsSource
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
		uploadDir, err := os.OpenRoot(*uploadDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid directory: %s\n", err)
			os.Exit(1)
		}

		if *verboseFlag {
			err := displayFS(uploadDir, pathPrefix)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		}

		extraRedirects := []redirects.Rule{}
		if *prettyURLsFlag {
			extraRedirects, err = prettyURLsFS(uploadDir.FS(), siteURL.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
		}

		dirSource = &fsSource{
			root:           uploadDir,
			prefix:         pathPrefix,
			needBlobs:      []string{},
			extraRedirects: extraRedirects,
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
		if *pathFlag != "" {
			if *parentsFlag {
				request.Header.Add("Create-Parents", "yes")
			} else {
				request.Header.Add("Create-Parents", "no")
			}
		}
		request.GetBody = func() (io.ReadCloser, error) {
			return streamArchiveFS(dirSource), nil
		}
		request.Body, _ = request.GetBody()
		request.ContentLength = -1
		request.Header.Add("Content-Type", "application/x-tar+zstd")
		request.Header.Add("Accept", "application/vnd.git-pages.unresolved;q=1.0, text/plain;q=0.9")

	case *redirectFlag != "":
		redirectURL, err := url.Parse(*redirectFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

		request, err = http.NewRequest("PUT", siteURL.String(), makeRedirects([]redirects.Rule{
			{
				From:   "/*",
				To:     redirectURL.JoinPath(":splat").String(),
				Status: http.StatusTemporaryRedirect,
			},
		}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		request.Header.Add("Content-Type", "application/x-tar")

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
		manifestURL := siteURL.JoinPath(".git-pages/manifest.json")
		request, err = http.NewRequest("GET", manifestURL.String(), nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}

	default:
		panic("no operation chosen")
	}
	if !expiresAt.IsZero() {
		request.Header.Add("Expires", expiresAt.Format(http.TimeFormat))
	}
	if request.Method == "PATCH" {
		if *atomicFlag {
			request.Header.Add("Atomic", "yes")
		} else {
			request.Header.Add("Atomic", "no")
		}
	}
	request.Header.Add("User-Agent", versionInfo())
	makeAuthorization := func(headerName string, kind string, value string) {
		if strings.ContainsAny(value, "\r\n") {
			fmt.Fprintf(os.Stderr, "error: invalid characters in %s header value: %q\n",
				headerName, value)
			os.Exit(1)
		}
		request.Header.Add(headerName, fmt.Sprintf("%s %s", kind, value))
	}
	switch {
	case *passwordFlag != "":
		makeAuthorization("Authorization", "Pages", *passwordFlag)
	case *tokenFlag != "":
		makeAuthorization("Forge-Authorization", "token", *tokenFlag)
	}
	if *dryRunFlag {
		request.Header.Add("Dry-Run", "yes")
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

	// HTTP-to-HTTPS redirects are often implemented with 301 or 302 response codes which instruct
	// the Go HTTP client to disregard the original request method and use GET instead. To prevent
	// confusion, detect such redirects and preserve the original request method and body.
	http.DefaultClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		last := via[len(via)-1]
		newURL := req.URL.JoinPath()
		lastURL := last.URL.JoinPath()
		if lastURL.Scheme == "http" && newURL.Scheme == "https" {
			lastURL.Scheme = newURL.Scheme
			if lastURL.String() == newURL.String() {
				req.Method = last.Method
				req.Body, _ = last.GetBody()
				req.Header.Set("Content-Type", last.Header.Get("Content-Type"))
				return nil
			}
		}
		if req.Method != last.Method {
			return fmt.Errorf("unexpected redirect")
		}
		return nil
	}

	displayServer := *verboseFlag
	for numRequest := 0; ; numRequest += 1 {
		if *debugRequestFlag {
			archiveExt := "bin"
			switch request.Header.Get("Content-Type") {
			case "application/x-tar":
				archiveExt = "tar"
			case "application/x-tar+zstd":
				archiveExt = "tar.zst"
			}
			file, err := os.Create(fmt.Sprintf("body-%d.%s", numRequest, archiveExt))
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			body, err := request.GetBody()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			_, err = io.Copy(file, body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", err)
				os.Exit(1)
			}
			file.Close()
		}

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			if response != nil && (response.StatusCode == 301 || response.StatusCode == 302) {
				if innerErr := errors.Unwrap(err); innerErr != nil {
					// If the error is due to a redirect, that means it wasn't followed, but the
					// original url.Error confusingly reports the new URL anyway.
					err = innerErr
				}
			}
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			// Pain: https://github.com/golang/go/issues/71500
			if strings.HasSuffix(err.Error(), "tls: internal error") {
				fmt.Fprintf(os.Stderr, "hint: initial deployment to an https:// URL will fail; "+
					"use an http:// URL first\n")
			}
			os.Exit(1)
		}
		serverValues := response.Header.Values("Server")
		// Cloudflare &co love to insert themselves into the `Server:` header without a way to
		// override it. They shouldn't, but this is a losing battle, so provide a workaround for
		// people stuck behind it.
		serverValues = append(serverValues, response.Header.Values("X-Server")...)
		serverIdent := strings.Join(serverValues, ", ")
		//lint:ignore SA6000 This isn't a hot loop.
		if matched, err := regexp.MatchString(`\bgit-pages\b`, serverIdent); err != nil {
			panic(err)
		} else if !matched {
			message := fmt.Sprintf(
				"the tool only works with git-pages, but the URL points to a %q server",
				serverIdent)
			if request.Method != "GET" {
				fmt.Fprintf(os.Stderr, "error: %s\n", message)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "warning: %s\n", message)
		}
		if displayServer {
			fmt.Fprintf(os.Stderr, "server: %s\n", serverIdent)
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
				if err = scanner.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					os.Exit(1)
				}
				response.Body.Close()
				if *verboseFlag {
					fmt.Fprintf(os.Stderr, "incremental: need %d blobs\n", len(needBlobs))
				}
				request.GetBody = func() (io.ReadCloser, error) {
					dirSource.needBlobs = needBlobs
					return streamArchiveFS(dirSource), nil
				}
				request.Body, _ = request.GetBody()
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
