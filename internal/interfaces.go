package internal

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// RTPProcessor はRTPパケットを処理するインターフェース
type RTPProcessor interface {
	// ProcessRTPPacket はRTPパケットを処理する
	// 返り値は処理されたデータ（複数のNALユニットやフレームを含む可能性がある）
	ProcessRTPPacket(packet *rtp.Packet, codecType string) ([][]byte, error)
}

// StreamWriter は処理されたメディアデータを書き込むインターフェース
type StreamWriter interface {
	// WriteVideoFrame はビデオフレームを書き込む
	WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error

	// WriteAudioFrame はオーディオフレームを書き込む
	WriteAudioFrame(data []byte, timestamp uint32) error

	// Run はメインループを実行する（必要に応じて）
	Run() error

	// Close はリソースをクリーンアップする
	Close() error
}

// StreamMuxer は複数のトラックを処理する統合インターフェース
type StreamMuxer interface {
	// AddVideoTrack はビデオトラックを追加
	AddVideoTrack(track *webrtc.TrackRemote, codecType string) error

	// AddAudioTrack はオーディオトラックを追加
	AddAudioTrack(track *webrtc.TrackRemote) error

	// Run はストリーム処理を開始
	Run() error

	// Stop は処理を停止
	Stop() error
}
