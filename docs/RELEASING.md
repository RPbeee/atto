# GitHubリリース

## 日常の検証

mainへのpushとPull Requestで、LinuxのGo 1.25・最新stable、およびmacOSの最新stableを検証します。整形、モジュール整合性、vet、race検査、ビルド、実端末相当のPTYテストを実行します。

GitHub公式ActionsはバージョンのコミットSHAに固定しています。DependabotがGoモジュールとActionsの更新を週次で提案します。

## リリース手順

1. `main.go`の`version`、CHANGELOG、必要に応じて仕様書を更新します。
2. `docs/releases/vX.Y.Z.md`にリリースノートを作成します。
3. CIが成功したmainのコミットにタグを付けてpushします。

```sh
git tag -a v0.3.0 -m 'atto v0.3.0'
git push origin v0.3.0
```

Releaseワークフローが検証後にLinux / macOS / Windowsのamd64・arm64向けアーカイブ、依存ライセンス、SHA256SUMSを作成し、GitHub Releaseへ添付します。タグとmain.goのバージョンが一致しない場合は停止します。`vX.Y.Z-beta.1`等のタグはprereleaseとして作成します。ノートがない場合はGitHubの自動生成ノートを使います。

GitHubに既に存在するリリース・アセットは自動で上書きしません。失敗したワークフローは原因を確認してから再実行してください。

## ローカルで配布物を作成する

GoとPython 3が必要です。`GO`環境変数でGo実行ファイルを指定できます。出力先は空のディレクトリを指定します。

```sh
python3 tools/release.py --version v0.3.0 --output dist/v0.3.0
```

一つの対象だけを確認する場合：

```sh
python3 tools/release.py --version v0.3.0 --targets linux/amd64 --output dist/local-check
```

ビルドはCGO無効・trimpath・VCS情報なしで実行し、main.versionをタグから埋め込みます。アーカイブ内のファイル日時・所有者情報を固定します。同じソース・同じGoツールチェーン・同じ依存パッケージでの再現性を想定し、Goバージョンが変わった場合のバイト単位一致は保証しません。
