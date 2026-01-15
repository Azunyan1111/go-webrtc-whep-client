# go-webrtc-whip-whep-client

WHEP/WHIP protocol clients for WebRTC streaming in Go.

- **whep-go**: Receives VP8/VP9 WebRTC streams via WHEP and outputs decoded rawvideo + Opus audio in MKV container to stdout
- **whip-go**: Reads MKV (rawvideo + Opus) from stdin, encodes to VP8, and sends via WHIP protocol

Compatible with Cloudflare Stream and other WHEP/WHIP-compliant servers.

## Features

### whep-go (WHEP Client)
- Receive VP8/VP9 + Opus audio via WHEP protocol
- Decode VP8/VP9 to RGBA using libvpx-go
- Output MKV container with decoded rawvideo + Opus audio to stdout

### whip-go (WHIP Client)
- Read MKV (rawvideo RGBA + Opus) from stdin
- Encode RGBA to VP8 using libvpx-go
- Send VP8 + Opus via WHIP protocol

## Installation

### Using go install
```bash
# Install WHEP client
go install github.com/Azunyan1111/go-webrtc-whep-client/cmd/whep-go@latest

# Install WHIP client
go install github.com/Azunyan1111/go-webrtc-whep-client/cmd/whip-go@latest
```

### Build from source
```bash
# Build WHEP client
go build -o whep-go ./cmd/whep-go

# Build WHIP client
go build -o whip-go ./cmd/whip-go
```

## Usage

### whep-go
```bash
./whep-go <WHEP_URL> [flags]

Arguments:
  WHEP_URL    WHEP server URL (required)

Flags:
  -d, --debug    Enable debug logging
```

### whip-go
```bash
./whip-go <WHIP_URL> [flags]

Arguments:
  WHIP_URL    WHIP server URL (required)

Flags:
  -d, --debug    Enable debug logging

Input:
  stdin       MKV stream with rawvideo (RGBA) + Opus audio
```

## Examples

### Play stream with ffplay
```bash
./whep-go http://example.com/whep | ffplay -i -
```

### Send stream to WHIP server
```bash
cat video.mkv | ./whip-go http://example.com/whip
```

### Relay stream (WHEP to WHIP)
```bash
./whep-go https://source.example.com/whep | ./whip-go https://dest.example.com/whip
```

### Cloudflare Stream examples
```bash
# Receive and play
./whep-go https://customer-{id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -

# Relay between streams
./whep-go https://customer-{id}.cloudflarestream.com/{src_id}/webRTC/play | \
  ./whip-go https://customer-{id}.cloudflarestream.com/{dest_id}/webRTC/publish
```

## Supported Codecs

- Video: VP8, VP9 (decode), VP8 (encode)
- Audio: Opus (passthrough)

## Compatibility

These clients are compatible with:
- Cloudflare Stream WebRTC (https://developers.cloudflare.com/stream/webrtc-beta/)
- Any WHEP/WHIP-compliant streaming server

## License

MIT

---

# go-webrtc-whep-client / go-webrtc-whip-client

GoによるWHEP/WHIPプロトコルクライアント。

- **whep-go**: WHEPでVP8/VP9 WebRTCストリームを受信し、デコード済みrawvideo + Opus音声をMKVでstdoutに出力
- **whip-go**: stdinからMKV（rawvideo + Opus）を読み込み、VP8にエンコードしてWHIPで送信

Cloudflare StreamなどのWHEP/WHIP対応サーバーで使用可能。

## 機能

### whep-go (WHEPクライアント)
- WHEPプロトコルでVP8/VP9 + Opus音声を受信
- libvpx-goでVP8/VP9をRGBAにデコード
- MKVコンテナにデコード済みrawvideo + Opus音声を出力

### whip-go (WHIPクライアント)
- stdinからMKV（rawvideo RGBA + Opus）を読み込み
- libvpx-goでRGBAをVP8にエンコード
- WHIPプロトコルでVP8 + Opusを送信

## インストール

### go installを使う
```bash
# WHEPクライアントをインストール
go install github.com/Azunyan1111/go-webrtc-whep-client/cmd/whep-go@latest

# WHIPクライアントをインストール
go install github.com/Azunyan1111/go-webrtc-whep-client/cmd/whip-go@latest
```

### ソースからビルド
```bash
# WHEPクライアントをビルド
go build -o whep-go ./cmd/whep-go

# WHIPクライアントをビルド
go build -o whip-go ./cmd/whip-go
```

## 使い方

### whep-go
```bash
./whep-go <WHEP_URL> [flags]

引数:
  WHEP_URL    WHEPサーバーURL（必須）

フラグ:
  -d, --debug    デバッグログ有効化
```

### whip-go
```bash
./whip-go <WHIP_URL> [flags]

引数:
  WHIP_URL    WHIPサーバーURL（必須）

フラグ:
  -d, --debug    デバッグログ有効化

入力:
  stdin       rawvideo（RGBA）+ Opus音声のMKVストリーム
```

## 使用例

### ffplayで再生
```bash
./whep-go http://example.com/whep | ffplay -i -
```

### WHIPサーバーに送信
```bash
cat video.mkv | ./whip-go http://example.com/whip
```

### ストリームのリレー（WHEP から WHIP）
```bash
./whep-go https://source.example.com/whep | ./whip-go https://dest.example.com/whip
```

### Cloudflare Streamの例
```bash
# 受信して再生
./whep-go https://customer-{id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -

# ストリーム間でリレー
./whep-go https://customer-{id}.cloudflarestream.com/{src_id}/webRTC/play | \
  ./whip-go https://customer-{id}.cloudflarestream.com/{dest_id}/webRTC/publish
```

## 対応コーデック

- ビデオ: VP8, VP9（デコード）、VP8（エンコード）
- オーディオ: Opus（パススルー）

## 対応サービス

- Cloudflare Stream WebRTC (https://developers.cloudflare.com/stream/webrtc-beta/)
- WHEP/WHIP準拠のストリーミングサーバー

## ライセンス

MIT
