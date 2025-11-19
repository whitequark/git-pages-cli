git-pages-cli
=============

_git-pages-cli_ is a command-line application for publishing sites to [git-pages].

If you want to publish a site from a Forgejo Actions workflow, use [git-pages/action] instead.

[git-pages]: https://codeberg.org/git-pages/git-pages
[git-pages/action]: https://codeberg.org/git-pages/action


Installation
------------

You can install _git-pages-cli_ using one of the following methods:

1. **Downloading a binary**. You can download the [latest build][latest] or pick a [release][releases].

1. **Using a Docker container**. Choose between the latest build or a [release tag][containers]. Then run:

   ```console
   $ docker run --rm codeberg.org/git-pages/git-pages-cli:latest ...
   ```

1. **Installing from source**. First, install [Go](https://go.dev/) 1.25 or newer. Then run:

   ```console
   $ go install codeberg.org/git-pages/git-pages-cli@latest
   ```

[latest]: https://codeberg.org/git-pages/git-pages-cli/releases/tag/latest
[releases]: https://codeberg.org/git-pages/git-pages-cli/releases
[containers]: https://codeberg.org/git-pages/-/packages/container/git-pages-cli/versions


Usage
-----

To prepare a DNS challenge for a given site and password:

```console
$ git-pages-cli https://example.org --challenge  # generate a random password
password: 28a616f4-2fbe-456b-8456-056d1f38e815
_git-pages-challenge.example.org. 3600 IN TXT "a59ecb58f7256fc5afb6b96892501007b0b65d64f251b1aca749b0fca61d582c"
$ git-pages-cli https://example.org --password xyz --challenge
_git-pages-challenge.example.org. 3600 IN TXT "6c47172c027b3c79358f9f8c110886baf4826d9bc2a1c7d0f439cc770ed42dc8"
$ git-pages-cli https://example.org --password xyz --challenge-bare
6c47172c027b3c79358f9f8c110886baf4826d9bc2a1c7d0f439cc770ed42dc8
```

To publish a site from a git repository available on the internet (`--password` may be omitted if the repository is allowlisted via DNS):

```console
$ git-pages-cli https://example.org --upload-git https://codeberg.org/username/example.org.git
$ git-pages-cli https://example.org --password xyz --upload-git https://codeberg.org/username/example.org.git
```

To publish a site from a directory on your machine:

```console
$ git-pages-cli https://example.org --password xyz --upload-dir site-contents
```

To delete a site:

```console
$ git-pages-cli https://example.org --password xyz --delete
```

It is not possible to publish a site to a domain for the first time using HTTPS, since the git-pages server is not allowed to acquire a TLS certificate for a domain before a site is published on that domain. Either use plain HTTP instead, or provide a hostname for which the server *does* have a TLS certificate using the `--server` option:

```console
$ git-pages-cli https://example.org --server grebedoc.dev --password xyz --upload-dir ...
```


Advanced usage
--------------

To retrieve the site manifest (for debugging only: manifest schema is not versioned and **subject to change without notice**, including renaming of existing fields):

```console
$ git-pages-cli https://example.org --password xyz --debug-manifest
{
  "contents": {
    "": {
      "type": "Directory"
    },
    "index.html": {
      "type": "InlineFile",
      "size": "5",
      "data": "bWVvdwo=",
      "contentType": "text/html; charset=utf-8"
    }
  },
  "originalSize": "5",
  "compressedSize": "5",
  "storedSize": "0",
  "redirects": [],
  "headers": [],
  "problems": []
}
```


License
-------

[0-clause BSD](LICENSE-0BSD.txt)
