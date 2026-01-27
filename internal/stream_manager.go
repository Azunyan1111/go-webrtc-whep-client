package internal

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/videoframe"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// StreamManager はストリーム処理を管理する統合クラス
type StreamManager struct {
	videoTrack      *webrtc.TrackRemote
	audioTrack      *webrtc.TrackRemote
	writer          StreamWriter
	processor       RTPProcessor
	codecType       string
	done            chan struct{}
	errChan         chan error
	wg              sync.WaitGroup
	closeOnce       sync.Once
	mu              sync.Mutex
	running         bool
	baseTimeout     time.Duration   // RTP読み取り基本タイムアウト（2秒）
	maxTimeout      time.Duration   // RTP読み取り最大タイムアウト
	timeoutStep     time.Duration   // タイムアウト増加ステップ（1秒）
	currentTimeout  time.Duration   // 現在のタイムアウト値
	mediaReceivedCh chan<- struct{} // 最初のメディア受信通知用
	firstMediaSent  bool            // 通知済みフラグ
	seenKeyFrame    bool            // videoframe用: キーフレーム受信済みフラグ
}

// rtpReadResult はReadRTPの結果を格納
type rtpReadResult struct {
	packet *rtp.Packet
	attrs  interceptor.Attributes
	err    error
}

// NewStreamManager は新しいストリームマネージャーを作成
// maxTimeout: 最大タイムアウト値（タイムアウトは2秒から開始し、1秒ずつ増加してこの値に達する）
func NewStreamManager(writer StreamWriter, processor RTPProcessor, maxTimeout time.Duration, mediaReceivedCh chan<- struct{}) *StreamManager {
	baseTimeout := 2 * time.Second
	timeoutStep := 1 * time.Second
	return &StreamManager{
		writer:          writer,
		processor:       processor,
		done:            make(chan struct{}),
		errChan:         make(chan error, 2),
		baseTimeout:     baseTimeout,
		maxTimeout:      maxTimeout,
		timeoutStep:     timeoutStep,
		currentTimeout:  baseTimeout,
		mediaReceivedCh: mediaReceivedCh,
	}
}

// readRTPWithTimeout はタイムアウト付きでRTPパケットを読み取る
// タイムアウトは2秒から開始し、タイムアウト発生ごとに1秒ずつ増加（最大maxTimeoutまで）
// パケット受信成功時はタイムアウトを2秒にリセット
func (sm *StreamManager) readRTPWithTimeout(track *webrtc.TrackRemote) (*rtp.Packet, interceptor.Attributes, error) {
	if sm.maxTimeout <= 0 {
		return track.ReadRTP()
	}

	resultChan := make(chan rtpReadResult, 1)

	go func() {
		packet, attrs, err := track.ReadRTP()
		select {
		case resultChan <- rtpReadResult{packet: packet, attrs: attrs, err: err}:
		case <-sm.done:
		}
	}()

	sm.mu.Lock()
	timeout := sm.currentTimeout
	sm.mu.Unlock()

	select {
	case <-sm.done:
		return nil, nil, io.EOF
	case result := <-resultChan:
		// 成功時はタイムアウトをリセット
		sm.mu.Lock()
		sm.currentTimeout = sm.baseTimeout
		sm.mu.Unlock()
		return result.packet, result.attrs, result.err
	case <-time.After(timeout):
		// タイムアウト時は次回のタイムアウト値を増加
		sm.mu.Lock()
		sm.currentTimeout += sm.timeoutStep
		if sm.currentTimeout > sm.maxTimeout {
			sm.currentTimeout = sm.maxTimeout
		}
		sm.mu.Unlock()
		return nil, nil, fmt.Errorf("RTP read timeout after %v", timeout)
	}
}

// notifyMediaReceived は最初のメディア受信を通知する（1回のみ）
func (sm *StreamManager) notifyMediaReceived() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.firstMediaSent && sm.mediaReceivedCh != nil {
		select {
		case sm.mediaReceivedCh <- struct{}{}:
			sm.firstMediaSent = true
		default:
		}
	}
}

// AddVideoTrack はビデオトラックを追加
func (sm *StreamManager) AddVideoTrack(track *webrtc.TrackRemote, codecType string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.videoTrack = track
	sm.codecType = codecType

	// 既に実行中かつ停止していない場合、新しいトラックの処理を開始
	if sm.running && track != nil {
		select {
		case <-sm.done:
			return
		default:
			sm.wg.Add(1)
			go sm.processVideoStream()
		}
	}
}

// AddAudioTrack はオーディオトラックを追加
func (sm *StreamManager) AddAudioTrack(track *webrtc.TrackRemote) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.audioTrack = track

	// 既に実行中かつ停止していない場合、新しいトラックの処理を開始
	if sm.running && track != nil {
		select {
		case <-sm.done:
			return
		default:
			sm.wg.Add(1)
			go sm.processAudioStream()
		}
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
			select {
			case sm.errChan <- runner.Run():
			case <-sm.done:
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

	// エラー監視: doneチャネルかerrChanからのエラーを待つ
	for {
		select {
		case <-sm.done:
			return nil
		case err := <-sm.errChan:
			if err != nil {
				return err
			}
		}
	}
}

// Stop はストリーム処理を停止
func (sm *StreamManager) Stop() error {
	sm.mu.Lock()
	sm.running = false
	sm.mu.Unlock()

	sm.closeOnce.Do(func() {
		close(sm.done)
	})
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

		rtpPacket, attrs, err := sm.readRTPWithTimeout(sm.videoTrack)
		if err != nil {
			if err == io.EOF {
				return
			}
			select {
			case sm.errChan <- fmt.Errorf("error reading video RTP: %w", err):
			case <-sm.done:
			}
			return
		}

		// 最初のメディア受信を通知
		sm.notifyMediaReceived()

		// videoframe interceptorからEncodedFrameを取得（VP8の場合）
		if sm.codecType == "vp8" && attrs != nil {
			if val := attrs.Get(videoframe.EncodedFramesKey); val != nil {
				if encodedFrames, ok := val.([]*videoframe.EncodedFrame); ok && len(encodedFrames) > 0 {
					for _, frame := range encodedFrames {
						keyframe := frame.FrameType == videoframe.FrameTypeKey

						// キーフレームをまだ見ていない場合はスキップ
						if !sm.seenKeyFrame {
							if keyframe {
								sm.seenKeyFrame = true
								DebugLog("videoframe: First keyframe received, ID=%d, Size=%d\n", frame.ID, len(frame.Data))
							} else {
								continue // キーフレーム前のデルタフレームはスキップ
							}
						}

						if err := sm.writer.WriteVideoFrame(frame.Data, frame.Timestamp, keyframe); err != nil {
							select {
							case sm.errChan <- fmt.Errorf("error writing video frame: %w", err):
							case <-sm.done:
							}
							return
						}
					}
					continue
				}
			}
		}

		// フォールバック: 従来のRTPプロセッサを使用
		frames, err := sm.processor.ProcessRTPPacket(rtpPacket, sm.codecType)
		if err != nil {
			select {
			case sm.errChan <- fmt.Errorf("error processing video RTP: %w", err):
			case <-sm.done:
			}
			return
		}

		// フレームを書き込み
		for _, frame := range frames {
			keyframe := sm.isKeyframe(frame, sm.codecType)
			if err := sm.writer.WriteVideoFrame(frame, rtpPacket.Timestamp, keyframe); err != nil {
				select {
				case sm.errChan <- fmt.Errorf("error writing video frame: %w", err):
				case <-sm.done:
				}
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

		rtpPacket, _, err := sm.readRTPWithTimeout(sm.audioTrack)
		if err != nil {
			if err == io.EOF {
				return
			}
			select {
			case sm.errChan <- fmt.Errorf("error reading audio RTP: %w", err):
			case <-sm.done:
			}
			return
		}

		// RTPパケットを処理（オーディオは通常opus）
		frames, err := sm.processor.ProcessRTPPacket(rtpPacket, "opus")
		if err != nil {
			select {
			case sm.errChan <- fmt.Errorf("error processing audio RTP: %w", err):
			case <-sm.done:
			}
			return
		}

		// フレームを書き込み
		for _, frame := range frames {
			if err := sm.writer.WriteAudioFrame(frame, rtpPacket.Timestamp); err != nil {
				select {
				case sm.errChan <- fmt.Errorf("error writing audio frame: %w", err):
				case <-sm.done:
				}
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

// stripVP8PayloadDescriptor はVP8 RTPペイロードデスクリプタを除去してビットストリームを取得
// RFC 7741に基づく
func stripVP8PayloadDescriptor(data []byte) []byte {
	if len(data) < 1 {
		return data
	}

	headerSize := 1
	firstByte := data[0]

	// X bit - extension present
	if firstByte&0x80 != 0 {
		if len(data) < 2 {
			return data
		}
		headerSize++
		extByte := data[1]

		// I bit - PictureID present
		if extByte&0x80 != 0 {
			headerSize++
			if len(data) < headerSize {
				return data
			}
			// M bit - PictureID is 16 bits
			if data[headerSize-1]&0x80 != 0 {
				headerSize++
			}
		}

		// L bit - TL0PICIDX present
		if extByte&0x40 != 0 {
			headerSize++
		}

		// T or K bit - TID/KEYIDX present
		if extByte&0x20 != 0 || extByte&0x10 != 0 {
			headerSize++
		}
	}

	if len(data) <= headerSize {
		return data
	}

	return data[headerSize:]
}
