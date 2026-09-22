# ADR 0001: 出力を「シェル文書 + iframe srcdoc + ハッシュルータ」で構成する

- 状態: 採択
- 日付: 2026-09-23

## 背景

furoshiki は、`index.html` とそこからリンクで到達できる同一フォルダ内の HTML、
およびそれらが参照する CSS / JavaScript / 画像 / フォント等を **1 つの HTML ファイル**
にまとめる。要件は次の 3 点である。

1. 複数ページ間の遷移(リンク、戻る / 進む、ページ内アンカー)が元のサイトと同じように動くこと
2. 各ページの CSS / JavaScript が互いに干渉せず、元のページ単体で開いたときと同じ挙動をすること
3. 出力はネットワークアクセスなしで、`file://` から開けること

「複数の HTML 文書を 1 ファイルに入れて、それらの間を遷移させる」方法は複数ある。

## 検討した選択肢

### (a) シェル文書 + `<iframe srcdoc>` + ハッシュルータ(採択)

出力ファイルは薄い「シェル」文書で、全ページの HTML を JSON ペイロードとして内包する。
シェルは 1 枚の `<iframe>` を持ち、表示するページの HTML を `srcdoc` に設定する。
ページ間リンクは `href="#/docs/guide.html"` のようなハッシュルートに書き換え、
シェルが `hashchange` を捕まえてページを切り替える。

- 長所
  - 各ページが独立した `document` / `window` になるため、CSS のグローバル衝突、
    JavaScript のグローバル変数衝突、`DOMContentLoaded` / `load` の発火タイミングなど、
    元ページの挙動がそのまま再現される。
  - ブラウザの履歴はシェルのハッシュ変更だけで構成されるため、戻る / 進むが自然に働き、
    `bundle.html#/about.html` の形でブックマークや共有ができる。
  - ページ切り替えのたびに `<iframe>` 要素を作り直すので、iframe 側のナビゲーションが
    履歴に余計なエントリを積まない。
- 短所
  - JavaScript が必須(`<noscript>` で説明文を表示する)。
  - iframe 内の `location.pathname` などに依存する既存スクリプトは動かない。
  - 印刷やページ内検索はブラウザから見ると iframe の内容になる。印刷については
    `Ctrl+P` の捕捉とシェル側 `beforeprint` での iframe 全高展開で緩和する。

### (b) 単一 document + `<template>` の差し替え

全ページの `<body>` を `<template>` に入れ、1 つの document の中で差し替える。

- iframe を使わないため検索 / 印刷は自然だが、`<head>` のマージが必要で、
  ページごとの CSS / JavaScript が同じグローバル空間で衝突する。
  `DOMContentLoaded` を擬似的に再発火させるなど、再現性の低い細工が必要になる。

### (c) 全ページを縦に連結(アンカー移動)

- 最も単純で JavaScript 不要だが、「遷移」ではなく 1 枚の長い文書になる。
  CSS 衝突も避けられない。

### (d) `document.open()` / `document.write()` で document 全体を置換

- 現行の HTML 仕様では `document.open()` が Window オブジェクトを再生成しないため、
  `const` / `let` の再宣言エラーやタイマー・イベントリスナの残留が起きる。

## 決定

(a) を採択する。理由は、要件 1 と 2 を同時に満たせる唯一の方式であり、
既存サイトのページを **変換せずそのまま** 表示できるためである。
将来 (b) や (c) を `--mode` として追加できるよう、ページ変換(`internal/page`)と
出力(`internal/shell`)は分離しておくが、v1 では実装しない。

## 付随する設計

- ルート形式は `#/<パス>[?<クエリ>][#<フラグメント>]`。パスの各セグメントは
  `url.PathEscape` でエスケープし、ランタイム側で `decodeURIComponent` する。
- ディレクトリへのリンク(`docs/`)は `docs/index.html` に解決する(ランタイムも同じ規則を持つ)。
- 各ページの `<head>` 先頭に `<meta charset="utf-8">`、`<base target="_top">`、
  シム用 `<script>` を注入する。`<base target="_top">` により、外部リンクや
  書き換えられなかったリンクは iframe ではなくトップウィンドウで開く。
- シムは、ページ内スクリプトが動的に生成したリンクのクリックを捕まえてシェルの
  `navigate()` に委ね、`Ctrl+P` をページの `window.print()` に振り向け、
  `document.title` の変更をシェルへ伝える。
- スクロール位置は履歴エントリごとに `history.state` に付与した id をキーに保存し、
  戻る / 進むで復元する。

## 結果

- 出力は 1 ファイル、依存なし、`file://` から動作する。
- 元ページの JavaScript / CSS は無変換で動く。
- JavaScript 無効環境、`location.pathname` 依存のスクリプト、`fetch()` による
  同一フォルダ内ファイルの取得、`<form action>` による遷移は対象外(README に明記)。
