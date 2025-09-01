package internal

import (
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
)

// MPEGTSStreamWriter はMPEG-TSコンテナに出力するライター
type MPEGTSStreamWriter struct {
	muxer     *MPEGTSMuxer
	mu        sync.Mutex
	codecType string
}

// NewMPEGTSStreamWriter は新しいMPEGTSStreamWriterを作成
func NewMPEGTSStreamWriter(w io.Writer, videoTrack, audioTrack *webrtc.TrackRemote) *MPEGTSStreamWriter {
	return &MPEGTSStreamWriter{
		muxer: NewMPEGTSMuxer(w, videoTrack, audioTrack),
	}
}

// WriteVideoFrame はビデオフレームを書き込む
func (m *MPEGTSStreamWriter) WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Initialize timestamp on first frame if needed
	if m.muxer.videoBaseTS == 0 {
		m.muxer.videoBaseTS = timestamp
	}
	
	// MPEG-TSの場合、NALユニットとして処理
	m.muxer.writeVideoPES(data, timestamp)
	return nil
}

// WriteAudioFrame はオーディオフレームを書き込む
func (m *MPEGTSStreamWriter) WriteAudioFrame(data []byte, timestamp uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Initialize timestamp on first frame if needed
	if m.muxer.audioBaseTS == 0 {
		m.muxer.audioBaseTS = timestamp
	}
	
	m.muxer.writeAudioPES(data, timestamp)
	return nil
}

// Run はMPEG-TSマルチプレクサのメインループを実行
func (m *MPEGTSStreamWriter) Run() error {
	go m.muxer.Run()
	return nil
}

// Close はリソースをクリーンアップ
func (m *MPEGTSStreamWriter) Close() error {
	// MPEG-TSは特にクリーンアップ不要
	return nil
}