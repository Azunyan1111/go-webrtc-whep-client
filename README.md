# go-webrtc-whep-client

A Go client that receives WebRTC streams via WHEP protocol and pipes them to ffmpeg

## Installation

```bash
go build -o go-webrtc-whep-client main.go
```

## Usage

```bash
# Save video to MP4
./go-webrtc-whep-client -u http://example.com/whep --video-pipe | ffmpeg -i - -c copy output.mp4

# Save audio to MP3
./go-webrtc-whep-client -u http://example.com/whep --audio-pipe | ffmpeg -i - -c copy output.mp3

# Check server codecs
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## Options

- `-u, --url`: WHEP server URL (default: http://localhost:8080/whep)
- `-v, --video-pipe`: Output video to stdout
- `-a, --audio-pipe`: Output audio to stdout
- `-c, --codec`: Video codec (h264/vp8/vp9, default: h264)
- `-l, --list-codecs`: List server codecs
- `-d, --debug`: Show debug logs

## License

MIT

---

# go-webrtc-whep-client

WHEPプロトコルでWebRTCストリームを受信し、ffmpegにパイプできるGoクライアント

## インストール

```bash
go build -o go-webrtc-whep-client main.go
```

## 使い方

```bash
# ビデオをMP4に保存
./go-webrtc-whep-client -u http://example.com/whep --video-pipe | ffmpeg -i - -c copy output.mp4

# オーディオをMP3に保存
./go-webrtc-whep-client -u http://example.com/whep --audio-pipe | ffmpeg -i - -c copy output.mp3

# サーバーのコーデック確認
./go-webrtc-whep-client -u http://example.com/whep --list-codecs
```

## オプション

- `-u, --url`: WHEPサーバーURL (デフォルト: http://localhost:8080/whep)
- `-v, --video-pipe`: ビデオをstdoutに出力
- `-a, --audio-pipe`: オーディオをstdoutに出力
- `-c, --codec`: ビデオコーデック (h264/vp8/vp9、デフォルト: h264)
- `-l, --list-codecs`: サーバーのコーデック一覧表示
- `-d, --debug`: デバッグログ表示

## ライセンス

MIT