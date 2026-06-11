# CtxPack

**AI エージェント向けトークン節約型コンテキスト抽出ツール。**

CtxPack は、ノイズの多い Web ページ・HTML・Markdown をエージェントが安全に消費できるコンパクトなコンテキストに変換します。不要なページ要素を除去し、有用な構造を保持しながら、トークン削減量を累積で記録します。

```bash
ctxpack https://example.com/article
ctxpack https://example.com/article --query "価格と制限" --json
ctxpack stats
ctxpack reset --yes
```

## なぜ CtxPack か

AI エージェントはしばしば、ナビゲーション・スクリプト・スタイル・Cookie バナー・フッター・関連リンク・繰り返しの定型文でコンテキストを無駄に消費します。既存の Web-to-Markdown ツールは有用ですが、エージェントにはもう一歩が必要です。**トークンを意識したパッキングと、測定可能な節約量の記録**です。

CtxPack はエージェントが URL を読む前に呼び出すツールとして設計されています。

```text
URL / HTML / Markdown
  -> ページの不要要素を除去
  -> 読みやすいテキストを抽出
  -> タスクに関連するセクションを先頭に移動
  -> コンパクトな Markdown または構造化 JSON で返却
  -> 節約したトークン数を記録
```

## 使用例

```bash
ctxpack https://example.com/docs --stats
```

Markdown 出力にはソースのメタデータと、オプションで実行ごとの節約量が含まれます。

```text
Raw input:   42,100 tokens
Clean text:   7,800 tokens
Final:        7,800 tokens
Saved:       34,300 tokens
Reduction:   81.5%
```

エージェントからは JSON で使います。

```bash
ctxpack https://example.com/docs --json
```

```json
{
  "ok": true,
  "source": { "url": "https://example.com/docs", "fetched_at": "..." },
  "title": "Example Docs",
  "content": { "format": "markdown", "text": "..." },
  "stats": {
    "raw_html_tokens": 42100,
    "clean_text_tokens": 7800,
    "final_tokens": 7800,
    "saved_tokens": 34300,
    "reduction_percent": 81.5
  }
}
```

## 累積削減量

通常の実行はすべて `~/.ctxpack/stats.jsonl` にローカル記録されます。

```bash
ctxpack stats
```

```text
Runs:        24
Raw input:   1,024,000 tokens
Clean text:    181,200 tokens
Final:         181,200 tokens
Saved:         842,800 tokens
Reduction:     82.3%
```

履歴をリセットする場合。

```bash
ctxpack reset --yes
```

1 回の実行だけ記録をスキップする場合。

```bash
ctxpack https://example.com --no-record
```

## インストール

### Homebrew（推奨）

```bash
brew install atani/tap/ctxpack
```

### Windows / winget

次回リリースが Windows Package Manager community repository に取り込まれた後は、次でインストールできます。

```powershell
winget install atani.ctxpack
```

このリポジトリは Windows 向けリリース成果物と `packaging/winget/` の winget マニフェストテンプレートを含みます。

### ソースから

```bash
git clone https://github.com/atani/ctxpack.git
cd ctxpack
go install ./cmd/ctxpack
```

開発中に実行する場合。

```bash
go run ./cmd/ctxpack https://example.com --stats
```

### Python 版からの移行

v0.2.x までの ctxpack は Python パッケージでした。CLI（フラグ・サブコマンド・終了コード・JSON スキーマ）と `~/.ctxpack/stats.jsonl` の履歴形式は変わらないため、既存のスクリプトと履歴はそのまま動きます。Python ライブラリ API（`from ctxpack import pack`）は廃止されたので、`pip` / `uv tool install` でインストールしていた場合は上記のいずれかの方法に切り替えてください。

## CLI リファレンス

```bash
ctxpack SOURCE [--query TEXT] [--json] [--stats] [--no-record] [-o FILE]
ctxpack run SOURCE [--query TEXT] [--json] [--stats] [--no-record] [-o FILE]
ctxpack stats [--json] [--reset]
ctxpack reset --yes
```

`SOURCE` に指定できるもの：

- `https://...` または `http://...`
- ローカルの HTML / Markdown ファイル
- 標準入力（`-`）

オプション：

- `--query TEXT` — タスクに関連するセクションを先頭に移動します。
- `--json` — エージェントの tool call 向けの構造化出力。
- `--stats` — Markdown 出力に実行ごとのトークン削減表を含めます。JSON 出力は常に `stats` を含むため、このフラグは Markdown 出力にのみ影響します。
- `--no-record` — この実行を累積削減量に記録しません。
- `-o FILE` — 出力をファイルに書き込みます。

終了コード：

- `0` — 成功。
- `1` — ネットワーク障害やレスポンスサイズ超過などの実行時エラー（リトライ可能）。
- `2` — 使い方エラーや入力ファイルが存在しない（リトライ不可）。
- `3` — 取得したページが JavaScript レンダリングを必要としている（ctxpack 単体ではリトライ不可。[JavaScript レンダリングが必要なページ](#javascript-レンダリングが必要なページ)を参照）。

## 仕組み

### コンテンツフィルタリング

テキスト抽出の前に CtxPack が除去するもの。

- `<script>`, `<style>`, `<noscript>`, `<svg>`, `<canvas>`, `<iframe>`（完全に削除）
- `<nav>`, `<footer>`, `<aside>` 要素
- `class`, `id`, `role` が以下のキーワードをスペース・記号区切りで含む要素（大文字小文字無視）：`ad`, `ads`, `advert`, `banner`, `breadcrumb`, `cookie`, `footer`, `header`, `menu`, `modal`, `nav`, `newsletter`, `promo`, `recommend`, `related`, `share`, `sidebar`, `social`, `subscribe`, `tracking`

マッチングはキーワード単位なので、`site-nav-primary` というクラスはノイズ（`nav`）と判定されますが、`navigation` はそのまま保持されます。有用なコンテンツが削除された場合は、ページをローカルファイルに保存して `ctxpack file.html` で何が保持されるか確認してください。

### トークン推定

トークン数はモデル非依存の高速な近似値であり、モデルごとの正確なカウントではありません。

- CJK 文字（一般的なひらがな・カタカナ・CJK 統合漢字ブロック）は 1 文字あたり約 0.8 トークン
- その他の文字は 4 文字あたり約 1 トークン

これは削減前後の相対比較を目的としたものです。絶対値は特定のトークナイザーと異なります。

### クエリマッチング

`--query` はすべてのコンテンツを保持しつつ、セクションをシンプルな関連スコアで並べ替えます。

- 2 文字以上の単語を大文字小文字無視でマッチング（ステミング・同義語展開なし）
- 本文マッチで +2 点、セクション先頭の見出しにあれば追加 +3 点
- 降順スコアでソート。同点の場合は元の文書順を維持

### ネットワーク動作

- **タイムアウト：** URL フェッチは 20 秒でタイムアウト
- **エンコーディング：** レスポンスで宣言された文字セットを使用、デフォルトは UTF-8。無効なバイトは U+FFFD で置換
- **サイズ制限：** 50 MB 超のレスポンス（ローカルファイルは 100 MB 超）はメモリ枯渇防止のため拒否
- **User-Agent：** `ctxpack (+https://github.com/atani/ctxpack)`

## 制限事項

CtxPack 0.x は静的な HTML / Markdown コンテンツを対象としています。現時点でのスコープ外。

- JavaScript レンダリングが必要なページ（SPA、遅延読み込みコンテンツ）— 検知して終了コード `3` で報告します。[JavaScript レンダリングが必要なページ](#javascript-レンダリングが必要なページ)を参照
- Bot 対策 / CAPTCHA
- ログインが必要なコンテンツ
- PDF・DOCX などのバイナリ形式（事前に HTML / Markdown に変換してください）

標準入力での HTML と Markdown の判定は山括弧の有無で自動検出します。確実に形式を指定したい場合は `.md` / `.html` ファイルパスを渡してください。

### JavaScript レンダリングが必要なページ

取得したページが未レンダリングの JavaScript アプリケーションのシェルに見える場合（抽出できるテキストがほぼ無い + `<script>` タグがあり、SPA のマウントポイント（`id="root"`, `id="app"`, `id="__next"` など）や `<noscript>` の「JavaScript を有効にしてください」メッセージで確認できる場合）、CtxPack はほぼ空のコンテンツを出力する代わりに終了コード `3` で終了します。

```text
ctxpack: page appears to require JavaScript rendering; no extractable main content: https://app.example.com
hint: render the page first and pipe the DOM in, e.g. `chrome --headless=new --dump-dom 'https://app.example.com' | ctxpack -`, or fall back to a JavaScript-capable fetcher.
```

対処方法は 2 つあります。

1. **レンダリング済み DOM をパイプで渡す。** ローカルファイルと標準入力はレンダリング済みとして扱われるため、クリーニングのパイプラインはそのまま適用されます。

   ```bash
   chrome --headless=new --dump-dom 'https://app.example.com' | ctxpack -
   ```

   （`chrome` は環境により `google-chrome` / `chromium`、macOS では `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"` になります。）

2. **呼び出し側でフォールバックする。** エージェントやフックは終了コード `3`（または非ゼロ終了）を見て、JavaScript を実行できる自前のフェッチツールに切り替えられます。

   ```bash
   ctxpack "$url" --json || your-fetch-tool "$url"
   ```

この検知は CtxPack 自身が取得した URL にのみ適用され、ヒューリスティックです。一部をサーバーサイドレンダリングし残りを遅延読み込みするページは、通常どおりパックされます。

## ロードマップ

- 読みやすさ抽出の精度向上
- JavaScript ヘビーなページ向けのオプション Playwright レンダリング
- モデル固有のトークナイザー対応
- JSON でのセクションレベル関連スコア
- キャッシュサポート
- エージェントフレームワーク向け MCP / サーバーモード
- 生 HTML や一般的な Web-to-Markdown ツールとのベンチマーク比較

## ライセンス

MIT
