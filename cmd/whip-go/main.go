package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/spf13/pflag"
)

// 統計情報
type stats struct {
	inputVideoFrames   int64 // 入力ビデオフレーム数
	sentVideoFrames    int64 // 送信ビデオフレーム数
	droppedVideoFrames int64 // 破棄ビデオフレーム数
	inputAudioFrames   int64 // 入力オーディオフレーム数
	sentAudioFrames    int64 // 送信オーディオフレーム数
	droppedAudioFrames int64 // 破棄オーディオフレーム数
	sentVideoRTP       int64 // 送信ビデオRTPパケット数
	sentAudioRTP       int64 // 送信オーディオRTPパケット数
	encodeErrors       int64 // エンコードエラー数
	sendErrors         int64 // 送信エラー数
}

func main() {
	internal.SetupWhipUsage()
	pflag.Parse()

	if err := internal.ParseWhipArgs(); err != nil {
		pflag.Usage()
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHIP server: %s\n", internal.WhipURL)
	fmt.Fprintln(os.Stderr, "Reading MKV from stdin (rawvideo + Opus)")

	// Create MKV reader
	mkvReader := internal.NewMKVReader(os.Stdin)

	// Wait for track info
	fmt.Fprintln(os.Stderr, "Waiting for first video frame to determine resolution...")

	// Read first video frame to get dimensions
	var firstFrame *internal.Frame
	for {
		frame, err := mkvReader.ReadFrame()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("no video frames found in input")
			}
			return fmt.Errorf("failed to read frame: %v", err)
		}
		if frame.Type == internal.FrameTypeVideo {
			firstFrame = frame
			break
		}
	}

	width := mkvReader.VideoWidth()
	height := mkvReader.VideoHeight()
	if width == 0 || height == 0 {
		return fmt.Errorf("could not determine video dimensions")
	}
	pixelFormat := mkvReader.PixelFormat()
	fmt.Fprintf(os.Stderr, "Video resolution: %dx%d, pixel format: %s\n", width, height, pixelFormat)

	// Check audio codec
	audioCodec := mkvReader.AudioCodec()
	needsOpusEncode := (audioCodec == "A_PCM/INT/LIT")
	if audioCodec != "" {
		fmt.Fprintf(os.Stderr, "Audio codec: %s\n", audioCodec)
		if needsOpusEncode {
			fmt.Fprintf(os.Stderr, "PCM audio detected, will encode to Opus\n")
		}
	}

	// Create Opus encoder if needed
	var opusEncoder *internal.OpusEncoder
	if needsOpusEncode {
		sampleRate := mkvReader.AudioSampleRate()
		channels := mkvReader.AudioChannels()
		if sampleRate == 0 {
			sampleRate = 48000
		}
		if channels == 0 {
			channels = 2
		}
		fmt.Fprintf(os.Stderr, "Audio: %dHz, %d channels\n", sampleRate, channels)
		var opusErr error
		opusEncoder, opusErr = internal.NewOpusEncoder(sampleRate, channels)
		if opusErr != nil {
			return fmt.Errorf("failed to create Opus encoder: %v", opusErr)
		}
		defer opusEncoder.Close()
	}

	// Create VP8 encoder
	encoder, err := internal.NewVP8Encoder(width, height, pixelFormat)
	if err != nil {
		return fmt.Errorf("failed to create VP8 encoder: %v", err)
	}
	defer encoder.Close()

	// Create MediaEngine
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		},
		PayloadType: 97,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return err
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	// Create API
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create PeerConnection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer peerConnection.Close()

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "whip-go",
	)
	if err != nil {
		return err
	}
	if _, err = peerConnection.AddTrack(videoTrack); err != nil {
		return err
	}

	// Create audio track
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "whip-go",
	)
	if err != nil {
		return err
	}
	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		return err
	}

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		internal.DebugLog("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
		}
	})

	// Exchange SDP with WHIP server
	if err := internal.ExchangeSDPWithWHIP(peerConnection, internal.WhipURL); err != nil {
		return fmt.Errorf("failed to exchange SDP: %v", err)
	}

	fmt.Fprintln(os.Stderr, "Connected to WHIP server, sending media...")
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Create packetizers
	rand.Seed(time.Now().UnixNano())
	videoPacketizer := internal.NewVP8Packetizer(rand.Uint32())
	audioPacketizer := internal.NewOpusPacketizer(rand.Uint32())

	// Create per-track pacers for PTS-based timing
	// Video/Audioで別々に管理し、異なる時刻系列の混在を防ぐ
	var videoPacer *internal.Pacer
	var audioPacer *internal.Pacer
	dropThreshold := time.Duration(internal.DropThreshold) * time.Millisecond
	if !internal.NoPacing {
		videoPacer = internal.NewPacer(1 * time.Second) // max wait 1 second
		audioPacer = internal.NewPacer(1 * time.Second) // max wait 1 second
		fmt.Fprintln(os.Stderr, "PTS-based pacing enabled")
		if dropThreshold > 0 {
			fmt.Fprintf(os.Stderr, "Late frame dropping enabled (threshold: %v)\n", dropThreshold)
		}
	} else {
		fmt.Fprintln(os.Stderr, "PTS-based pacing disabled")
	}

	// 統計情報の初期化
	var s stats
	statsStartTime := time.Now()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	stopChan := make(chan struct{})
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "Stopping...")
		close(stopChan)
	}()

	// 統計情報を5秒ごとに出力するgoroutine
	if internal.DebugMode {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			var lastInputVideo, lastSentVideo, lastDroppedVideo, lastInputAudio, lastSentAudio, lastDroppedAudio int64
			var lastSentVideoRTP, lastSentAudioRTP int64
			lastTime := statsStartTime
			for {
				select {
				case <-stopChan:
					return
				case now := <-ticker.C:
					elapsed := now.Sub(lastTime).Seconds()
					if elapsed <= 0 {
						elapsed = 5.0
					}

					// 現在の値を取得
					currentInputVideo := atomic.LoadInt64(&s.inputVideoFrames)
					currentSentVideo := atomic.LoadInt64(&s.sentVideoFrames)
					currentDroppedVideo := atomic.LoadInt64(&s.droppedVideoFrames)
					currentInputAudio := atomic.LoadInt64(&s.inputAudioFrames)
					currentSentAudio := atomic.LoadInt64(&s.sentAudioFrames)
					currentDroppedAudio := atomic.LoadInt64(&s.droppedAudioFrames)
					currentSentVideoRTP := atomic.LoadInt64(&s.sentVideoRTP)
					currentSentAudioRTP := atomic.LoadInt64(&s.sentAudioRTP)
					encodeErrors := atomic.LoadInt64(&s.encodeErrors)
					sendErrors := atomic.LoadInt64(&s.sendErrors)

					// 差分を計算
					diffInputVideo := currentInputVideo - lastInputVideo
					diffSentVideo := currentSentVideo - lastSentVideo
					diffDroppedVideo := currentDroppedVideo - lastDroppedVideo
					diffInputAudio := currentInputAudio - lastInputAudio
					diffSentAudio := currentSentAudio - lastSentAudio
					diffDroppedAudio := currentDroppedAudio - lastDroppedAudio
					diffSentVideoRTP := currentSentVideoRTP - lastSentVideoRTP
					diffSentAudioRTP := currentSentAudioRTP - lastSentAudioRTP

					// FPS計算
					inputVideoFPS := float64(diffInputVideo) / elapsed
					sentVideoFPS := float64(diffSentVideo) / elapsed
					inputAudioFPS := float64(diffInputAudio) / elapsed
					sentAudioFPS := float64(diffSentAudio) / elapsed

					// 全体経過時間
					totalElapsed := now.Sub(statsStartTime).Seconds()

					fmt.Fprintf(os.Stderr, "\n[STATS] ---- %.1fs elapsed ----\n", totalElapsed)
					fmt.Fprintf(os.Stderr, "[STATS] Video: input=%d (%.1f fps), sent=%d (%.1f fps), dropped=%d, RTP packets=%d\n",
						currentInputVideo, inputVideoFPS, currentSentVideo, sentVideoFPS, diffDroppedVideo, diffSentVideoRTP)
					fmt.Fprintf(os.Stderr, "[STATS] Audio: input=%d (%.1f fps), sent=%d (%.1f fps), dropped=%d, RTP packets=%d\n",
						currentInputAudio, inputAudioFPS, currentSentAudio, sentAudioFPS, diffDroppedAudio, diffSentAudioRTP)
					if encodeErrors > 0 || sendErrors > 0 {
						fmt.Fprintf(os.Stderr, "[STATS] Errors: encode=%d, send=%d\n", encodeErrors, sendErrors)
					}

					// 最後の値を更新
					lastInputVideo = currentInputVideo
					lastSentVideo = currentSentVideo
					lastDroppedVideo = currentDroppedVideo
					lastInputAudio = currentInputAudio
					lastSentAudio = currentSentAudio
					lastDroppedAudio = currentDroppedAudio
					lastSentVideoRTP = currentSentVideoRTP
					lastSentAudioRTP = currentSentAudioRTP
					lastTime = now
				}
			}
		}()
	}

	// Process first frame
	if firstFrame.Type == internal.FrameTypeVideo {
		atomic.AddInt64(&s.inputVideoFrames, 1)
		// Apply pacing before sending
		if videoPacer != nil {
			videoPacer.Wait(firstFrame.TimestampMs)
		}
		// 最初のフレームは破棄チェックなし（基準時刻設定後なので必ず通る）
		sentRTP, err := processVideoFrameWithStats(firstFrame, encoder, videoPacketizer, videoTrack)
		if err != nil {
			internal.DebugLog("Error processing video frame: %v\n", err)
			atomic.AddInt64(&s.encodeErrors, 1)
		} else {
			atomic.AddInt64(&s.sentVideoFrames, 1)
			atomic.AddInt64(&s.sentVideoRTP, int64(sentRTP))
		}
	}

	// Main loop
	videoCount := 1
	audioCount := 0
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
			return nil
		default:
		}

		frame, err := mkvReader.ReadFrame()
		if err != nil {
			if err == io.EOF {
				fmt.Fprintf(os.Stderr, "End of input stream\n")
				fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
				return nil
			}
			return fmt.Errorf("failed to read frame: %v", err)
		}

		switch frame.Type {
		case internal.FrameTypeVideo:
			atomic.AddInt64(&s.inputVideoFrames, 1)
			// Check if frame should be dropped due to lateness
			if videoPacer != nil && videoPacer.ShouldDrop(frame.TimestampMs, dropThreshold) {
				atomic.AddInt64(&s.droppedVideoFrames, 1)
				continue
			}
			// Apply pacing before sending
			if videoPacer != nil {
				videoPacer.Wait(frame.TimestampMs)
			}
			sentRTP, err := processVideoFrameWithStats(frame, encoder, videoPacketizer, videoTrack)
			if err != nil {
				internal.DebugLog("Error processing video frame: %v\n", err)
				atomic.AddInt64(&s.encodeErrors, 1)
				continue
			}
			atomic.AddInt64(&s.sentVideoFrames, 1)
			atomic.AddInt64(&s.sentVideoRTP, int64(sentRTP))
			videoCount++

		case internal.FrameTypeAudio:
			atomic.AddInt64(&s.inputAudioFrames, 1)
			// Check if frame should be dropped due to lateness
			if audioPacer != nil && audioPacer.ShouldDrop(frame.TimestampMs, dropThreshold) {
				atomic.AddInt64(&s.droppedAudioFrames, 1)
				continue
			}
			// Apply pacing before sending
			if audioPacer != nil {
				audioPacer.Wait(frame.TimestampMs)
			}
			if needsOpusEncode && opusEncoder != nil {
				// PCM -> Opus encoding
				encodedFrames, err := opusEncoder.Encode(frame.Data, frame.TimestampMs)
				if err != nil {
					internal.DebugLog("Error encoding audio: %v\n", err)
					atomic.AddInt64(&s.encodeErrors, 1)
					continue
				}
				for _, encoded := range encodedFrames {
					// Use the timestamp from each encoded frame (increments by 10ms per frame)
					packet := audioPacketizer.Packetize(encoded.Data, encoded.TimestampMs)
					if packet != nil {
						if err := audioTrack.WriteRTP(packet); err != nil {
							internal.DebugLog("Error writing audio RTP: %v\n", err)
							atomic.AddInt64(&s.sendErrors, 1)
						} else {
							atomic.AddInt64(&s.sentAudioRTP, 1)
						}
					}
				}
				atomic.AddInt64(&s.sentAudioFrames, 1)
			} else {
				// Already Opus, pass through
				packet := audioPacketizer.Packetize(frame.Data, frame.TimestampMs)
				if packet != nil {
					if err := audioTrack.WriteRTP(packet); err != nil {
						internal.DebugLog("Error writing audio RTP: %v\n", err)
						atomic.AddInt64(&s.sendErrors, 1)
					} else {
						atomic.AddInt64(&s.sentAudioRTP, 1)
					}
				}
				atomic.AddInt64(&s.sentAudioFrames, 1)
			}
			audioCount++
		}
	}
}

func processVideoFrameWithStats(frame *internal.Frame, encoder *internal.VP8Encoder, packetizer *internal.VP8Packetizer, track *webrtc.TrackLocalStaticRTP) (int, error) {
	// Encode RGBA to VP8
	encoded, isKeyframe, err := encoder.Encode(frame.Data)
	if err != nil {
		return 0, fmt.Errorf("encode error: %v", err)
	}
	if encoded == nil {
		return 0, nil
	}

	// Packetize and send
	packets := packetizer.Packetize(encoded, frame.TimestampMs, isKeyframe)
	sentCount := 0
	for _, packet := range packets {
		if err := track.WriteRTP(packet); err != nil {
			return sentCount, fmt.Errorf("write RTP error: %v", err)
		}
		sentCount++
	}

	return sentCount, nil
}
