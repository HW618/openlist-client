# OpenDesign 原型环境说明

本目录用于在仓库内保存可直接复用的 **OpenDesign** 项目快照与配置元数据，以便后续通过 OpenDesign（例如搭配 OpenDesign Cloud 或 Local Codex）继续迭代交互原型时，不必重新搭建基线环境。

## 当前项目信息

- **项目 ID**：`openlist-client-v0.1.0`
- **项目名称**：`OpenList Client v0.1.0`
- **设计系统**：`apple`
- **入口文件**：`index.html`
- **导出原型路径**：[`design/prototype/index-v0.1.0.html`](../prototype/index-v0.1.0.html)

## 目录内容（`design/opendesign/openlist-client-v0.1.0/`）

- `project.yaml`：项目 ID、名称、设计系统与导出路径映射。
- `index.html`：OpenDesign 项目内的主设计稿文件，与 `design/prototype/index-v0.1.0.html` 保持一致。
- `index.html.artifact.json`：OpenDesign 的成品元数据文件。
- `.od-frames/`：OpenDesign 设备外框模板与布局样式（`iphone.html`、`android.html`、`neutral.html`、`layout.css`、`deck-framework.md`、`.od-next-device-frames.json`）。

## 后续复用方式

1. **本机直接复用**：当前本机 OpenDesign 工作区已保留活跃项目 `openlist-client-v0.1.0`，后续修改原型时可直接选中该项目继续迭代。
2. **换机或重建环境**：若在其他机器或全新 OpenDesign 环境中没有该项目，可按 `project.yaml` 创建 `id` 为 `openlist-client-v0.1.0`、设计系统为 `apple` 的项目，并将本目录下的 `index.html`（及需要时的 `index.html.artifact.json`、`.od-frames/`）写入新项目目录作为基线。
3. **同步要求**：每次通过 OpenDesign 调整完原型后，请同时更新 `design/opendesign/openlist-client-v0.1.0/index.html` 与 `design/prototype/index-v0.1.0.html`；不要把 `.file-versions/` 历史快照、本机绝对路径或真实令牌提交进仓库。
