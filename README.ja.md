[English](README.md) | [简体中文](README.zh-CN.md) | **日本語**


<p align="center">
  <img src=".github/images/castor.svg" alt="Castor" width="200"/>
</p>

<p align="center">
  <a href="https://trendshift.io/repositories/86848?utm_source=trendshift-badge&amp;utm_medium=badge&amp;utm_campaign=badge-trendshift-86848" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/trendshift/repositories/86848/daily?language=Go" alt="stupside%2Fcastor | Trendshift" width="250" height="55"/></a>
</p>

<p align="center">
  <a href="https://github.com/stupside/castor/releases/latest">
    <img src="https://img.shields.io/github/v/release/stupside/castor?style=flat-square" alt="最新リリース">
  </a>
  <a href="https://pkg.go.dev/github.com/stupside/castor">
    <img src="https://img.shields.io/badge/Go-Reference-00ADD8?style=flat-square&logo=go" alt="Go リファレンス">
  </a>
  <a href="https://github.com/stupside/homebrew-tap/blob/main/Casks/castor.rb">
    <img src="https://img.shields.io/badge/Homebrew-Available-FBB040?style=flat-square&logo=homebrew" alt="Homebrew">
  </a>
  <a href="https://github.com/stupside/castor/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/stupside/castor?style=flat-square" alt="ライセンス">
  </a>
  <a href="https://github.com/stupside/castor/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/stupside/castor/continuous-integration.yml?style=flat-square" alt="ビルド状態">
  </a>
</p>

# Castor

スマートテレビは任意のウェブ動画をキャストできず、画面ミラーリングには遅延があり、解像度も低下します。Castor は代わりに、実際のストリームを最高品質のままターミナルからキャストします。

視聴中のウェブページまたはストリームの直接 URL を Castor に指定すると、動画を見つけ、ストリームを抽出し、テレビ向けにトランスコードしてリアルタイムでキャストします。自分で設定したソースを使って IMDB/TMDB ID を解決し、自動生成した字幕を映像に焼き込むこともできます。

抽出時にはヘッドレス Chrome を起動し、Chrome DevTools Protocol 経由でネットワークトラフィックを監視します。その後、再生を始めるための短いアクションパイプラインを実行します。ページをクリックし、最大の iframe に移動し、フォールバックとしてもう一度クリックします。自動再生を許可するページで動作しますが、すべてのページで動作するわけではありません。

*汎用キャストツールです。指定したものだけをキャストします。[用途と免責事項](#用途と免責事項)を参照してください。*

<p align="center">
  <img src=".github/images/screen-selection.png" alt="Castor TUI でタイトルを閲覧" width="640"/>
  <br/>
  <sub><em><code>castor cast</code> を実行すれば、ターミナルを離れずにタイトルを閲覧してキャストできます。</em></sub>
</p>


## インストール

Castor は**ネイティブバイナリ**として実行してください（推奨）。自分のマシン上で動作するため、デバイス検出に必要なテレビと同じネットワークを共有できます。このバイナリは、`PATH` に存在する必要がある三つのツールを呼び出します。

| ツール | バージョン | 用途 |
| --- | --- | --- |
| **Chrome / Chromium** | 最近の任意のバージョン | ヘッドレスでのストリーム抽出 |
| **ffmpeg** | 7.1+ | トランスコード（およびストリームコピー） |
| **ffprobe** | 7.1+ | ソース形式の検出 |

ffmpeg と ffprobe は **7.1 以降**が必要です。Castor が使用するフラグ（`-readrate_initial_burst`、より厳密な HLS 拡張子処理）は古いビルドでは拒否されます。[Docker イメージ](#docker任意)には適切なビルドが含まれています（Linux のみ）。

### Homebrew macOS

```sh
brew install --cask stupside/tap/castor
```

<details>
<summary><b>ソースからビルド</b>（Go 1.26+ と cmake が必要）</summary>

whisper.cpp バインディングは cgo を使用し、ローカルでビルドした `libwhisper.a` にリンクするため、サブモジュールを含めてクローンし、`make` でビルドします。

```sh
git clone --recurse-submodules https://github.com/stupside/castor.git
cd castor
make          # builds libwhisper.a, then the castor binary
```

`go install` は使用できません。ベンダー化された whisper.cpp バインディングはローカルの `replace` 経由で取り込まれ、事前にビルドした静的ライブラリが必要です。

</details>


## クイックスタート

使用するテレビを Castor に指定します。`castor scan` で名前を調べ、`config.yaml` に記述してください。

```yaml
device:
  name: "Living Room TV"   # exact name from `castor scan`
  type: dlna
```

これで、視聴中のページまたは手元のストリーム URL をキャストできます。

```sh
castor cast player https://example.com/watch/some-video
```

タイトルを検索してキャストするインタラクティブブラウザーを利用するには、TMDB キーとソースを追加し（[設定](#設定)を参照）、`castor cast` を実行します。

<p align="center">
  <img src=".github/images/screen-devices.png" alt="Castor TUI でキャスト先を選択" width="640"/>
</p>

よく使うコマンド（すべてのフラグは `castor --help` で確認できます）：

| コマンド | 動作 |
| --- | --- |
| `castor scan` | ネットワーク上のキャスト先を一覧表示 |
| `castor cast` | タイトルを閲覧し、対話形式でキャスト（TMDB キーが必要） |
| `castor cast player <url>` | 埋め込み動画プレーヤーがあるウェブページをキャスト |
| `castor cast url <url>` | ストリームまたは動画の直接 URL をキャスト |
| `castor cast movie <id>` | ソースを使って映画 ID を解決し、キャスト |
| `castor cast episode <id> --season N --episode N` | テレビ番組のエピソードを解決し、キャスト |


## 設定

Castor は作業ディレクトリの `config.yaml` を読み込みます（または `--config <path>`）。**必須キーは[クイックスタート](#クイックスタート)に示した `device` だけです**。その他すべて（タイムアウト、プローブ、キャプチャ、トランスコード、ネットワークインターフェイス、Chrome の検出）には実用的なデフォルト値があります。

> [!TIP]
> TMDB キーなどの秘密情報をコミット対象ファイルに入れないでください。git から無視された `config.local.yaml`（`config.yaml` に重ねて適用されます）、または `CASTOR_SECTION__FIELD` 環境変数に保存してください。[SECURITY.md](SECURITY.md) を参照してください。

以下はすべて任意です。

<details>
<summary><b>ソース</b>：設定したサイトから映画 / エピソード ID を解決</summary>

`cast movie`、`cast episode`、インタラクティブブラウザーは、設定したソースを使ってタイトル ID を解決します。Castor にはソースが含まれないため、自分のもの（利用権限があるサイト）を追加してください。カタログも検索機能もありません。Castor は ID を作成した `templates` に代入し、それぞれの `proxies` を先頭に付け、生成されたページを開いて、`cast player` とまったく同じ方法でストリームを抽出します。たとえば、`castor cast movie tt12300742` は `https://your-source.example/embed/movie/tt12300742` を開きます。

```yaml
sources:
  - proxies: ["https://your-source.example"]   # base URLs, tried in order
    templates:
      movie: "/embed/movie/{itemID}"
      episode: "/embed/tv/{itemID}/{season}-{episode}"
```

</details>

<details>
<summary><b>リゾルバー</b>：<code>max_height</code> で解像度を制限</summary>

ソース選択とストリームのプローブを行います。すべてのオプションには適切なデフォルト値があります。通常は `max_height` だけをテレビの垂直解像度に設定し、選択するストリームとエンコーダー出力の両方を制限します。

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
<summary><b>TMDB</b>：インタラクティブブラウザー用 API キー</summary>

インタラクティブブラウザー（`castor cast`）は TMDB API キーを使ってタイトルを検索します。`cast movie <id>` のような直接コマンドには不要です。[themoviedb.org](https://www.themoviedb.org/settings/api) から無料キーを取得し、`config.local.yaml` に保存してください。

```yaml
tmdb:
  api_key: "<KEY>"
```

</details>

<details>
<summary><b>字幕</b>：whisper で自動文字起こしして焼き込み（デフォルトはオフ）</summary>

whisper で文字起こしし、動画に焼き込む自動生成字幕：

```yaml
whisper:
  enable: true             # off by default
  # language: "fr"         # default: English
  # model_path: ""         # default: ggml-tiny.en (~75 MB, auto-downloaded)
```

</details>


## 対応デバイス

`castor scan` を実行して、ネットワーク上のデバイスを一覧表示します。

| プロトコル | 対応機器 | 状態 |
| --- | --- | --- |
| **DLNA / UPnP**（`MediaRenderer:1`） | 過去 10 年ほどのほぼすべてのスマートテレビ（Samsung、LG、Sony Bravia、Panasonic Viera、Philips、Hisense、TCL、VIZIO、Sharp）と、Kodi、VLC、Plex などのネットワークプレーヤー | Samsung でテスト済み |
| **Chromecast** | Google Cast デバイス | 実験的、未テスト（コントリビューション歓迎） |


## Docker（任意）

ビルド済みの `ghcr.io/stupside/castor` イメージには Chrome、ffmpeg、ffprobe が含まれるため、手動インストールは不要です。**テレビと同じ LAN 上の Linux ホスト**で実行してください。

> [!WARNING]
> `--network host` が必要です。デバイス検出（SSDP マルチキャスト）と、テレビが Castor からストリームを受信する処理のどちらも、コンテナが実際の LAN 上にある必要があります。Docker Desktop（macOS/Windows）ではこのフラグは効果がなく、コンテナはテレビに到達できず、`scan` は何も検出しません。代わりに[ネイティブバイナリ](#homebrew-macos)を使用してください。

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

`--device /dev/dri` は、VA-API ハードウェア H.264 エンコードのために Intel GPU をコンテナへ渡します。指定しない場合（または Intel 以外のホストの場合）、Castor はソフトウェア `libx264` にフォールバックします。どちらの場合でも、テレビがソース動画をそのまま受け入れられるときは、Castor がストリームをコピーしてエンコードを完全に省略します。

コマンドは [`config.yaml`](config.yaml) があるディレクトリから実行します（`/config.yaml` にマウントされます）。`castor-cache` ボリュームは、自動ダウンロードされた whisper モデルを永続化します。

| タグ | ビルド |
| --- | --- |
| `:latest` | 最新の安定版 |
| `:canary` | 最新のプレビュービルド |
| `:v1.7.0` | 特定の固定バージョン |


## 用途と免責事項

Castor は汎用キャストツールであり、特定のサイトに結び付いたサービスではありません。

- **何もホストしません。** 動画、カタログ、ソースは含まれません。Castor は Chromecast と同様に、利用権限があり、自分で指定したページ、ストリーム URL、ソースだけをキャストします。
- **DRM には触れません。** Castor は DRM を復号または回避せず、DRM で保護されたサービスをキャストできません。
- **合法的に使用する責任は利用者にあります。** Castor の使い方がサイトの利用規約や地域の法律で許可されるかどうかは、利用者の責任です。著作権侵害に使用しないでください。

Castor は、合法的な個人利用および教育目的に限り、現状のまま提供されます。


## コントリビューション

[CONTRIBUTING.md](CONTRIBUTING.md) を参照してください。
