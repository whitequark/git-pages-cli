git-pages-cli
=============

_git-pages-cli_ is a command-line application for uploading sites to [git-pages].

[git-pages]: https://codeberg.org/git-pages/git-pages


Installation
------------

You will need [Go](https://go.dev/) 1.25 or newer. Run:

```console
$ go install codeberg.org/git-pages/git-pages-cli
```


Usage
-----

To deploy a site from a git repository available on the internet (`--password` may be omitted if the repository is allowlisted via DNS):

```console
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
