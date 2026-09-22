# furoshiki

静的なウェブサイト(`index.html` とそこからリンクされた HTML、CSS、JavaScript、画像、
フォントなど)を **1 つの HTML ファイル** に包む Go 製のコマンドラインツールです。

出力されたファイルはネットワークもサーバーも不要で、ブラウザで開くだけで
元のサイトと同じようにページ間を移動できます。資料の配布、アーカイブ、
メール添付、オフライン閲覧などに使えます。

```
furoshiki ./site            # ./site.html ができる
```

## 特徴

- `index.html` から **リンクで到達できるページだけ** を集めます(到達できないファイルは含めません)
- ページ間のリンク、戻る / 進む、ページ内アンカー、ブックマーク(`site.html#/docs/guide.html`)が動きます
- CSS(`@import`、`url()` を含む)、JavaScript、画像、`srcset`、フォント、favicon、
  `<video>` / `<audio>`、`<iframe>` で埋め込まれたページ、ダウンロード用ファイル(PDF など)を同梱します
- 同じ内容のファイルは 1 回だけ格納します(共有 CSS / JS / ロゴが複製されません)
- Shift_JIS / EUC-JP など UTF-8 以外のページも UTF-8 に変換して取り込みます
- 各ページは独立した文書として表示されるため、ページごとの CSS / JavaScript が互いに干渉しません
- 出力は決定的です(同じ入力からは同じバイト列が生成されます)
- 依存ライブラリは最小限で、単一バイナリとして配布できます

## インストール

```
go install github.com/ukaji3/furoshiki/cmd/furoshiki@latest
```

またはリポジトリを clone して:

```
make build          # ./furoshiki
make install        # $(go env GOBIN) にインストール
```

Go 1.27 以降が必要です。

## 使い方

```
furoshiki [options] <site-dir>
```

| オプション | 説明 |
|---|---|
| `-o`, `--output PATH` | 出力ファイル。既定はカレントディレクトリの `<site-dir 名>.html`。`-` で標準出力 |
| `-e`, `--entry PATH` | 開始ページ(`<site-dir>` からの相対パス)。既定 `index.html` |
| `--exclude GLOB` | 同梱しないパスのパターン。複数指定可。`/` を含まないパターンはパスの各セグメントにも一致するので、`drafts` は `drafts/` 以下全体を除外します |
| `--single-page` | 開始ページだけを同梱し、他ページへのリンクはそのまま残します |
| `--max-resource-size SIZE` | 同梱するファイルの上限(`K` / `M` / `G` 接尾辞可)。超えたファイルは元の参照のまま残し、警告します。`0` で無制限(既定) |
| `--charset LABEL` | 文字コード宣言のない文書に仮定する文字コード(既定 `utf-8`)。例: `shift_jis` |
| `--strict` | 警告があれば終了コード 1 にします |
| `-q`, `--quiet` | 警告と要約を表示しません(エラーは表示します) |
| `-v`, `--verbose` | 情報レベルの注記と同梱ページ一覧も表示します |
| `--version` | バージョンを表示します |

終了コード: `0` 成功 / `1` 生成失敗、または `--strict` で警告あり / `2` コマンドラインの誤り

### 例

```
# docs/ 以下のサイトを docs.html に。下書きとバックアップは除外
furoshiki -o docs.html --exclude drafts --exclude '*.bak' ./docs

# 開始ページを指定し、警告があれば失敗にする(CI 向け)
furoshiki --strict -e start.html -o out/site.html ./site

# 5 MiB を超える動画などは同梱しない
furoshiki --max-resource-size 5M ./site

# 古い Shift_JIS のサイト(宣言なし)
furoshiki --charset shift_jis ./oldsite
```

出力されたファイルは `file://` から開けます。特定のページを開くには
`site.html#/docs/guide.html` のようにハッシュを付けます。

## 参照の解決規則

`<site-dir>` をサイトのルート(`/`)として扱います。

- `css/site.css`、`../img/logo.png` のような相対パスは、参照している文書の位置から解決します
- `/css/site.css` のようなルート絶対パスは `<site-dir>` から解決します
- `docs/` のようなディレクトリへのリンクは `docs/index.html` になります
- `<base href>` があればそれを尊重します(外部 URL の `<base>` は警告し、相対参照は同梱しません)
- `<site-dir>` の外(`../secret.html`)、存在しないファイル、`--exclude` に一致するファイルは
  **そのまま残して** 警告(または情報)を出します
- `http://` / `https://` / `//cdn...` などの外部 URL はそのまま残します(取得しません)
- HTML 以外へのリンク(PDF、zip など)は同梱し、`download` 属性を付けます

## 仕組み

出力ファイルは小さな「シェル」文書です。全ページの HTML と全リソースを JSON として内包し、
URL のハッシュ(`#/about.html`)で指定されたページを `<iframe srcdoc>` に表示します。
ページ間のリンクはバンドル時にハッシュルートへ書き換えられ、ページ内のスクリプトが
実行時に生成したリンクも、各ページに注入される小さなシムが捕まえてルーティングします。

リソースは内容のハッシュで重複排除され、文書内では `furoshiki-res:<hash>` という
プレースホルダで参照されます。表示時にランタイムがプレースホルダを `data:` URL に
展開します。詳しくは以下の設計記録を参照してください。

- [ADR 0001: 出力を「シェル文書 + iframe srcdoc + ハッシュルータ」で構成する](docs/adr/0001-single-file-shell-architecture.md)
- [ADR 0002: リソースは重複排除したストアに置き、文書内にはプレースホルダを書く](docs/adr/0002-resource-dedup.md)

## 対応している参照

| 要素 / 場所 | 処理 |
|---|---|
| `<a href>`, `<area href>`, SVG `<a>` | 同梱ページ → ハッシュルート。ファイル → 同梱 + `download`。外部 → そのまま(トップウィンドウで開く) |
| `<link rel=stylesheet>`(`media`、`alternate`、`disabled` を保持) | CSS を同梱。中の `url()` / `@import` / `image-set()` も再帰的に解決 |
| `<style>`、`style="..."` 属性 | 中の `url()` / `@import` を解決 |
| `<script src>`(`defer` / `async` / `type=module` を保持) | JavaScript を同梱 |
| `<img src/srcset>`, `<picture><source>`, `<video src/poster>`, `<audio>`, `<track>`, `<embed>`, `<object data>`, `<input type=image>`, `background` 属性 | 同梱 |
| `<link rel=icon / apple-touch-icon / preload / modulepreload>` | 同梱 |
| `<link rel=prefetch / prerender / manifest>` | 削除(情報を出力) |
| `<link rel=next / prev / alternate / canonical ...>` | 同梱ページならハッシュルートへ |
| `<iframe src>`, `<frame src>` | 同梱ページを `srcdoc` として埋め込み(循環は検出) |
| SVG `<image href>`, `<use href="sprite.svg#id">` | 画像は同梱。スプライトは文書内に 1 回インラインして `#id` 参照に |
| `<meta http-equiv=refresh>` | 同梱ページ / 外部 URL への遷移スクリプトに変換 |
| `<meta charset>`, `<meta http-equiv=Content-Type>` | UTF-8 に統一 |
| `<meta http-equiv=Content-Security-Policy>` | 削除して警告(同梱リソースをブロックしうるため) |
| `<base href>` | 解決に使ったうえで削除。`<base target="_top">` を注入 |

## 制限(v1)

- **JavaScript が必須** です。無効な場合は説明文だけが表示されます。
- ページのスクリプトが行う `fetch()` / `XMLHttpRequest` による同一フォルダ内ファイルの取得、
  `new Worker('x.js')`、`location.href = 'x.html'`、`window.open('x.html')`、
  `<form action="x.html">` による遷移は動きません(フォームは警告を出します)。
- ES モジュールの `import './lib.js'` のような **相対指定子は解決されません**
  (`<script type=module src>` 自体は同梱され、警告を出します)。
- `location.pathname` や `document.currentScript.src` に依存するスクリプトは、
  値が元と異なります(前者はシェルのパス、後者は `data:` URL)。
- 外部(`http(s)://`)リソースは取得・同梱しません。表示時にネットワークが必要です。
- 印刷は表示中のページが対象です。`Ctrl+P` / `Cmd+P` はページ側の印刷に振り向けられ、
  ブラウザメニューからの印刷時は iframe をページ全体の高さに広げます。
- ブラウザのページ内検索は表示中のページだけを対象にします。
- 非常に大きなサイト(数百 MB)は、ブラウザのメモリ制約により開けないことがあります。
  `--max-resource-size` で動画などを外してください。

## 開発

```
make test        # ユニット・統合テスト(-race)
make e2e         # ブラウザテスト(Chrome / Chromium が必要。FUROSHIKI_CHROME で実行ファイルを指定可)
make lint        # golangci-lint(gofmt / goimports を含む)
make vet vuln sec
make check       # CI と同じ一式(e2e を除く)
```

ツールは `go run <module>@<version>` で実行するため、グローバルへのインストールは不要です。
ブラウザテストは snap 版 Chromium では `file://` の読み込みに失敗することがあります。
その場合は `FUROSHIKI_CHROME=/usr/bin/google-chrome` のように非 snap のブラウザを指定してください。

### ディレクトリ構成

```
cmd/furoshiki      CLI
internal/bundle    クロールと出力(原子的な書き込み)
internal/page      HTML 文書の変換
internal/css       CSS 内の url() / @import の書き換え(tdewolff/parse)
internal/resolve   ルート限定の URL 解決・分類・MIME 判定
internal/store     リソースの重複排除とプレースホルダ
internal/shell     シェル文書・ランタイム JS・ページ用シム(embed)
internal/textenc   文字コードの判定と UTF-8 への変換
internal/diag      警告・情報の収集
e2e                chromedp によるブラウザテスト(-tags e2e)
testdata/site1     テスト用サイト
docs/adr           設計記録
```

## ライセンス

MIT License。詳細は [LICENSE](LICENSE) を参照してください。
