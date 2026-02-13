package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
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
	queueDroppedFrames int64 // キュー由来の破棄フレーム数
}

const (
	frameQueueCapacity         = 12
	frameQueueLowLatencyTarget = 4
	frameQueueTrimInterval     = 3
)

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
	if internal.CPUProfilePath != "" {
		f, err := os.Create(internal.CPUProfilePath)
		if err != nil {
			return fmt.Errorf("failed to create cpu profile file: %v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			_ = f.Close()
			return fmt.Errorf("failed to start cpu profile: %v", err)
		}
		defer func() {
			pprof.StopCPUProfile()
			_ = f.Close()
			fmt.Fprintf(os.Stderr, "CPU profile written: %s\n", internal.CPUProfilePath)
		}()
	}
	if internal.MemProfilePath != "" {
		defer func() {
			f, err := os.Create(internal.MemProfilePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to create mem profile file: %v\n", err)
				return
			}
			defer f.Close()
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write mem profile: %v\n", err)
				return
			}
			fmt.Fprintf(os.Stderr, "Memory profile written: %s\n", internal.MemProfilePath)
		}()
	}

	fmt.Fprintf(os.Stderr, "Connecting to WHIP server: %s\n", internal.WhipURL)
	fmt.Fprintln(os.Stderr, "Reading MKV from stdin (rawvideo + Opus)")

	// Create MKV reader
	mkvReader := internal.NewMKVReader(os.Stdin)

	// 統計情報の初期化
	var s stats

	// stdin 取り込みを先行し、後段のエンコード/送信と分離する
	frameQueue := make(chan *internal.Frame, frameQueueCapacity)
	frameReadErr := make(chan error, 1)
	go ingestFrames(mkvReader, frameQueue, frameReadErr, &s)

	// Wait for track info
	fmt.Fprintln(os.Stderr, "Waiting for first video frame to determine resolution...")

	// Read first video frame to get dimensions
	var firstFrame *internal.Frame
	for {
		frame, ok := <-frameQueue
		if !ok {
			err := <-frameReadErr
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
	videoSender, err := peerConnection.AddTrack(videoTrack)
	if err != nil {
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
	audioSender, err := peerConnection.AddTrack(audioTrack)
	if err != nil {
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

	// Read RTCP reports from senders
	// RTCP受信時刻を追跡し、5秒間受信がなければ自動終了
	var lastRTCPReceived int64
	atomic.StoreInt64(&lastRTCPReceived, time.Now().UnixNano())
	go readRTCP("video", videoSender, &lastRTCPReceived)
	go readRTCP("audio", audioSender, &lastRTCPReceived)

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

	statsStartTime := time.Now()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	stopChan := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() {
		stopOnce.Do(func() { close(stopChan) })
	}
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "Stopping...")
		closeStop()
	}()

	// RTCPタイムアウト監視: 5秒間RTCPレポートが来なければ自動終了
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				last := atomic.LoadInt64(&lastRTCPReceived)
				if time.Since(time.Unix(0, last)) > 5*time.Second {
					fmt.Fprintln(os.Stderr, "RTCP timeout: no reports received for 5 seconds, stopping...")
					closeStop()
					return
				}
			}
		}
	}()

	// 統計情報を5秒ごとに出力するgoroutine
	if internal.DebugMode {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			var lastInputVideo, lastSentVideo, lastDroppedVideo, lastInputAudio, lastSentAudio, lastDroppedAudio int64
			var lastSentVideoRTP, lastSentAudioRTP int64
			var lastQueueDropped int64
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
					currentQueueDropped := atomic.LoadInt64(&s.queueDroppedFrames)
					encodeErrors := atomic.LoadInt64(&s.encodeErrors)
					sendErrors := atomic.LoadInt64(&s.sendErrors)
					queueDepth := len(frameQueue)
					queueCap := cap(frameQueue)

					// 差分を計算
					diffInputVideo := currentInputVideo - lastInputVideo
					diffSentVideo := currentSentVideo - lastSentVideo
					diffDroppedVideo := currentDroppedVideo - lastDroppedVideo
					diffInputAudio := currentInputAudio - lastInputAudio
					diffSentAudio := currentSentAudio - lastSentAudio
					diffDroppedAudio := currentDroppedAudio - lastDroppedAudio
					diffSentVideoRTP := currentSentVideoRTP - lastSentVideoRTP
					diffSentAudioRTP := currentSentAudioRTP - lastSentAudioRTP
					diffQueueDropped := currentQueueDropped - lastQueueDropped

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
					fmt.Fprintf(os.Stderr, "[STATS] Queue: depth=%d/%d, dropped(total=%d, +%d)\n",
						queueDepth, queueCap, currentQueueDropped, diffQueueDropped)
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
					lastQueueDropped = currentQueueDropped
					lastTime = now
				}
			}
		}()
	}

	// Process first frame
	if firstFrame.Type == internal.FrameTypeVideo {
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
	lastQueueDropSeen := atomic.LoadInt64(&s.queueDroppedFrames)
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
			return nil
		case frame, ok := <-frameQueue:
			if !ok {
				err := <-frameReadErr
				if err == io.EOF {
					fmt.Fprintf(os.Stderr, "End of input stream\n")
					fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
					return nil
				}
				return fmt.Errorf("failed to read frame: %v", err)
			}

			currentQueueDropSeen := atomic.LoadInt64(&s.queueDroppedFrames)
			if currentQueueDropSeen != lastQueueDropSeen {
				if videoPacer != nil {
					videoPacer.Reset()
				}
				if audioPacer != nil {
					audioPacer.Reset()
				}
				internal.DebugLogPeriodic("pacer.queue_resync", time.Second, "Pacing resync after queue drops: total=%d\n", currentQueueDropSeen)
				lastQueueDropSeen = currentQueueDropSeen
			}

			switch frame.Type {
			case internal.FrameTypeVideo:
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
}

func ingestFrames(mkvReader *internal.MKVReader, frameQueue chan *internal.Frame, frameReadErr chan<- error, s *stats) {
	defer close(frameQueue)
	trimCounter := 0

	for {
		frame, err := mkvReader.ReadFrame()
		if err != nil {
			frameReadErr <- err
			return
		}

		addInputFrameStats(s, frame)
		enqueueFrame(frameQueue, frame, s, &trimCounter)
	}
}

func enqueueFrame(frameQueue chan *internal.Frame, frame *internal.Frame, s *stats, trimCounter *int) {
	for {
		select {
		case frameQueue <- frame:
			break
		default:
			dropped := dropOldestFrame(frameQueue)
			if dropped != nil {
				recordQueueDrop(s, dropped, "queue-full", len(frameQueue), cap(frameQueue))
			}
			continue
		}
		break
	}

	// 入出力FPSが同程度で滞留する場合、目標超過時に段階的に先頭を捨てて低遅延へ近づける
	if len(frameQueue) > frameQueueLowLatencyTarget {
		*trimCounter++
		if *trimCounter >= frameQueueTrimInterval {
			dropped := dropOldestFrame(frameQueue)
			if dropped != nil {
				recordQueueDrop(s, dropped, "latency-trim", len(frameQueue), cap(frameQueue))
			}
			*trimCounter = 0
		}
		return
	}

	*trimCounter = 0
}

func dropOldestFrame(frameQueue chan *internal.Frame) *internal.Frame {
	select {
	case frame := <-frameQueue:
		return frame
	default:
		return nil
	}
}

func addInputFrameStats(s *stats, frame *internal.Frame) {
	switch frame.Type {
	case internal.FrameTypeVideo:
		atomic.AddInt64(&s.inputVideoFrames, 1)
	case internal.FrameTypeAudio:
		atomic.AddInt64(&s.inputAudioFrames, 1)
	}
}

func recordQueueDrop(s *stats, frame *internal.Frame, reason string, queueDepth int, queueCap int) {
	atomic.AddInt64(&s.queueDroppedFrames, 1)
	switch frame.Type {
	case internal.FrameTypeVideo:
		atomic.AddInt64(&s.droppedVideoFrames, 1)
	case internal.FrameTypeAudio:
		atomic.AddInt64(&s.droppedAudioFrames, 1)
	}

	internal.DebugLog("[QUEUE] dropped oldest frame reason=%s type=%s depth=%d/%d ts=%dms\n",
		reason, frameTypeString(frame.Type), queueDepth, queueCap, frame.TimestampMs)
}

func frameTypeString(frameType internal.FrameType) string {
	switch frameType {
	case internal.FrameTypeVideo:
		return "video"
	case internal.FrameTypeAudio:
		return "audio"
	default:
		return "unknown"
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

func readRTCP(trackType string, sender *webrtc.RTPSender, lastReceived *int64) {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		atomic.StoreInt64(lastReceived, time.Now().UnixNano())
		if !internal.DebugMode {
			continue
		}
		for _, pkt := range packets {
			switch p := pkt.(type) {
			case *rtcp.ReceiverReport:
				for _, r := range p.Reports {
					lossPercent := float64(r.FractionLost) / 256.0 * 100.0
					fmt.Fprintf(os.Stderr, "[RTCP] %s RR: SSRC=%x loss=%.1f%% totalLost=%d jitter=%d lastSeq=%d\n",
						trackType, r.SSRC, lossPercent, r.TotalLost, r.Jitter, r.LastSequenceNumber)
				}
			case *rtcp.SenderReport:
				fmt.Fprintf(os.Stderr, "[RTCP] %s SR: SSRC=%x packets=%d octets=%d\n",
					trackType, p.SSRC, p.PacketCount, p.OctetCount)
				for _, r := range p.Reports {
					lossPercent := float64(r.FractionLost) / 256.0 * 100.0
					fmt.Fprintf(os.Stderr, "[RTCP] %s SR-RR: SSRC=%x loss=%.1f%% totalLost=%d jitter=%d\n",
						trackType, r.SSRC, lossPercent, r.TotalLost, r.Jitter)
				}
			case *rtcp.TransportLayerNack:
				for _, nack := range p.Nacks {
					fmt.Fprintf(os.Stderr, "[RTCP] %s NACK: seqNums=%v\n", trackType, nack.PacketList())
				}
			case *rtcp.PictureLossIndication:
				fmt.Fprintf(os.Stderr, "[RTCP] %s PLI: senderSSRC=%x mediaSSRC=%x\n",
					trackType, p.SenderSSRC, p.MediaSSRC)
			case *rtcp.FullIntraRequest:
				fmt.Fprintf(os.Stderr, "[RTCP] %s FIR: senderSSRC=%x mediaSSRC=%x\n",
					trackType, p.SenderSSRC, p.MediaSSRC)
			case *rtcp.ReceiverEstimatedMaximumBitrate:
				fmt.Fprintf(os.Stderr, "[RTCP] %s REMB: bitrate=%.0f bps\n", trackType, p.Bitrate)
			default:
				fmt.Fprintf(os.Stderr, "[RTCP] %s %T\n", trackType, pkt)
			}
		}
	}
}
