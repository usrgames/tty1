# tty1

The launcher of usr.games, an arcade in your terminal: one command that installs the games, keeps them current, and starts them.

**It is not that yet.** This repository holds its first part, the one everything else stands on: telling a release the studio signed from one it did not. There is no command to run so far.

## What is here

| Path | What |
|---|---|
| `internal/release` | Decides whether a release is the studio's: checks the signature of its manifest, then each file against the manifest. |
| `internal/release/allowed_signers` | The one key `tty1` trusts, the studio's root, built into the program. It is the list published at `usr.games/etc/allowed_signers`. |
| `internal/release/testdata` | A real release's manifest and signature, to test against. |

## How a release is checked

A release holds `manifest.json`, which names every file with its size and SHA-256, and `manifest.json.sig`, a signature of the manifest in the form `ssh-keygen -Y sign` writes. The signature carries a certificate: the studio's root key vouches, for a year at a time, for the key that signs releases. [KEYS.md](https://github.com/usrgames/usr.games/blob/main/KEYS.md) in the site's repository tells the whole of it.

`tty1` accepts a manifest only if all of this holds:

- the signature is for a release, and for nothing else the key might sign;
- it was made with a certificate, and the built-in root signed that certificate;
- the certificate is for the studio's releases, is in date by this machine's clock, and carries no condition `tty1` does not understand;
- the signature matches the manifest, to the byte;
- the manifest is the one asked for: this program, this version.

Then a file is accepted only if the manifest names it, and it has the size and the SHA-256 the manifest gives.

## The check is written here

Reading SSH's signatures and certificates is done in this repository, with Go's standard library and nothing else. The cryptography itself, Ed25519 and SHA-2, is the standard library's.

That is a choice. No library outside this repository can change what `tty1` trusts, and the code reads only the one kind of key and of certificate the studio uses. The price is that this reading code is ours to get right, so the tests carry the weight:

- **Against the real `ssh-keygen`.** On keys made fresh for each run, the verdict of the machine's own `ssh-keygen` and of this code must be the same, case by case. Where this code is stricter on purpose, the test says so.
- **Against a real release**, signed with the studio's release key.
- **Every byte.** No copy of that real signature with one byte changed, dropped or added is accepted.
- **Fuzzing.** `make fuzz` throws made-up signatures at the reader: it must refuse them and never crash.

## Working on it

```sh
make check
```

formats, tests and vets, for 64-bit and for 32-bit, since this code is all lengths and offsets.

When the site's list of signers changes, `internal/release/allowed_signers` changes with it, and so does the fingerprint in the test that pins it. CI compares the file with the site's.

## License

MIT. See [LICENSE](LICENSE).
