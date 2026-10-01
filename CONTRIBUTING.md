# 開発への参加

不具合や機能提案はGitHub Issues、コードの変更はPull Requestで受け付けます。操作を変更する場合はREADME・画面内ヘルプ・仕様書も更新してください。

```sh
go test -race ./...
go vet ./...
go build -buildvcs=false -o atto .
python3 tools/pty_smoke.py
```

変更箇所に対応する回帰テストを追加し、日本語・結合文字・複数バッファ・分割画面・未保存データの扱いを確認してください。構成と詳しい検証手順は[開発メモ](docs/DEVELOPMENT.md)を参照してください。
