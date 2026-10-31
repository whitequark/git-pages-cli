# Changelog

## v1.13.0

* Changed: git-pages-cli requires Go 1.26.10.
* Added: `--strict` option, supported since git-pages v0.10.1+git94f29c73.

## v1.12.1

* Fixed: `--server` accepts `host` (as well as `host:port`) once again. (Regression in v1.11.3.)

## v1.12.0

* Added: `--comment` option, supported since git-pages v0.10.0.
* Changed: `--incremental` option is hidden from help. Incremental uploads are reliable and the option is kept for debugging only.

## v1.11.3

* Fixed: `--server` accepts `host:port` once again. (Regression in v1.11.0.)
* Fixed: `--pretty-urls` prepends site name to 'from' and 'to' URLs in `_redirects`, requires git-pages v0.10.0.

## v1.11.2

* Fixed: `--pretty-urls` creates a redirect from `/index.html` to `/`.

## v1.11.1

* Fixed: `--pretty-urls` works if the upload lacks an existing `_redirects` file.

## v1.11.0

* Added: `--redirect` option, to create sites consisting only of a splat redirect.
* Added: `--pretty-urls` option, to automatically generate `_redirects` file like GitHub or Netlify, where `/page-name.html` has URL `/page-name`, and `/category-name/index.html` has URL `/category-name/`.
* Changed: symlinks are followed outside of `--upload-dir`.
* Fixed: combination of `--path` and `--delete`.
* Added: debugging flag `--debug-body`, to dump HTTP request bodies.
* Changed: improved syntax checking for `--server`.
* Changed: new diagnostic for initial deployment of a site where no TLS certificate can be acquired yet.
* Changed: `.git` and `.jj` directories are skipped, to avoid wasting server disk space and leaking potentially sensitive data.

## v1.10.1

* Changed: only abort on unknown `Server:` header for requests that update sites.

## v1.10.0

* Changed: use absolute `ENTRYPOINT` in Docker image.
* Added: `--dry-run` option.

## v1.9.0

* Added: `--expires <days>` flag.
* Removed: deprecated `Race-Free:` header in requests.

## v1.8.2

* Fixed: HTTP request body is re-submitted when a 301 or 302 redirect is received, usually as part of `http://` to `https://` redirect.
* Fixed: `Create-Parents:` header is included only in `PATCH` requests.

## v1.8.1

* Added: `X-Server:` header is considered alongside `Server:`.
* Fixed: correct `--version` information in first-party artifacts.

## v1.8.0

* Added: secrets specified via `GIT_PAGES_PASSWORD` and `GIT_PAGES_TOKEN`.

## v1.7.0

* Changed: `--incremental` mode is enabled by default.

## v1.6.1

* Fixed: null pointer dereference while handling HTTP response error.
* Fixed: necessary token permissions in README.

## v1.6.0

* Added: `--password-file` option.

## v1.5.3

* Fixed: preserve request method even for 301 and 302 HTTP redirects, usually as part of `http://` to `https://` redirect.
* Changed: exit with an error if `Server:` header does not indicate git-pages being used.
* Fixed: `--debug-manifest` for site URLs not ending in a `/`.

## v1.5.2

* Changed: git-pages-cli requires Go 1.25.6.
* Changed: Nix flake uses gomod2nix.

## v1.5.1

* Changed: `--incremental` is accepted and does nothing for modes other than `--upload-dir`.

## v1.5.0

* Added: `--incremental` mode for `--upload-dir`.

## v1.4.2

* Added: `--parents` flag for `--path` option.
* Added: `-v` and `-V` aliases for `--verbose` and `--version`.

## v1.4.1

* Changed: include `Atomic:` header with the same value as `Race-Free:` in HTTP requests.

## v1.4.0

* Added: `--path` option to perform partial site updates.

## v1.3.2

* Changed: improved `--help` output with usage information.

## v1.3.1

No functional changes.

## v1.3.0

* Changed: stream uploaded data instead of buffering the entire request in memory.

## v1.2.1

* Changed: packagers can override `--version` information during build.

## v1.2.0

* Added: `--token` option, for forge authorization.

## v1.1.0

* Changed: expose version in `User-Agent:` header.

## v1.0.1

* Changed: containers are mirrored on https://code.forgejo.org and https://data.forgejo.org.

## v1.0.0

Initial release.
