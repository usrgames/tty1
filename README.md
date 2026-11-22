# tty1

The launcher of usr.games, an arcade in your terminal: one command that brings the games home, keeps them current, and starts them.

```sh
tty1 ls                 # the games, what you have, and what is newer
tty1 install rustwake   # bring a game home
tty1 run rustwake       # start it
tty1 update             # bring everything up to date, tty1 included
```

**There is no release of `tty1` yet, and no game has one it can install.** Until then it builds from this repository with `go build`, and `tty1 ls` says of every game that it has no release yet.

## What it does, and what it will not do

- **Nothing is unpacked before it is checked.** A release's manifest has to carry the studio's signature. The archive is downloaded to its end and held to the size and the SHA-256 the manifest gives. Only then is it unpacked.
- **An archive is unpacked strictly.** Plain files with plain names, and nothing else: a path, a link, a device, or more than a release can hold stops it.
- **A game is put in place whole.** It is unpacked beside where it will live and moved in with one step. If anything fails, the game that was there is as it was.
- **It does not go back.** A "newest" release that is older than the one installed is refused, since that is what a release put back by someone else looks like.
- **The list of games is not trusted.** It says what to offer. Whatever name it is given, `tty1` installs only what the studio signed under that exact name.
- **`tty1` replaces itself only if a release built it.** Built with `go install`, it says to update the same way.

## Where things are

| What | Where |
|---|---|
| Installed games | `~/.local/share/tty1/games/NAME/`, or under `$XDG_DATA_HOME`. Each holds what the archive held and the manifest that vouched for it. Nothing is put on your `PATH`; removing the folder removes everything `tty1` installed. |
| The list of games | `https://usr.games/etc/games.json` |
| A game's releases | `https://github.com/usrgames/NAME/releases` |

Three variables point it elsewhere, for trying it out: `TTY1_HOME` for the folder, `TTY1_SITE` for the site, `TTY1_RELEASES` for where releases are. None of them changes whose signature it accepts.

## What is here

| Path | What |
|---|---|
| `main.go` | Wires the commands to this machine: its folders, its clock, the real network. |
| `internal/cli` | The commands, and the order their steps go in. |
| `internal/fetch` | The two things asked of the network: the site's list, and a program's releases. |
| `internal/release` | Decides whether a release is the studio's: checks the signature of its manifest, then each file against the manifest. |
| `internal/release/allowed_signers` | The one key `tty1` trusts, the studio's root, built into the program. It is the list published at `usr.games/etc/allowed_signers`. |
| `internal/release/releasetest` | A studio with keys of its own, made for a test, so that other packages' tests can sign releases. |
| `internal/release/testdata` | A real release's manifest and signature, to test against. |
| `internal/shelf` | The folder of installed games. |
| `internal/unpack` | The strict unpacking of an archive. |
| `internal/version` | Comparing two release tags. |

## How a release is checked

A release holds `manifest.json`, which names every file with its size and SHA-256, and `manifest.json.sig`, a signature of the manifest in the form `ssh-keygen -Y sign` writes. The signature carries a certificate: the studio's root key vouches, for a year at a time, for the key that signs releases. [KEYS.md](https://github.com/usrgames/usr.games/blob/main/KEYS.md) in the site's repository tells the whole of it.

`tty1` accepts a manifest only if all of this holds:

- the signature is for a release, and for nothing else the key might sign;
- it was made with a certificate, and the built-in root signed that certificate;
- the certificate is for the studio's releases, is in date by this machine's clock, and carries no condition `tty1` does not understand;
- the signature matches the manifest, to the byte;
- the manifest is the one asked for: this program, and a version no older than the one installed.

Then a file is accepted only if the manifest names it, and it has the size and the SHA-256 the manifest gives.

## The check is written here

Reading SSH's signatures and certificates is done in this repository, with Go's standard library and nothing else. The cryptography itself, Ed25519 and SHA-2, is the standard library's.

That is a choice. No library outside this repository can change what `tty1` trusts, and the code reads only the one kind of key and of certificate the studio uses. The price is that this reading code is ours to get right, so the tests carry the weight:

- **Against the real `ssh-keygen`.** On keys made fresh for each run, the verdict of the machine's own `ssh-keygen` and of this code must be the same, case by case. Where this code is stricter on purpose, the test says so.
- **Against a real release**, signed with the studio's release key.
- **Every byte.** No copy of that real signature with one byte changed, dropped or added is accepted.
- **Fuzzing.** `make fuzz` throws made-up signatures at the reader: it must refuse them and never crash.
- **A stand-in for the network.** The commands are tested against a local server that plays the site and the releases, with releases signed inside the test. Every way an install has to stop is tried, and each time the game that was there must still run.

## Working on it

```sh
make check
```

formats, tests and vets, for 64-bit and for 32-bit, since this code is all lengths and offsets.

When the site's list of signers changes, `internal/release/allowed_signers` changes with it, and so does the fingerprint in the test that pins it. CI compares the file with the site's.

## License

MIT. See [LICENSE](LICENSE).
