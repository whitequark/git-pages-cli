git-pages-cli
=============

_git-pages-cli_ is a command-line application for uploading sites to [git-pages].

If you want to publish a site from a Forgejo Actions workflow, use [git-pages/action] instead.

[git-pages]: https://codeberg.org/git-pages/git-pages
[git-pages/action]: https://codeberg.org/git-pages/action


Installation
------------

You will need [Go](https://go.dev/) 1.25 or newer. Run:

```console
$ go install codeberg.org/git-pages/git-pages-cli
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
$ git-pages-cli https://mycoolweb.site --password xyz --challenge
_git-pages-challenge.mycoolweb.site. 3600 IN TXT "317716dee4379c167e8b5ce9df38eb880e043e5a842d160fe8d5bb408ee0c191"
$ git-pages-cli https://mycoolweb.site --password xyz --challenge-bare
317716dee4379c167e8b5ce9df38eb880e043e5a842d160fe8d5bb408ee0c191
```

To deploy a site from a git repository available on the internet (`--password` may be omitted if the repository is allowlisted via DNS):

```console
$ git-pages-cli https://mycoolweb.site --upload-git https://codeberg.org/username/mycoolweb.site.git
$ git-pages-cli https://mycoolweb.site --password xyz --upload-git https://codeberg.org/username/mycoolweb.site.git
```

To deploy a site from a directory on your machine:

```console
$ git-pages-cli https://mycoolweb.site --password xyz --upload-dir site-contents
```

To delete a site:

```console
$ git-pages-cli https://mycoolweb.site --password xyz --delete
```


License
-------

[0-clause BSD](LICENSE-0BSD.txt)
