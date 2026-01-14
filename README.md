# go-webrtc-whep-client

A Go client that receives VP8/VP9 WebRTC streams via WHEP protocol and outputs decoded rawvideo + Opus audio in MKV container to stdout. Compatible with Cloudflare Stream and other WHEP-compliant servers.

## Features

- Receive VP8/VP9 + Opus audio via WHEP protocol
- Decode VP8/VP9 to RGBA using libvpx-go (v0.2.0)
- Output MKV container with decoded rawvideo + Opus audio (passthrough) to stdout

## Installation

### Build from source
```bash
go build -o go-webrtc-whep-client .
```

### Using go install
```bash
go install github.com/Azunyan1111/go-webrtc-whep-client@latest
```

## Usage

```bash
./go-webrtc-whep-client <WHEP_URL> [flags]

Arguments:
  WHEP_URL    WHEP server URL (required)

Flags:
  -d, --debug    Enable debug logging
```

## Examples

```bash
# Play stream with ffplay
./go-webrtc-whep-client http://example.com/whep | ffplay -i -

# Play with debug logging
./go-webrtc-whep-client http://example.com/whep -d | ffplay -i -

# Play Cloudflare Stream video
./go-webrtc-whep-client https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -
```

## Supported Codecs

- Video: VP8, VP9
- Audio: Opus

## Compatibility

This client is compatible with:
- Cloudflare Stream WebRTC playback (https://developers.cloudflare.com/stream/webrtc-beta/)
- Any WHEP-compliant streaming server

## License

MIT

---

# go-webrtc-whep-client

WHEPプロトコルでVP8/VP9 WebRTCストリームを受信し、デコード済みrawvideo + Opus音声をMKVコンテナでstdoutに出力するGoクライアント。Cloudflare StreamなどのWHEP対応サーバーで使用可能。

## 機能

- WHEPプロトコルでVP8/VP9 + Opus音声を受信
- VP8/VP9をlibvpx-go (v0.2.0)でRGBAにデコード
- MKVコンテナにデコード済みrawvideo + Opus音声（パススルー）をmuxしてstdoutに出力

## インストール

### ソースからビルド
```bash
go build -o go-webrtc-whep-client .
```

### go installを使う方法
```bash
go install github.com/Azunyan1111/go-webrtc-whep-client@latest
```

## 使い方

```bash
./go-webrtc-whep-client <WHEP_URL> [flags]

引数:
  WHEP_URL    WHEPサーバーURL（必須）

フラグ:
  -d, --debug    デバッグログ有効化
```

## 使用例

```bash
# ffplayで再生
./go-webrtc-whep-client http://example.com/whep | ffplay -i -

# デバッグログ付きで再生
./go-webrtc-whep-client http://example.com/whep -d | ffplay -i -

# Cloudflare Streamのビデオを再生
./go-webrtc-whep-client https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -
```

## 対応コーデック

- ビデオ: VP8, VP9
- オーディオ: Opus

## 対応サービス

このクライアントは以下のサービスに対応:
- Cloudflare Stream WebRTC再生 (https://developers.cloudflare.com/stream/webrtc-beta/)
- WHEP準拠のストリーミングサーバー

## ライセンス

MIT
