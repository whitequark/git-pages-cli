git-pages-cli
=============

_git-pages-cli_ is a command-line application for publishing sites to [git-pages].

If you want to publish a site from a Forgejo Actions workflow, use [git-pages/action] instead.

[git-pages]: https://codeberg.org/git-pages/git-pages
[git-pages/action]: https://codeberg.org/git-pages/action


Installation
------------

You will need [Go](https://go.dev/) 1.25 or newer. Run:

```console
$ go install codeberg.org/git-pages/git-pages-cli@latest
```

If you prefer, you may also use a [Docker container][docker]:

```console
docker run --rm codeberg.org/git-pages/git-pages-cli:latest ...
```

[docker]: https://codeberg.org/git-pages/-/packages/container/git-pages-cli/latest


Usage
-----

To prepare a DNS challenge for a given site and password:

```console
$ git-pages-cli https://example.org --password xyz --challenge
_git-pages-challenge.example.org. 3600 IN TXT "317716dee4379c167e8b5ce9df38eb880e043e5a842d160fe8d5bb408ee0c191"
$ git-pages-cli https://example.org --password xyz --challenge-bare
317716dee4379c167e8b5ce9df38eb880e043e5a842d160fe8d5bb408ee0c191
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
