package internal

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/pion/webrtc/v4"
)

// StreamManager はストリーム処理を管理する統合クラス
type StreamManager struct {
	videoTrack *webrtc.TrackRemote
	audioTrack *webrtc.TrackRemote
	writer     StreamWriter
	processor  RTPProcessor
	codecType  string
	done       chan struct{}
	errChan    chan error
	wg         sync.WaitGroup
	closeOnce  sync.Once
	mu         sync.Mutex
	running    bool
}

// NewStreamManager は新しいストリームマネージャーを作成
func NewStreamManager(writer StreamWriter, processor RTPProcessor) *StreamManager {
	return &StreamManager{
		writer:    writer,
		processor: processor,
		done:      make(chan struct{}),
		errChan:   make(chan error, 2),
	}
}

// AddVideoTrack はビデオトラックを追加
func (sm *StreamManager) AddVideoTrack(track *webrtc.TrackRemote, codecType string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.videoTrack = track
	sm.codecType = codecType

	// 既に実行中の場合、新しいトラックの処理を開始
	if sm.running && track != nil {
		sm.wg.Add(1)
		go sm.processVideoStream()
	}
}

// AddAudioTrack はオーディオトラックを追加
func (sm *StreamManager) AddAudioTrack(track *webrtc.TrackRemote) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.audioTrack = track

	// 既に実行中の場合、新しいトラックの処理を開始
	if sm.running && track != nil {
		sm.wg.Add(1)
		go sm.processAudioStream()
	}
}

// Run はストリーム処理を開始
func (sm *StreamManager) Run() error {
	sm.mu.Lock()
	sm.running = true
	// 現在のトラックを取得
	videoTrack := sm.videoTrack
	audioTrack := sm.audioTrack
	sm.mu.Unlock()

	// WriterのRunメソッドがあれば実行
	if runner, ok := sm.writer.(interface{ Run() error }); ok {
		go func() {
			if err := runner.Run(); err != nil {
				sm.errChan <- err
			}
		}()
	}

	// ビデオストリーム処理
	if videoTrack != nil {
		sm.wg.Add(1)
		go sm.processVideoStream()
	}

	// オーディオストリーム処理
	if audioTrack != nil {
		sm.wg.Add(1)
		go sm.processAudioStream()
	}

	// エラー監視
	go func() {
		sm.wg.Wait()
		sm.closeOnce.Do(func() {
			close(sm.errChan)
		})
	}()

	// エラーをチェック
	for err := range sm.errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop はストリーム処理を停止
func (sm *StreamManager) Stop() error {
	close(sm.done)
	sm.wg.Wait()

	if closer, ok := sm.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// processVideoStream はビデオストリームを処理
func (sm *StreamManager) processVideoStream() {
	defer sm.wg.Done()
	fmt.Fprintf(os.Stderr, "Starting video stream processing\n")

	for {
		select {
		case <-sm.done:
			return
		default:
		}

		rtpPacket, _, err := sm.videoTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			sm.errChan <- fmt.Errorf("error reading video RTP: %w", err)
			return
		}

		// RTPパケットを処理
		frames, err := sm.processor.ProcessRTPPacket(rtpPacket, sm.codecType)
		if err != nil {
			sm.errChan <- fmt.Errorf("error processing video RTP: %w", err)
			return
		}

		// フレームを書き込み
		for _, frame := range frames {
			keyframe := sm.isKeyframe(frame, sm.codecType)
			if err := sm.writer.WriteVideoFrame(frame, rtpPacket.Timestamp, keyframe); err != nil {
				sm.errChan <- fmt.Errorf("error writing video frame: %w", err)
				return
			}
		}
	}
}

// processAudioStream はオーディオストリームを処理
func (sm *StreamManager) processAudioStream() {
	defer sm.wg.Done()
	fmt.Fprintf(os.Stderr, "Starting audio stream processing\n")

	for {
		select {
		case <-sm.done:
			return
		default:
		}

		rtpPacket, _, err := sm.audioTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			sm.errChan <- fmt.Errorf("error reading audio RTP: %w", err)
			return
		}

		// RTPパケットを処理（オーディオは通常opus）
		frames, err := sm.processor.ProcessRTPPacket(rtpPacket, "opus")
		if err != nil {
			sm.errChan <- fmt.Errorf("error processing audio RTP: %w", err)
			return
		}

		// フレームを書き込み
		for _, frame := range frames {
			if err := sm.writer.WriteAudioFrame(frame, rtpPacket.Timestamp); err != nil {
				sm.errChan <- fmt.Errorf("error writing audio frame: %w", err)
				return
			}
		}
	}
}

// isKeyframe はフレームがキーフレームかどうかを判定
func (sm *StreamManager) isKeyframe(frame []byte, codecType string) bool {
	if len(frame) == 0 {
		return false
	}

	switch codecType {
	case "vp8":
		// VP8のキーフレームをチェック
		if len(frame) > 0 {
			return (frame[0] & 0x01) == 0
		}
	case "vp9":
		// VP9のキーフレームをチェック
		// 簡略化された判定
		return true
	}

	return false
}
