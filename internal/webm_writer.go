package internal

import (
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
)

// WebMStreamWriter はWebMコンテナに出力するライター
type WebMStreamWriter struct {
	muxer        *WebMMuxer
	mu           sync.Mutex
	codecType    string
	initialized  bool
	running      chan struct{}
}

// NewWebMStreamWriter は新しいWebMStreamWriterを作成
func NewWebMStreamWriter(w io.Writer, videoTrack, audioTrack *webrtc.TrackRemote) *WebMStreamWriter {
	return &WebMStreamWriter{
		muxer:   NewWebMMuxer(w, videoTrack, audioTrack),
		running: make(chan struct{}),
	}
}

// WriteVideoFrame はビデオフレームを書き込む
func (w *WebMStreamWriter) WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error {
	// 初期化を待つ
	<-w.running
	
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Initialize timestamp on first frame if needed
	if !w.muxer.hasFirstVideoTS {
		w.muxer.firstVideoTS = timestamp
		w.muxer.hasFirstVideoTS = true
		
		w.muxer.mutex.Lock()
		if !w.muxer.hasBaseTS {
			w.muxer.baseTimestamp = timestamp
			w.muxer.hasBaseTS = true
		}
		w.muxer.mutex.Unlock()
	}
	
	return w.muxer.writeVideoFrame(data, timestamp)
}

// WriteAudioFrame はオーディオフレームを書き込む
func (w *WebMStreamWriter) WriteAudioFrame(data []byte, timestamp uint32) error {
	// 初期化を待つ
	<-w.running
	
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Initialize timestamp on first frame if needed
	if !w.muxer.hasFirstAudioTS {
		w.muxer.firstAudioTS = timestamp
		w.muxer.hasFirstAudioTS = true
		
		w.muxer.mutex.Lock()
		if !w.muxer.hasBaseTS {
			w.muxer.baseTimestamp = timestamp
			w.muxer.hasBaseTS = true
		}
		w.muxer.mutex.Unlock()
	}
	
	return w.muxer.writeAudioFrame(data, timestamp)
}

// Run はWebMマルチプレクサのメインループを実行
func (w *WebMStreamWriter) Run() error {
	// ヘッダーを初期化
	if err := w.muxer.Initialize(); err != nil {
		return err
	}
	
	w.mu.Lock()
	w.initialized = true
	w.mu.Unlock()
	close(w.running)
	
	// muxerを実行（Stopまで待機）
	return w.muxer.Run()
}

// Close はリソースをクリーンアップ
func (w *WebMStreamWriter) Close() error {
	return w.muxer.Stop()
}