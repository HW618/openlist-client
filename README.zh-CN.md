# OpenList Client

[English](README.md) | **简体中文**

[OpenList](https://github.com/OpenListTeam/OpenList) 的轻量级跨平台客户端，使用 Go 和 [NSGo](https://nsgo.dev/) 编写。界面由各平台自己的原生视图绘制，支持浅色和深色外观，支持简体中文和英文。

> 状态：早期版本（v0.2.0）。`go tool nsgo build` 可打包 macOS（arm64）、Windows（x64）、iOS 模拟器、Android（arm64）和网页版；代码同时支持 Linux。

## 功能

- 多服务器管理，每个服务器有自己的文件夹和下载设置；顶栏站点切换旁及实例切换面板顶部提供快捷 `+ 添加站点` 入口
- 目录浏览：面包屑导航、返回上一级目录按钮、排序、列表和网格视图
- 搜索：在当前文件夹下搜索，或搜索整个服务器
- 多选，批量移动和删除；悬浮菜单用于新建文件夹和文件
- 上传和下载队列（全部下载与上传均在 Go 传输引擎中运行，支持 HTTP `Range` 断点续传与失败/停滞自动重试；进行中的任务在 Android 上通过 OpenList 自己的单条静音进度通知展示，iOS 使用灵动岛实况活动，桌面端则在图标上显示总进度并提供系统托盘状态菜单、关闭窗口最小化到托盘继续传输及活跃传输退出确认；「传输」页顶部展示可点击关闭的传输汇总/进度卡片）
- 离线下载及其进度
- 传输或离线任务完成/失败时的系统通知（设置里可开关，条目过多时自动折叠汇总；首次启动会引导开启权限）
- 文件预览：图片、视频、音频、文本
- 权限检查与修复引导

## 登录与登录信息的保存位置

登录状态在重启后保持，只有在「设置」里对该服务器点击**登出**时才会删除。访问令牌通过 NSGo 保存在平台的安全存储中，不会写入普通文件：

| 平台 | 存储位置 |
| --- | --- |
| macOS、iOS | 钥匙串（Keychain） |
| Windows | 凭据管理器（Credential Manager） |
| Linux | Secret Service（GNOME Keyring、KWallet 等）：应用提示时请安装一个 |
| Android | AES-GCM 加密，密钥保存在 Android Keystore 中 |
| 网页 | 只在内存中：刷新页面后需要重新登录 |

令牌只会发送给服务器自己的主机；服务器使用 `https` 访问时，不会通过明文 `http` 发送。应用 ID（`nsgo.yaml` 中的 `io.github.hw618.openlistclient`）同时也是安全存储的 service 名；此前使用 `dev.nsgo.openlistclient` 保存的令牌会在启动时复制到新 service（桌面端钥匙串）。安卓上更换包名会变成另一个应用，旧 Keystore 中的令牌无法随之迁移。

## 下载与后台传输

文件默认保存到系统的「下载」文件夹下的 `OpenList` 子文件夹，每个服务器也可以使用自己的文件夹。在浏览器里，文件会交给浏览器下载，或写入你授权访问的文件夹（大文件需要选择这个文件夹）。

在所有原生平台上，下载与上传均由 Go 传输引擎执行（`transfer.FetchResumable` 配合 `.part` 临时文件与 HTTP `Range` 请求），遇到网络断开或连接停滞时会自动重试，并在重试、暂停、后台中断以及应用重启后从已保存的字节偏移量继续（Android 系统保存对话框模式同样保留暂存的 `.part` 进度）。开启通知且有任务进行时，Android 仅在 OpenList 应用自身名下显示一条静音实时进度通知并在完成或失败时发出提醒（不再显示系统下载器或重复的实况卡片，点击可回到「传输」页），iOS 则在锁屏/灵动岛展示实时活动，桌面端通过 Dock/任务栏图标显示总进度，并提供系统托盘图标与菜单（显示实时传输状态、恢复窗口、打开传输队列、退出），默认在设置中开启「关闭窗口时最小化到托盘」并在仍有活跃传输尝试退出时弹出确认框。需要说明的是，由于缺少面向 Go 工作协程的原生后台保活服务，移动端切到后台后系统仍可能限制网络或挂起进程；不过已下载的 `.part` 进度会持续落盘保存，切回应用或恢复网络连接时会自动从断点继续下载。

## 目录结构

| 路径 | 内容 |
| --- | --- |
| `main.go` | 入口：运行 `app.Run` |
| `README.md`、`README.zh-CN.md` | 英文和中文说明，两个文件需要同步更新 |
| `ROADMAP.md` | 后续计划及其可行性评估 |
| `AGENTS.md` | 在本仓库工作的 AI 助手和贡献者需要遵守的规则 |
| `nsgo.yaml` | 应用名称、ID、版本、图标、语言目录和构建目标（NSGo 要求它放在模块根目录） |
| `src/app/` | 应用本体：窗口与导航、状态、文件、传输、设置、弹层、桌面托盘、预览、权限，以及界面测试 |
| `src/internal/openlist/` | OpenList HTTP API 客户端 |
| `src/internal/browse/` | 排序、过滤、搜索爬取和悬浮菜单定位 |
| `src/internal/store/` | 站点、设置和令牌的存放 |
| `src/internal/transfer/` | 上传和下载引擎 |
| `src/internal/access/` | 把失败归类为权限问题 |
| `src/locales/` | `en.yaml` 和 `zh-Hans.yaml` 语言目录；`catalog_gen.go` 为自动生成 |
| `src/theme/` | 主题（`openlist.yaml`）及其加载器 |
| `.github/workflows/` | `ci.yml`（PR/`dev` 检查）和 `release.yml`（发布 release 时构建） |
| `design/prototype/` | 应用的 Open Design HTML 原型 |
| `design/opendesign/` | 可复用的 OpenDesign 项目快照（`openlist-client-v0.2.0`）与元数据 |
| `design/icon/` | 应用图标源文件，以及制作图标所用的 OpenList 标志 |

界面有改动时，请让原型与应用保持一致；原型文件头部写明了每个界面对应哪个源文件。

## 开发

需要 Go 1.27.3 或更新版本（Apple 平台还需要 Xcode）。

```sh
go tool nsgo doctor        # 查看本机能构建和运行哪些目标
go tool nsgo dev           # 热重载运行；加 -target web 在浏览器中运行
go tool nsgo test          # 无界面测试
go tool nsgo check         # 静态检查（vet）
go tool nsgo build         # 按 nsgo.yaml 打包所有目标到 dist/
go tool nsgo build -target macos_arm64   # 只构建一个目标；加 -installers=false 可跳过 dmg
```

请使用 `go tool nsgo` 命令，而不要直接用 `go build` 或 `go test`：它们会做类型检查、生成语言目录，并对所有目标做 vet。

原型是单个文件：用浏览器打开 `design/prototype/index-v0.2.0.html`（参数：`?platform=desktop|ios|android|web`，`?perm=folder,network,server,browser,keychain,notifications`，`?notify=1` 可演示折叠汇总传输通知（仅在传输页显示），`?notify=progress` 可演示实时传输进度通知（仅在传输页显示），`?tray=1` 可演示桌面托盘菜单，`?quit=1` 可演示活跃传输退出确认）。

向 `dev` 的推送和针对它的 PR 会运行 [`.github/workflows/ci.yml`](.github/workflows/ci.yml)：`go tool nsgo test`、`go tool nsgo check`，以及对 Windows、js/wasm、Linux（关闭 cgo）的 `go vet`，与 [AGENTS.md](AGENTS.md) §4.1 一致。

## 发布

在 GitHub 上发布 release 会运行 [`.github/workflows/release.yml`](.github/workflows/release.yml)，构建各平台客户端并添加到该 release：

| 客户端 | 文件 | 构建环境 |
| --- | --- | --- |
| Android（arm64） | `.apk` | [InstaBox](https://instabox.sh/) |
| Windows（x64） | `-setup.exe` 安装包 | InstaBox |
| Linux（amd64） | `.deb`、`.pkg.tar.gz`、`.AppImage` | InstaBox |
| Linux（arm64） | `.deb`、`.pkg.tar.gz`、`.AppImage` | InstaBox |
| macOS（arm64、x64） | `.dmg` | GitHub 的 `macos-latest` 运行器：macOS 打包需要 Xcode，而 InstaBox 的 Linux 机器没有 |

发布步骤：先修改 `nsgo.yaml` 里的 `version`，再发布标签为 `v<版本号>` 的 release（版本 `0.2.0` 对应标签 `v0.2.0`）；两者不一致时工作流会停止。在配好开发者或商店证书之前，macOS 和 Windows 首次启动时可能出现安全提示。

### Android 正式签名（未配置密钥时仍用调试密钥）

在第一个对外版本前，请在本机生成 PKCS#12（或 JKS）密钥库，并在仓库的 Secrets（Settings → Secrets and variables → Actions）中添加下列项。不要把密钥库或密码提交进仓库（`*.p12` / `*.jks` 已在 `.gitignore` 中）。

| Secret | 用途 |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | 密钥库文件的 base64 |
| `NSGO_KEYSTORE_PASSWORD` | 密钥库密码（NSGo 读取此环境变量名） |
| `NSGO_KEY_PASSWORD` | 密钥密码（常与密钥库密码相同） |
| `ANDROID_KEY_ALIAS` | 可选；默认 `openlist` |

仅当设置了 `ANDROID_KEYSTORE_BASE64` 时，`release.yml` 才会把密钥库写到 `.nsgo/` 并传入 `-keystore` / `-key-alias`。

一次性设置：为本仓库安装 [instabox GitHub App](https://instabox.sh/docs/use-cases/ci)（控制台的 CI 页面，点击 *Connect GitHub*）；在安装之前，使用 `runs-on: instabox-medium` 的任务会一直排队。

## 路线图

后续计划及每一项的可行性评估见 [ROADMAP.md](ROADMAP.md)。

## 致谢

- [NSGo](https://nsgo.dev/) 是本应用使用的 Go 界面引擎：一套 Go 代码，用各平台的原生视图绘制，并提供测试、打包和安全存储等工具。感谢 NSGo 的作者们。
- [OpenList](https://github.com/OpenListTeam/OpenList) 是本客户端所连接的文件列表程序；`design/icon/` 中的 OpenList 标志来自该项目，版权归其所有。感谢 OpenList 团队和所有贡献者。

这是一个非官方客户端，与 OpenList 项目没有隶属关系，也未获得其认可。
