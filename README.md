# go-webrtc-whep-client

A Go client that receives WebRTC streams via WHEP protocol and pipes Matroska (MKV) or raw video streams to ffplay for playback verification. Compatible with Cloudflare Stream and other WHEP-compliant servers.

## Installation

### Using go install
```bash
go install github.com/Azunyan1111/go-webrtc-whep-client@latest
```

### Build from source
```bash
go build -o go-webrtc-whep-client main.go
```

## Usage

```bash
# Play Matroska (MKV) stream with muxed audio and video
./go-webrtc-whep-client -u http://example.com/whep | ffplay -i -

# Play raw video stream (video only)
./go-webrtc-whep-client -u http://example.com/whep --format rawvideo | ffplay -f h264 -i -

# Play Cloudflare Stream video
./go-webrtc-whep-client -u https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -

# Check server codecs
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## Options

- `-u, --url`: WHEP server URL (default: http://localhost:8080/whep)
- `-c, --codec`: Video codec (h264/vp8/vp9, default: h264)
- `-f, --format`: Output format (mkv/rawvideo, default: mkv)
- `-l, --list-codecs`: List server codecs
- `-d, --debug`: Show debug logs

## Compatibility

This client is compatible with:
- Cloudflare Stream WebRTC playback (https://developers.cloudflare.com/stream/webrtc-beta/)
- Any WHEP-compliant streaming server

## License

MIT

---

# go-webrtc-whep-client

WHEPプロトコルでWebRTCストリームを受信し、Matroska (MKV) もしくはrawvideoをffplayにパイプして再生確認ができるGoクライアント。Cloudflare Streamなどの WHEP対応サーバーで使用可能。

## インストール

### go installを使う方法
```bash
go install github.com/Azunyan1111/go-webrtc-whep-client@latest
```

### ソースからビルド
```bash
go build -o go-webrtc-whep-client main.go
```

## 使い方

```bash
# Matroska (MKV) で音声・映像を同時再生
./go-webrtc-whep-client -u http://example.com/whep | ffplay -i -

# rawvideoで映像のみ出力
./go-webrtc-whep-client -u http://example.com/whep --format rawvideo | ffplay -f h264 -i -

# Cloudflare Streamのビデオを再生
./go-webrtc-whep-client -u https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play | ffplay -i -

# サーバーのコーデック確認
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## オプション

- `-u, --url`: WHEPサーバーURL (デフォルト: http://localhost:8080/whep)
- `-c, --codec`: ビデオコーデック (h264/vp8/vp9、デフォルト: h264)
- `-f, --format`: 出力形式 (mkv/rawvideo、デフォルト: mkv)
- `-l, --list-codecs`: サーバーのコーデック一覧表示
- `-d, --debug`: デバッグログ表示

## 対応サービス

このクライアントは以下のサービスに対応:
- Cloudflare Stream WebRTC再生 (https://developers.cloudflare.com/stream/webrtc-beta/)
- WHEP準拠のストリーミングサーバー

## ライセンス

MIT
