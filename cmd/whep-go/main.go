package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/spf13/pflag"
)

const (
	maxReconnectAttempts = 10               // 最大再接続試行回数
	reconnectInterval    = 5 * time.Second  // 再接続間隔（固定）
	mediaTimeout         = 5 * time.Second  // メディア受信タイムアウト
	connectionTimeout    = 10 * time.Second // ICE接続タイムアウト
)

func main() {
	internal.SetupUsage()
	pflag.Parse()

	if err := internal.ParseArgs(); err != nil {
		pflag.Usage()
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", internal.WhepURL)
	fmt.Fprintln(os.Stderr, "Supported video codecs: VP8, VP9")

	// シグナルハンドリング
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	var lastErr error
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "Reconnection attempt %d/%d in %v...\n",
				attempt, maxReconnectAttempts, reconnectInterval)

			select {
			case <-sigChan:
				fmt.Fprintln(os.Stderr, "Interrupted, exiting...")
				return nil
			case <-time.After(reconnectInterval):
			}
		}

		err := connectAndStream(sigChan)
		if err == nil {
			return nil
		}

		lastErr = err
		fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
	}

	return fmt.Errorf("max reconnection attempts (%d) exceeded: %w",
		maxReconnectAttempts, lastErr)
}

func connectAndStream(sigChan <-chan os.Signal) error {
	// Create MediaEngine with VP8/VP9
	mediaEngine, err := internal.CreateVP8VP9MediaEngine()
	if err != nil {
		return fmt.Errorf("failed to create media engine: %w", err)
	}

	// イベント通知用チャネル
	eventChan := make(chan internal.ConnectionEvent, 10)
	mediaReceivedChan := make(chan struct{}, 1)

	// StreamManagerを先に作成
	processor := internal.NewDefaultRTPProcessor()
	writer := internal.NewRawVideoMKVWriter(os.Stdout, "vp8")
	streamManager := internal.NewStreamManager(writer, processor, mediaTimeout, mediaReceivedChan)

	// Create PeerConnection
	peerConnection, err := internal.CreatePeerConnection(mediaEngine, eventChan, streamManager)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}

	// クリーンアップを確実に実行
	defer func() {
		if stopErr := streamManager.Stop(); stopErr != nil {
			fmt.Fprintf(os.Stderr, "cannot stop stream manager: %v\n", stopErr)
		}
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "cannot close peerConnection: %v\n", cErr)
		}
	}()

	// Exchange SDP with WHEP server
	if err := internal.ExchangeSDPWithWHEP(peerConnection, internal.WhepURL); err != nil {
		return fmt.Errorf("SDP exchange failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "SDP exchange complete, waiting for connection...")

	// ICE接続待機
	connectionTimer := time.NewTimer(connectionTimeout)
	defer connectionTimer.Stop()

WaitConnection:
	for {
		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "Interrupted during connection...")
			return nil
		case event := <-eventChan:
			switch event.State {
			case internal.StateConnected:
				break WaitConnection
			case internal.StateFailed:
				return fmt.Errorf("connection failed: %w", event.Error)
			}
		case <-connectionTimer.C:
			return fmt.Errorf("connection timeout after %v", connectionTimeout)
		}
	}

	fmt.Fprintln(os.Stderr, "ICE connected, starting stream manager...")

	// StreamManager.Run()をgoroutineで開始
	streamErrChan := make(chan error, 1)
	go func() {
		if err := streamManager.Run(); err != nil {
			streamErrChan <- err
		}
	}()

	fmt.Fprintln(os.Stderr, "Waiting for media stream...")

	// メディア受信待機
	mediaTimer := time.NewTimer(mediaTimeout)
	defer mediaTimer.Stop()

	select {
	case <-sigChan:
		fmt.Fprintln(os.Stderr, "Interrupted while waiting for media...")
		return nil
	case <-mediaReceivedChan:
		fmt.Fprintln(os.Stderr, "Media received, streaming...")
	case err := <-streamErrChan:
		return fmt.Errorf("stream error during startup: %w", err)
	case <-mediaTimer.C:
		return fmt.Errorf("media timeout after %v", mediaTimeout)
	}

	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")
	fmt.Fprintln(os.Stderr, "Piping Matroska (MKV) stream with decoded rawvideo + Opus audio to stdout")
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// ストリーミング中のイベント監視
	for {
		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "Closing...")
			return nil
		case err := <-streamErrChan:
			if err != nil {
				return fmt.Errorf("stream error: %w", err)
			}
			return nil
		case event := <-eventChan:
			switch event.State {
			case internal.StateFailed:
				return fmt.Errorf("connection lost: %w", event.Error)
			case internal.StateDisconnected:
				fmt.Fprintln(os.Stderr, "ICE disconnected, waiting for recovery...")
				recoveryTimer := time.NewTimer(5 * time.Second)
				select {
				case <-recoveryTimer.C:
					return fmt.Errorf("ICE recovery timeout")
				case recoverEvent := <-eventChan:
					recoveryTimer.Stop()
					if recoverEvent.State == internal.StateConnected {
						fmt.Fprintln(os.Stderr, "ICE reconnected")
						continue
					}
					return fmt.Errorf("ICE recovery failed: state=%d", recoverEvent.State)
				case <-sigChan:
					recoveryTimer.Stop()
					fmt.Fprintln(os.Stderr, "Interrupted during recovery...")
					return nil
				}
			}
		}
	}
}
