# OpenList Client

A lightweight, cross-platform client for [OpenList](https://github.com/OpenListTeam/OpenList), written in Go with [NSGo](https://nsgo.dev/). It draws with each platform's own native views, in light and dark appearance, with Simplified Chinese and English.

> Status: early (v0.1.0). `go tool nsgo build` packages macOS (arm64), Windows (x64), the iOS simulator, Android (arm64) and the web; the code also supports Linux.

## Features

Several servers (each with its own folders and download settings), browsing with breadcrumbs, sorting, list and grid views, search below the folder or through the whole server, selection with batch move and delete, a floating menu for new folders and files, upload and download queues, offline downloads with their progress, file preview, and permission checks with guidance.

## Signing in and where logins are kept

A login stays across restarts and is only deleted when you press **Sign out** on that server in Settings. The access token is kept in the platform's secure store through NSGo, never in a plain file:

| Platform | Store |
| --- | --- |
| macOS, iOS | Keychain |
| Windows | Credential Manager |
| Linux | Secret Service (GNOME Keyring, KWallet, ...): install one if the app asks |
| Android | AES-GCM with a key in the Android Keystore |
| Web | Memory only: you sign in again after a reload |

Tokens are only sent to the server's own host, and not over plain `http` when the server is reached over `https`.

## Downloads

By default files go to the system's Downloads folder, in an `OpenList` subfolder, and each server can use its own folder. In the browser a file is handed to the browser's download, or written to a folder you grant access to (large files need that folder).

## Layout

| Path | What it holds |
| --- | --- |
| `main.go` | The entry point: runs `app.Root` |
| `nsgo.yaml` | App name, id, version, icons, language catalogs and build targets (NSGo needs it in the module root) |
| `src/app/` | The app: window and navigation, state, Files, Transfers, Settings, sheets, preview, permissions, and its UI tests |
| `src/internal/openlist/` | OpenList HTTP API client |
| `src/internal/browse/` | Sorting, filtering, search crawl and floating-menu placement |
| `src/internal/store/` | Sites, settings and where tokens are kept |
| `src/internal/transfer/` | Upload and download engine |
| `src/internal/access/` | Classifies failures into permission problems |
| `src/locales/` | `en.yaml` and `zh-Hans.yaml` catalogs; `catalog_gen.go` is generated |
| `src/theme/` | The theme (`openlist.yaml`) and its loader |
| `.github/workflows/` | `release.yml`: builds and publishes the clients when a release is published |
| `design/prototype/` | The Open Design HTML prototype of the app |
| `design/icon/` | App icon sources and the OpenList logo it is made from |

Keep the prototype in step with the app when the UI changes; its header says which source file each screen matches.

## Develop

Needs Go 1.27.3 or later (Xcode for Apple targets).

```sh
go tool nsgo doctor        # which targets this machine can build and run
go tool nsgo dev           # run with hot reload; -target web for the browser
go tool nsgo test          # headless tests
go tool nsgo check         # vet
go tool nsgo build         # package every target in nsgo.yaml into dist/
go tool nsgo build -target macos_arm64   # one target; add -installers=false to skip the dmg
```

Run the `go tool nsgo` commands rather than `go build` or `go test` directly: they type-check, generate the locale catalogs and vet every target.

The prototype is a single file: open `design/prototype/index-v0.0.1.html` in a browser (`?platform=desktop|web|android`, `?perm=folder,network,server,browser`).

## Releases

Publishing a release on GitHub runs [`.github/workflows/release.yml`](.github/workflows/release.yml), which builds the clients and adds them to that release:

| Client | Files | Built on |
| --- | --- | --- |
| Android (arm64) | `.apk` | [InstaBox](https://instabox.sh/) |
| Windows (x64) | `-setup.exe` installer | InstaBox |
| Linux (amd64) | `.deb`, `.pkg.tar.gz`, `.AppImage` | InstaBox |
| Linux (arm64) | `.deb`, `.pkg.tar.gz`, `.AppImage` | InstaBox |
| macOS (arm64, x64) | `.dmg` | GitHub's `macos-latest` runner: macOS packaging needs Xcode, which InstaBox's Linux machines do not have |

To release: set `version` in `nsgo.yaml`, then publish a release tagged `v<version>` (version `0.1.0` is tag `v0.1.0`); the workflow stops if they differ. The packages are not signed with a store or developer certificate: the APK carries the debug key, and macOS and Windows may warn on first start.

Setup, once: install the [instabox GitHub App](https://instabox.sh/docs/use-cases/ci) for this repository (the dashboard's CI page, *Connect GitHub*); jobs with `runs-on: instabox-medium` wait in the queue until it is.

## Roadmap

Planned work, with a feasibility assessment of each item, is in [ROADMAP.md](ROADMAP.md) (in Chinese).

## Acknowledgements

- [NSGo](https://nsgo.dev/) is the Go UI engine this app is built on: one Go codebase, drawn with each platform's native views, with its tooling for testing, packaging and secure storage. Thank you to the NSGo authors.
- [OpenList](https://github.com/OpenListTeam/OpenList) is the file list program this client talks to, and the OpenList logo in `design/icon/` comes from that project and belongs to it. Thank you to the OpenList team and its contributors.

This is an unofficial client and is not affiliated with or endorsed by the OpenList project.
