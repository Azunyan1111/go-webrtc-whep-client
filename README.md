# go-webrtc-whep-client

A Go client that receives WebRTC streams via WHEP protocol and pipes them to ffplay for playback verification. Compatible with Cloudflare Stream and other WHEP-compliant servers.

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
# Play video stream with ffplay
./go-webrtc-whep-client -u http://example.com/whep --video-pipe | ffplay -i -

# Play audio stream with ffplay
./go-webrtc-whep-client -u http://example.com/whep --audio-pipe | ffplay -i -

# Play MPEG-TS stream with muxed audio and video
./go-webrtc-whep-client -u http://example.com/whep --mpegts | ffplay -f mpegts -i -

# Play MPEG-TS stream with video only (no audio)
./go-webrtc-whep-client -u http://example.com/whep --mpegts-video-only | ffplay -f mpegts -i -

# Play WebM/Matroska stream with muxed audio and video
./go-webrtc-whep-client -u http://example.com/whep --webm | ffplay -i -

# Play Cloudflare Stream video
./go-webrtc-whep-client -u https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play --video-pipe | ffplay -i -

# Check server codecs
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## Options

- `-u, --url`: WHEP server URL (default: http://localhost:8080/whep)
- `-v, --video-pipe`: Output video to stdout
- `-a, --audio-pipe`: Output audio to stdout
- `-m, --mpegts`: Output MPEG-TS stream with muxed audio and video to stdout
- `--mpegts-video-only`: Output MPEG-TS stream with video only (no audio)
- `-w, --webm`: Output WebM/Matroska stream with muxed audio and video to stdout
- `-c, --codec`: Video codec (h264/vp8/vp9, default: h264)
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

WHEPプロトコルでWebRTCストリームを受信し、ffplayにパイプして再生確認ができるGoクライアント。Cloudflare Streamなどの WHEP対応サーバーで使用可能。

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
# ビデオストリームをffplayで再生
./go-webrtc-whep-client -u http://example.com/whep --video-pipe | ffplay -i -

# オーディオストリームをffplayで再生
./go-webrtc-whep-client -u http://example.com/whep --audio-pipe | ffplay -i -

# MPEG-TSストリームで音声・映像を同時再生
./go-webrtc-whep-client -u http://example.com/whep --mpegts | ffplay -f mpegts -i -

# MPEG-TSストリームで映像のみ再生（音声なし）
./go-webrtc-whep-client -u http://example.com/whep --mpegts-video-only | ffplay -f mpegts -i -

# WebM/Matroskaストリームで音声・映像を同時再生
./go-webrtc-whep-client -u http://example.com/whep --webm | ffplay -i -

# Cloudflare Streamのビデオを再生
./go-webrtc-whep-client -u https://customer-{customer_id}.cloudflarestream.com/{video_id}/webRTC/play --video-pipe | ffplay -i -

# サーバーのコーデック確認
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## オプション

- `-u, --url`: WHEPサーバーURL (デフォルト: http://localhost:8080/whep)
- `-v, --video-pipe`: ビデオをstdoutに出力
- `-a, --audio-pipe`: オーディオをstdoutに出力
- `-m, --mpegts`: MPEG-TSストリームで音声・映像を多重化してstdoutに出力
- `--mpegts-video-only`: MPEG-TSストリームで映像のみ出力（音声なし）
- `-w, --webm`: WebM/Matroskaストリームで音声・映像を多重化してstdoutに出力
- `-c, --codec`: ビデオコーデック (h264/vp8/vp9、デフォルト: h264)
- `-l, --list-codecs`: サーバーのコーデック一覧表示
- `-d, --debug`: デバッグログ表示

## 対応サービス

このクライアントは以下のサービスに対応:
- Cloudflare Stream WebRTC再生 (https://developers.cloudflare.com/stream/webrtc-beta/)
- WHEP準拠のストリーミングサーバー

## ライセンス

MIT