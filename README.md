# OpenList Client

**English** | [简体中文](README.zh-CN.md)

A lightweight, cross-platform client for [OpenList](https://github.com/OpenListTeam/OpenList), written in Go with [NSGo](https://nsgo.dev/). It draws with each platform's own native views, in light and dark appearance, with Simplified Chinese and English.

> Status: early (v0.2.0). `go tool nsgo build` packages macOS (arm64), Windows (x64), the iOS simulator, Android (arm64) and the web; the code also supports Linux.

## Features

Several servers (each with its own folders and download settings, plus a quick `+ Add site` button beside the switcher and at the top of the site-switcher panel), browsing with breadcrumbs and a parent-folder back button, sorting, list and grid views, search below the folder or through the whole server, selection with batch move and delete, a floating menu for new folders and files, upload and download queues (all downloads and uploads run in the Go transfer engine with HTTP `Range` `.part` resume and automatic failure/stall retry; Android shows active progress in a single silent OpenList app notification, iOS uses Live Activities, desktops show icon progress bars and a system tray icon with menu and close-to-tray background running plus active-transfer quit confirmation, and the Transfers page shows a dismissible transfer summary/progress notice), offline downloads with their progress, system notifications when transfers or offline tasks finish or fail (with a Settings toggle, collapsing many alerts into a summary; the app asks for permission on first launch), file preview, and permission checks with guidance.

## Signing in and where logins are kept

A login stays across restarts and is only deleted when you press **Sign out** on that server in Settings. The access token is kept in the platform's secure store through NSGo, never in a plain file:

| Platform | Store |
| --- | --- |
| macOS, iOS | Keychain |
| Windows | Credential Manager |
| Linux | Secret Service (GNOME Keyring, KWallet, ...): install one if the app asks |
| Android | AES-GCM with a key in the Android Keystore |
| Web | Memory only: you sign in again after a reload |

Tokens are only sent to the server's own host, and not over plain `http` when the server is reached over `https`. The app ID (`io.github.hw618.openlistclient` in `nsgo.yaml`) is also the secure-store service name; tokens kept under the previous ID `dev.nsgo.openlistclient` are copied into the new service on launch (desktop keychains). Changing the Android package name installs as a separate app, so those tokens cannot move with it.

## Downloads and background transfers

By default files go to the system's Downloads folder, in an `OpenList` subfolder, and each server can use its own folder. In the browser a file is handed to the browser's download, or written to a folder you grant access to (large files need that folder).

On all native targets, downloads and uploads run inside the Go transfer engine (`transfer.FetchResumable` backed by `.part` files and HTTP `Range` requests), automatically retrying transient disconnects and stalled connections and resuming from the saved offset across retries, pauses, background interruptions, and app restarts (including Android save-dialog staging). While transfers are active and notifications are enabled, Android shows a single silent progress notification under OpenList itself (without system downloader or duplicate Live Update notifications) and emits alerts on completion/failure (tapping opens Transfers), iOS displays Live Activities in the Lock Screen/Dynamic Island, and desktop platforms show total progress on the Dock/taskbar icon as well as a system tray icon (with a status/restore/quit menu, close-to-tray background running enabled by default in Settings, and a confirmation dialog before quitting while transfers are active). Note that without a native background execution service for Go workers, mobile operating systems may still throttle or suspend backgrounded transfers; saved `.part` progress is persisted on disk and interrupted downloads resume automatically when returning to the app or regaining connectivity.

## Layout

| Path | What it holds |
| --- | --- |
| `main.go` | The entry point: runs `app.Run` |
| `README.md`, `README.zh-CN.md` | This file and its Chinese version; keep them in step |
| `ROADMAP.md` | Planned work and its feasibility (Chinese) |
| `AGENTS.md` | Rules for AI assistants and contributors working in this repository (Chinese) |
| `nsgo.yaml` | App name, id, version, icons, language catalogs and build targets (NSGo needs it in the module root) |
| `src/app/` | The app: window and navigation, state, Files, Transfers, Settings, sheets, desktop tray, preview, permissions, and its UI tests |
| `src/internal/openlist/` | OpenList HTTP API client |
| `src/internal/browse/` | Sorting, filtering, search crawl and floating-menu placement |
| `src/internal/store/` | Sites, settings and where tokens are kept |
| `src/internal/transfer/` | Upload and download engine |
| `src/internal/access/` | Classifies failures into permission problems |
| `src/locales/` | `en.yaml` and `zh-Hans.yaml` catalogs; `catalog_gen.go` is generated |
| `src/theme/` | The theme (`openlist.yaml`) and its loader |
| `.github/workflows/` | `ci.yml` (PR/`dev` checks) and `release.yml` (builds when a release is published) |
| `design/prototype/` | The Open Design HTML prototype of the app |
| `design/opendesign/` | Reusable OpenDesign project snapshot (`openlist-client-v0.2.0`) and metadata |
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

The prototype is a single file: open `design/prototype/index-v0.2.0.html` in a browser (`?platform=desktop|ios|android|web`, `?perm=folder,network,server,browser,keychain,notifications`, `?notify=1` for a collapsed transfer summary notice shown in the Transfers tab, `?notify=progress` for a demo live transfer progress notice shown in the Transfers tab, `?tray=1` for the desktop tray menu, `?quit=1` for the active-transfer quit dialog).

Pull requests and pushes to `dev` run [`.github/workflows/ci.yml`](.github/workflows/ci.yml): `go tool nsgo test`, `go tool nsgo check`, and `go vet` for Windows, js/wasm, and Linux (no cgo), matching [AGENTS.md](AGENTS.md) §4.1.

## Releases

Publishing a release on GitHub runs [`.github/workflows/release.yml`](.github/workflows/release.yml), which builds the clients and adds them to that release:

| Client | Files | Built on |
| --- | --- | --- |
| Android (arm64) | `.apk` | [InstaBox](https://instabox.sh/) |
| Windows (x64) | `-setup.exe` installer | InstaBox |
| Linux (amd64) | `.deb`, `.pkg.tar.gz`, `.AppImage` | InstaBox |
| Linux (arm64) | `.deb`, `.pkg.tar.gz`, `.AppImage` | InstaBox |
| macOS (arm64, x64) | `.dmg` | GitHub's `macos-latest` runner: macOS packaging needs Xcode, which InstaBox's Linux machines do not have |

To release: set `version` in `nsgo.yaml`, then publish a release tagged `v<version>` (version `0.2.0` is tag `v0.2.0`); the workflow stops if they differ. macOS and Windows may warn on first start until they use a developer or store certificate.

### Android release signing (optional until secrets are set)

Without the secrets below, the Android APK still uses the debug key. Before the first public build, generate a PKCS#12 (or JKS) keystore locally, then add these repository secrets (Settings → Secrets and variables → Actions). Never commit the keystore or passwords (`*.p12` / `*.jks` are gitignored).

| Secret | Purpose |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | Base64 of the keystore file |
| `NSGO_KEYSTORE_PASSWORD` | Keystore password (NSGo reads this env name) |
| `NSGO_KEY_PASSWORD` | Key password (often the same as the keystore) |
| `ANDROID_KEY_ALIAS` | Optional; defaults to `openlist` |

`release.yml` writes the keystore under `.nsgo/` and passes `-keystore` / `-key-alias` only when `ANDROID_KEYSTORE_BASE64` is set.

Setup, once: install the [instabox GitHub App](https://instabox.sh/docs/use-cases/ci) for this repository (the dashboard's CI page, *Connect GitHub*); jobs with `runs-on: instabox-medium` wait in the queue until it is.

## Roadmap

Planned work, with a feasibility assessment of each item, is in [ROADMAP.md](ROADMAP.md) (in Chinese). A Chinese version of this README is in [README.zh-CN.md](README.zh-CN.md).

## Acknowledgements

- [NSGo](https://nsgo.dev/) is the Go UI engine this app is built on: one Go codebase, drawn with each platform's native views, with its tooling for testing, packaging and secure storage. Thank you to the NSGo authors.
- [OpenList](https://github.com/OpenListTeam/OpenList) is the file list program this client talks to, and the OpenList logo in `design/icon/` comes from that project and belongs to it. Thank you to the OpenList team and its contributors.

This is an unofficial client and is not affiliated with or endorsed by the OpenList project.
