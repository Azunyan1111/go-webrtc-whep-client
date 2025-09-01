package internal

import (
	"fmt"
	"io"
	"os"

	"github.com/pion/rtp"
)

// SDPStreamWriter はSDP形式で出力するライター
type SDPStreamWriter struct {
	writer io.Writer
}

// NewSDPStreamWriter は新しいSDPStreamWriterを作成
func NewSDPStreamWriter(w io.Writer) *SDPStreamWriter {
	return &SDPStreamWriter{
		writer: w,
	}
}

// WriteVideoRTPPacket はビデオのRTPパケットを直接書き込む
func (s *SDPStreamWriter) WriteVideoRTPPacket(packet *rtp.Packet) error {
	// RTPパケットの情報をデバッグ出力
	fmt.Fprintf(os.Stderr, "video RTP packet: ssrc=%d, seq=%d, timestamp=%d, payload_len=%d\n", packet.SSRC, packet.SequenceNumber, packet.Timestamp, len(packet.Payload))
	return nil
}

// WriteAudioRTPPacket はオーディオのRTPパケットを直接書き込む
func (s *SDPStreamWriter) WriteAudioRTPPacket(packet *rtp.Packet) error {
	// RTPパケットの情報をデバッグ出力
	fmt.Fprintf(os.Stderr, "audio RTP packet: ssrc=%d, seq=%d, timestamp=%d, payload_len=%d\n", packet.SSRC, packet.SequenceNumber, packet.Timestamp, len(packet.Payload))
	return nil
}

// Run は必要ないので何もしない
func (s *SDPStreamWriter) Run() error {
	fmt.Fprintf(os.Stderr, "SDPStreamWriter running (no-op)\n")
	// TODO: ここでstdoutにSDP情報を書き出すなどの処理を追加することも可能

	return nil
}

// Close はリソースをクリーンアップ
func (s *SDPStreamWriter) Close() error {
	fmt.Fprintf(os.Stderr, "SDPStreamWriter closed\n")
	return nil
}
