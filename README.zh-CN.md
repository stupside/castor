[English](README.md) | **简体中文** | [日本語](README.ja.md)


<p align="center">
  <img src=".github/images/castor.svg" alt="Castor" width="200"/>
</p>

<p align="center">
  <a href="https://trendshift.io/repositories/86848?utm_source=trendshift-badge&amp;utm_medium=badge&amp;utm_campaign=badge-trendshift-86848" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/trendshift/repositories/86848/daily?language=Go" alt="stupside%2Fcastor | Trendshift" width="250" height="55"/></a>
</p>

<p align="center">
  <a href="https://github.com/stupside/castor/releases/latest">
    <img src="https://img.shields.io/github/v/release/stupside/castor?style=flat-square" alt="最新版本">
  </a>
  <a href="https://pkg.go.dev/github.com/stupside/castor">
    <img src="https://img.shields.io/badge/Go-Reference-00ADD8?style=flat-square&logo=go" alt="Go 参考">
  </a>
  <a href="https://github.com/stupside/homebrew-tap/blob/main/Casks/castor.rb">
    <img src="https://img.shields.io/badge/Homebrew-Available-FBB040?style=flat-square&logo=homebrew" alt="Homebrew">
  </a>
  <a href="https://github.com/stupside/castor/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/stupside/castor?style=flat-square" alt="许可证">
  </a>
  <a href="https://github.com/stupside/castor/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/continuous-integration.yml?style=flat-square" alt="构建状态">
  </a>
</p>

# Castor

智能电视无法投放任意网页视频，而屏幕镜像不仅延迟高，还会降低分辨率。Castor 会从终端直接投放真实媒体流，并保持完整画质。

让 Castor 处理你正在观看的网页或一个直接的媒体流 URL，它就会找到视频、提取媒体流、为电视转码，并实时投放。它还可以使用你自行配置的来源解析 IMDB/TMDB ID，并将自动生成的字幕烧录到视频中。

为了提取媒体流，它会启动无头 Chrome，通过 Chrome DevTools Protocol 监视网络流量，然后运行一个简短的操作流水线以开始播放：点击页面、进入最大的 iframe，并在必要时再次点击。这适用于允许自动播放的页面，但无法在所有页面上生效。

*一款通用投放工具：它只投放你指向的内容。请参阅[用途和免责声明](#用途和免责声明)。*

<p align="center">
  <img src=".github/images/screen-selection.png" alt="在 Castor TUI 中浏览标题" width="640"/>
  <br/>
  <sub><em>运行 <code>castor cast</code> 即可浏览并投放标题，全程无需离开终端。</em></sub>
</p>


## 安装

以**原生二进制文件**方式运行 Castor（推荐）：它在你的计算机上运行，因此与电视共享设备发现所需的网络。该二进制文件会调用三个必须位于 `PATH` 中的工具：

| 工具 | 版本 | 用途 |
| --- | --- | --- |
| **Chrome / Chromium** | 任意较新版本 | 无头媒体流提取 |
| **ffmpeg** | 7.1+ | 转码（以及媒体流复制） |
| **ffprobe** | 7.1+ | 源格式检测 |

ffmpeg 和 ffprobe 必须为 **7.1 或更高版本**：Castor 使用的参数（`-readrate_initial_burst`、更严格的 HLS 扩展名处理）会被旧版本拒绝。[Docker 镜像](#docker可选)内置了合适的版本（仅限 Linux）。

### Homebrew macOS

```sh
brew install --cask stupside/tap/castor
```

<details>
<summary><b>从源代码构建</b>（需要 Go 1.26+ 和 cmake）</summary>

whisper.cpp 绑定使用 cgo，并链接到本地构建的 `libwhisper.a`，因此请连同子模块一起克隆，并使用 `make` 构建：

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make          # builds libwhisper.a, then the castor binary
```

`go install` 无法使用：以 vendored 方式提供的 whisper.cpp 绑定通过本地 `replace` 引入，并需要预先构建的静态库。

</details>


## 快速开始

告诉 Castor 要使用哪台电视：使用 `castor scan` 查找电视名称，并将其写入 `config.yaml`。

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna
```

现在即可投放你正在观看的网页，或已有的媒体流 URL：

```sh
castor cast player https://example.com/watch/some-video
```

若要使用能够搜索标题并代你投放的交互式浏览器，请添加 TMDB 密钥和一个来源（参阅[配置](#配置)），然后运行 `castor cast`：

<p align="center">
  <img src=".github/images/screen-devices.png" alt="在 Castor TUI 中选择投放目标" width="640"/>
</p>

最常用的命令（运行 `castor --help` 可查看所有参数）：

| 命令 | 功能 |
| --- | --- |
| `castor scan` | 列出网络上的投放目标 |
| `castor cast` | 以交互方式浏览标题并投放（需要 TMDB 密钥） |
| `castor cast player <url>` | 投放包含嵌入式视频播放器的网页 |
| `castor cast url <url>` | 投放直接的媒体流或视频 URL |
| `castor cast movie <id>` | 使用你的来源解析电影 ID 并投放 |
| `castor cast episode <id> --season N --episode N` | 解析电视剧集并投放 |


## 配置

Castor 会从工作目录读取 `config.yaml`（也可使用 `--config <path>`）。**唯一必需的键是[快速开始](#快速开始)中所示的 `device`**；其他所有项目（超时、探测、捕获、转码、网络接口、Chrome 发现）都有可用的默认值。

> [!TIP]
> 不要将 TMDB 密钥等机密信息写入已提交的文件：请放入被 git 忽略的 `config.local.yaml`（该文件会叠加在 `config.yaml` 上），或使用 `CASTOR_SECTION__FIELD` 环境变量。请参阅 [SECURITY.md](SECURITY.md)。

以下所有内容均为可选项。

<details>
<summary><b>来源</b>：使用你配置的网站解析电影 / 剧集 ID</summary>

`cast movie`、`cast episode` 和交互式浏览器会使用你配置的来源解析标题 ID。Castor 不附带任何来源，因此请添加你自己的来源（你有权使用的网站）。它没有目录或查询功能：Castor 会将 ID 代入你编写的 `templates`，在每个结果前添加你的 `proxies`，打开生成的页面，再以与 `cast player` 完全相同的方式提取媒体流。例如，`castor cast movie tt12300742` 会打开 `https://your-source.example/embed/movie/tt12300742`。

```yaml
sources:
  - proxies: ["https://your-source.example"]   # base URLs, tried in order
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

</details>

<details>
<summary><b>解析器</b>：使用 <code>max_height</code> 限制分辨率</summary>

用于来源选择和媒体流探测。所有选项都有合理的默认值；通常只需将 `max_height` 设置为电视的垂直分辨率，它会同时限制所选媒体流和编码器输出。

```yaml
resolver:
  # The tallest video to cast. Source selection prefers the largest stream no
  # taller than this, and the encoder scales its output down to it. Defaults to
  # 1080; raise it to your TV's native height (e.g. 2160 for a 4K panel) to pass
  # 4K through, or lower it to save bandwidth.
  max_height: 2160
  # hls_timeout: 30s
  # probe_timeout: 30s
  # probe_max_concurrency: 2
  # ffprobe_path: ffprobe
```

</details>

<details>
<summary><b>TMDB</b>：交互式浏览器的 API 密钥</summary>

交互式浏览器（`castor cast`）使用 TMDB API 密钥搜索标题；`cast movie <id>` 等直接命令不需要该密钥。请从 [themoviedb.org](https://www.themoviedb.org/settings/api) 获取免费密钥，并将其保存在 `config.local.yaml` 中：

```yaml
tmdb:
  api_key: "<KEY>"
```

</details>

<details>
<summary><b>字幕</b>：使用 whisper 自动转录并烧录（默认关闭）</summary>

使用 whisper 转录并烧录到视频中的自动生成字幕：

```yaml
whisper:
  enable: true             # off by default
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)
```

</details>


## 支持的设备

运行 `castor scan` 即可列出网络中的设备。

| 协议 | 适用设备 | 状态 |
| --- | --- | --- |
| **DLNA / UPnP**（`MediaRenderer:1`） | 过去十年中几乎所有智能电视（Samsung、LG、Sony Bravia、Panasonic Viera、Philips、Hisense、TCL、VIZIO、Sharp），以及 Kodi、VLC 和 Plex 等联网播放器 | 已在 Samsung 上测试 |
| **Chromecast** | Google Cast 设备 | 实验性功能，未经测试（欢迎贡献） |


## Docker（可选）

预构建的 `ghcr.io/stupside/castor` 镜像包含 Chrome、ffmpeg 和 ffprobe，因此无需手动安装任何内容。请在**与电视位于同一 LAN（局域网）的 Linux 主机**上运行。

> [!WARNING]
> 必须使用 `--network host`：设备发现（SSDP 组播）和电视从 Castor 获取流媒体都要求容器接入真实 LAN。在 Docker Desktop（macOS/Windows）上，该参数不起作用，因此容器无法访问电视，`scan` 也找不到任何设备。请改用[原生二进制文件](#homebrew-macos)。

```sh
# Discover devices (no config needed)
docker run --rm --network host ghcr.io/stupside/castor:latest scan

# Cast, passing the Intel GPU through for hardware transcoding
docker run --rm --network host --device /dev/dri \
  -v "$PWD/config.yaml:/config.yaml" \
  -v castor-cache:/root/.cache \
  ghcr.io/stupside/castor:latest \
  cast player https://example.com/watch/some-video
```

`--device /dev/dri` 会将 Intel GPU 传递给容器，以便通过 VA-API 进行硬件 H.264 编码；如果不使用该参数（或在非 Intel 主机上），Castor 会回退到软件 `libx264`。无论哪种方式，当电视已经接受源视频格式时，Castor 都会直接复制媒体流并完全跳过编码。

请从存放 [`config.yaml`](config.yaml) 的目录运行命令（该文件会挂载到 `/config.yaml`）。`castor-cache` 卷会持久保存自动下载的 whisper 模型。

| 标签 | 构建 |
| --- | --- |
| `:latest` | 最新稳定版本 |
| `:canary` | 最新预览版本 |
| `:v1.7.0` | 指定的固定版本 |


## 用途和免责声明

Castor 是一款通用投放工具，不与任何特定网站绑定。

- **它不托管任何内容。** 不附带视频、目录或来源：Castor 只投放你提供并有权使用的页面、媒体流 URL 或来源，与 Chromecast 类似。
- **它不会处理 DRM。** Castor 不会解密或绕过 DRM，也无法投放受 DRM 保护的服务。
- **你有责任合法使用。** 你的使用行为是否符合网站使用条款和当地法律由你负责。请勿使用它侵犯版权。

Castor 按原样提供，仅供合法、个人和教育用途。


## 参与贡献

请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)。
