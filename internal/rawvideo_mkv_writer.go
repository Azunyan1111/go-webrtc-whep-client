package internal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
	"unsafe"

	"github.com/Azunyan1111/libvpx-go/vpx"
)

// Matroska (MKV) EBML IDs
const (
	ebmlHeader  = 0x1A45DFA3
	segment     = 0x18538067
	info        = 0x1549A966
	tracks      = 0x1654AE6B
	cluster     = 0x1F43B675
	timecode    = 0xE7
	simpleBlock = 0xA3

	// Info elements
	timecodeScale = 0x2AD7B1
	muxingApp     = 0x4D80
	writingApp    = 0x5741

	// Track elements
	trackEntry        = 0xAE
	trackNumber       = 0xD7
	trackUID          = 0x73C5
	trackType         = 0x83
	codecID           = 0x86
	video             = 0xE0
	audio             = 0xE1
	pixelWidth        = 0xB0
	pixelHeight       = 0xBA
	samplingFrequency = 0xB5
	channels          = 0x9F
	colourSpace       = 0x2EB524
	bitsPerChannel    = 0x55B2

	// Track types
	trackTypeVideo = 0x01
	trackTypeAudio = 0x02
)

// RawVideoMKVWriter はVP8/VP9をデコードしてrawvideoとしてMKVに出力するライター
type RawVideoMKVWriter struct {
	writer          io.Writer
	bufWriter       *bufio.Writer
	ctx             *vpx.CodecCtx
	codecType       string
	width           int
	height          int
	resolutionKnown bool
	isHeaderWritten bool
	videoTrackNum   uint64
	audioTrackNum   uint64
	clusterTime     uint64
	videoTimestamp  rtpTimestampUnwrapper
	audioTimestamp  rtpTimestampUnwrapper
	mutex           sync.Mutex
	done            chan struct{}
	running         chan struct{}
	initialized     bool
	decoderInit     bool
	lastValidFrame  []byte          // 最後に成功したRGBAフレームデータ（デコード失敗時の再出力用）
	frameValidator  *FrameValidator // フレーム品質検証器
	validationStats ValidationStats // 検証統計情報
}

// ValidationStats は検証統計を保持
type ValidationStats struct {
	TotalFrames       int
	ValidFrames       int
	InvalidFrames     int
	RepeatedFrames    int // lastValidFrameを再利用した回数
	DecodeErrors      int
	LastInvalidReason string
}

// rtpTimestampUnwrapper は32bit RTP timestampを64bitの単調増加値へ展開する
type rtpTimestampUnwrapper struct {
	initialized bool
	lastRaw     uint32
	wrapCount   uint64
}

func (u *rtpTimestampUnwrapper) Extend(timestamp uint32) uint64 {
	if !u.initialized {
		u.initialized = true
		u.lastRaw = timestamp
		return uint64(timestamp)
	}

	// 32bit境界を跨いだ前進のみラップとして扱う
	if timestamp < u.lastRaw && (u.lastRaw-timestamp) > (1<<31) {
		u.wrapCount++
	}

	u.lastRaw = timestamp
	return (u.wrapCount << 32) | uint64(timestamp)
}

// NewRawVideoMKVWriter は新しいRawVideoMKVWriterを作成
func NewRawVideoMKVWriter(w io.Writer, codecType string) *RawVideoMKVWriter {
	bufWriter := bufio.NewWriterSize(w, 64*1024) // 64KB buffer
	return &RawVideoMKVWriter{
		writer:        bufWriter,
		bufWriter:     bufWriter,
		codecType:     codecType,
		videoTrackNum: 1,
		audioTrackNum: 2,
		done:          make(chan struct{}),
		running:       make(chan struct{}),
	}
}

// initDecoder はデコーダーを初期化
func (w *RawVideoMKVWriter) initDecoder() error {
	var iface *vpx.CodecIface
	switch w.codecType {
	case "vp8":
		iface = vpx.DecoderIfaceVP8()
	case "vp9":
		iface = vpx.DecoderIfaceVP9()
	default:
		return fmt.Errorf("unsupported codec type: %s", w.codecType)
	}

	w.ctx = vpx.NewCodecCtx()
	if err := vpx.Error(vpx.CodecDecInitVer(w.ctx, iface, nil, 0, vpx.DecoderABIVersion)); err != nil {
		return fmt.Errorf("failed to initialize VPX decoder: %w", err)
	}
	w.decoderInit = true
	return nil
}

// WriteVideoFrame はビデオフレームをデコードして書き込む
func (w *RawVideoMKVWriter) WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error {
	if len(data) == 0 {
		return nil
	}

	// 初期化を待つ
	<-w.running

	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.validationStats.TotalFrames++

	// Debug: dump first frame header
	if !w.videoTimestamp.initialized && len(data) >= 10 {
		DebugLog("First frame: len=%d, header=%x, keyframe=%v\n", len(data), data[:10], keyframe)
	}

	// デコーダーがまだ初期化されていない場合
	if !w.decoderInit {
		if err := w.initDecoder(); err != nil {
			return err
		}
	}

	// Calculate timecode in milliseconds
	// PTSはRTP timestampから直接復元し、time.Now()由来の補正は行わない。
	timecodeMs := (w.videoTimestamp.Extend(timestamp) * 1000) / 90000 // 90kHz to ms

	// フレームをデコード
	if err := vpx.Error(vpx.CodecDecode(w.ctx, string(data), uint32(len(data)), nil, 0)); err != nil {
		w.validationStats.DecodeErrors++
		// Debug: dump failed frame header
		if len(data) >= 10 {
			DebugLog("Decode failed (skipping): len=%d, header=%x, keyframe=%v\n", len(data), data[:10], keyframe)
		}
		// デコード失敗時、lastValidFrameがあれば再出力（画面フリーズ効果）
		return w.repeatLastValidFrame(timecodeMs, "decode error")
	}

	// デコードされた画像を取得
	var iter vpx.CodecIter
	img := vpx.CodecGetFrame(w.ctx, &iter)
	if img == nil {
		return nil // フレームがまだ準備できていない
	}
	img.Deref()

	// 解像度が未知の場合、十分な解像度のキーフレームを待ってから確定しヘッダーを書き込む
	// サーバーが最初に低解像度のプレビューキーフレームを送ることがあるため
	frameWidth := int(img.DW)
	frameHeight := int(img.DH)

	if !w.resolutionKnown {
		if !keyframe {
			DebugLog("Waiting for keyframe to determine resolution\n")
			return nil
		}
		// 360p未満の解像度は低解像度プレビューとみなしてスキップ
		if frameWidth < 640 || frameHeight < 360 {
			DebugLog("Skipping low-resolution keyframe: %dx%d (waiting for >= 640x360)\n", frameWidth, frameHeight)
			return nil
		}
		w.width = frameWidth
		w.height = frameHeight
		w.resolutionKnown = true
		DebugLog("Resolution detected from keyframe: %dx%d\n", w.width, w.height)

		// FrameValidatorを初期化
		w.frameValidator = NewFrameValidator(w.width, w.height)

		if err := w.writeHeaders(); err != nil {
			return fmt.Errorf("failed to write headers: %w", err)
		}
	}

	// YUV420からRGBAに変換（ImageRGBAメソッドを使用）
	rgbaImg := img.ImageRGBA()
	rgba := rgbaImg.Pix

	// フレーム品質検証（ノイズ/アーティファクト検出）
	// --no-validate フラグで無効化可能
	if w.frameValidator != nil && !NoFrameValidation {
		result := w.frameValidator.ValidateFrame(rgba, keyframe)
		if !result.IsValid {
			w.validationStats.InvalidFrames++
			w.validationStats.LastInvalidReason = result.Reason
			DebugLog("Frame validation failed: %s (changed=%.2f%%, green=%.2f%%, hist=%.2f%%, block=%.2f%%)\n",
				result.Reason,
				result.ChangedPixelRatio*100,
				result.GreenDominantRatio*100,
				result.HistogramDiff*100,
				result.BlockingScore*100)

			// 破損フレーム検出時、lastValidFrameを再出力
			return w.repeatLastValidFrame(timecodeMs, result.Reason)
		}
	}

	// 検証成功：正常フレームをキャッシュ
	w.validationStats.ValidFrames++
	if w.lastValidFrame == nil || len(w.lastValidFrame) != len(rgba) {
		w.lastValidFrame = make([]byte, len(rgba))
	}
	copy(w.lastValidFrame, rgba)

	// SimpleBlockとして書き込み
	return w.writeSimpleBlock(w.videoTrackNum, rgba, timecodeMs, keyframe)
}

// repeatLastValidFrame は最後の正常フレームを再出力する
func (w *RawVideoMKVWriter) repeatLastValidFrame(timecodeMs uint64, reason string) error {
	if len(w.lastValidFrame) > 0 && w.isHeaderWritten {
		w.validationStats.RepeatedFrames++
		DebugLog("Using cached frame (freeze effect) due to %s: timecode=%dms\n", reason, timecodeMs)
		return w.writeSimpleBlock(w.videoTrackNum, w.lastValidFrame, timecodeMs, false)
	}
	DebugLog("No cached frame available, skipping (reason: %s)\n", reason)
	return nil
}

// GetValidationStats は検証統計を返す
func (w *RawVideoMKVWriter) GetValidationStats() ValidationStats {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.validationStats
}

// WriteAudioFrame はオーディオフレームを書き込む
func (w *RawVideoMKVWriter) WriteAudioFrame(data []byte, timestamp uint32) error {
	if len(data) == 0 {
		return nil
	}

	// 初期化を待つ
	<-w.running

	w.mutex.Lock()
	defer w.mutex.Unlock()

	// ヘッダーがまだ書き込まれていない場合はスキップ
	if !w.isHeaderWritten {
		return nil
	}

	// Calculate timecode in milliseconds
	// PTSはRTP timestampから直接復元し、time.Now()由来の補正は行わない。
	timecodeMs := (w.audioTimestamp.Extend(timestamp) * 1000) / 48000 // 48kHz to ms

	return w.writeSimpleBlock(w.audioTrackNum, data, timecodeMs, false)
}

// Run はメインループを実行
func (w *RawVideoMKVWriter) Run() error {
	w.mutex.Lock()
	w.initialized = true
	w.mutex.Unlock()
	close(w.running)

	// Keep running until Stop() is called
	<-w.done

	// Final flush
	if err := w.bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush final data: %w", err)
	}

	return nil
}

// Close はリソースをクリーンアップ
func (w *RawVideoMKVWriter) Close() error {
	select {
	case <-w.done:
		// Already stopped
	default:
		close(w.done)
	}

	time.Sleep(100 * time.Millisecond)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.decoderInit && w.ctx != nil {
		vpx.CodecDestroy(w.ctx)
		w.ctx = nil
		w.decoderInit = false
	}

	if w.isHeaderWritten {
		return w.bufWriter.Flush()
	}
	return nil
}

// writeHeaders はEBML/MKVヘッダーを書き込む
func (w *RawVideoMKVWriter) writeHeaders() error {
	// Write EBML header
	if err := w.writeEBMLHeader(); err != nil {
		return fmt.Errorf("failed to write EBML header: %w", err)
	}

	// Start segment
	if err := w.writeSegmentHeader(); err != nil {
		return fmt.Errorf("failed to write segment header: %w", err)
	}

	// Write Info
	if err := w.writeInfo(); err != nil {
		return fmt.Errorf("failed to write info: %w", err)
	}

	// Write Tracks
	if err := w.writeTracks(); err != nil {
		return fmt.Errorf("failed to write tracks: %w", err)
	}

	// Flush headers immediately
	if err := w.bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush headers: %w", err)
	}
	w.isHeaderWritten = true

	return nil
}

func (w *RawVideoMKVWriter) writeEBMLHeader() error {
	header := []byte{
		0x1A, 0x45, 0xDF, 0xA3, // EBML
		0x9F,                   // size (31 bytes)
		0x42, 0x86, 0x81, 0x01, // EBMLVersion = 1
		0x42, 0xF7, 0x81, 0x01, // EBMLReadVersion = 1
		0x42, 0xF2, 0x81, 0x04, // EBMLMaxIDLength = 4
		0x42, 0xF3, 0x81, 0x08, // EBMLMaxSizeLength = 8
		0x42, 0x82, 0x88, 0x6D, 0x61, 0x74, 0x72, 0x6F, 0x73, 0x6B, 0x61, // DocType = "matroska"
		0x42, 0x87, 0x81, 0x04, // DocTypeVersion = 4
		0x42, 0x85, 0x81, 0x02, // DocTypeReadVersion = 2
	}
	_, err := w.writer.Write(header)
	return err
}

func (w *RawVideoMKVWriter) writeSegmentHeader() error {
	// Segment with unknown size (0x01FFFFFFFFFFFFFF)
	_, err := w.writer.Write([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	return err
}

func (w *RawVideoMKVWriter) writeInfo() error {
	infoData := &bytes.Buffer{}

	// TimecodeScale (1ms = 1000000ns)
	if err := w.writeEBMLElement(infoData, timecodeScale, w.encodeUInt(1000000)); err != nil {
		return err
	}

	// MuxingApp
	if err := w.writeEBMLElement(infoData, muxingApp, []byte("go-webrtc-whep-client")); err != nil {
		return err
	}

	// WritingApp
	if err := w.writeEBMLElement(infoData, writingApp, []byte("go-webrtc-whep-client")); err != nil {
		return err
	}

	// Write Info element
	return w.writeEBMLElement(w.writer, info, infoData.Bytes())
}

func (w *RawVideoMKVWriter) writeTracks() error {
	tracksData := &bytes.Buffer{}

	// Video track - V_UNCOMPRESSED (RGBA)
	videoEntry := &bytes.Buffer{}
	if err := w.writeEBMLElement(videoEntry, trackNumber, w.encodeUInt(w.videoTrackNum)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(videoEntry, trackUID, w.encodeUInt(w.videoTrackNum)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(videoEntry, trackType, []byte{trackTypeVideo}); err != nil {
		return err
	}
	if err := w.writeEBMLElement(videoEntry, codecID, []byte("V_UNCOMPRESSED")); err != nil {
		return err
	}

	// Video element
	videoSettings := &bytes.Buffer{}
	if err := w.writeEBMLElement(videoSettings, pixelWidth, w.encodeUInt(uint64(w.width))); err != nil {
		return err
	}
	if err := w.writeEBMLElement(videoSettings, pixelHeight, w.encodeUInt(uint64(w.height))); err != nil {
		return err
	}
	// ColourSpace - RGBA (FourCC)
	if err := w.writeEBMLElement(videoSettings, colourSpace, []byte("RGBA")); err != nil {
		return err
	}
	// BitsPerChannel - 8 bits per channel
	if err := w.writeEBMLElement(videoSettings, bitsPerChannel, w.encodeUInt(8)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(videoEntry, video, videoSettings.Bytes()); err != nil {
		return err
	}

	if err := w.writeEBMLElement(tracksData, trackEntry, videoEntry.Bytes()); err != nil {
		return err
	}

	// Audio track - A_OPUS
	audioEntry := &bytes.Buffer{}
	if err := w.writeEBMLElement(audioEntry, trackNumber, w.encodeUInt(w.audioTrackNum)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioEntry, trackUID, w.encodeUInt(w.audioTrackNum)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioEntry, trackType, []byte{trackTypeAudio}); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioEntry, codecID, []byte("A_OPUS")); err != nil {
		return err
	}

	// Audio element
	audioSettings := &bytes.Buffer{}
	if err := w.writeEBMLElement(audioSettings, samplingFrequency, w.encodeFloat(48000)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioSettings, channels, w.encodeUInt(2)); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioEntry, audio, audioSettings.Bytes()); err != nil {
		return err
	}

	if err := w.writeEBMLElement(tracksData, trackEntry, audioEntry.Bytes()); err != nil {
		return err
	}

	// Write Tracks element
	return w.writeEBMLElement(w.writer, tracks, tracksData.Bytes())
}

func (w *RawVideoMKVWriter) writeSimpleBlock(trackNum uint64, data []byte, timecodeMs uint64, keyframe bool) error {
	// Start new cluster on keyframe or every second
	needNewCluster := false
	if keyframe && trackNum == w.videoTrackNum {
		needNewCluster = true
	} else if timecodeMs-w.clusterTime > 1000 || w.clusterTime == 0 {
		needNewCluster = true
	}

	if needNewCluster {
		if err := w.startNewCluster(timecodeMs); err != nil {
			return fmt.Errorf("failed to start new cluster: %w", err)
		}
	}

	block := &bytes.Buffer{}

	// Track number (variable size integer)
	if err := w.writeVarInt(block, trackNum); err != nil {
		return fmt.Errorf("failed to write track number: %w", err)
	}

	// Timecode (relative to cluster)
	relativeTime := int16(timecodeMs - w.clusterTime)
	if err := binary.Write(block, binary.BigEndian, relativeTime); err != nil {
		return fmt.Errorf("failed to write timecode: %w", err)
	}

	// Flags
	flags := byte(0)
	if keyframe {
		flags |= 0x80
	}
	if err := block.WriteByte(flags); err != nil {
		return fmt.Errorf("failed to write flags: %w", err)
	}

	// Frame data
	if _, err := block.Write(data); err != nil {
		return fmt.Errorf("failed to write frame data: %w", err)
	}

	// Write SimpleBlock
	if err := w.writeEBMLElement(w.writer, simpleBlock, block.Bytes()); err != nil {
		return fmt.Errorf("failed to write simple block: %w", err)
	}

	// Flush more frequently for lower latency
	if w.isHeaderWritten && (keyframe || timecodeMs-w.clusterTime > 100) {
		if err := w.bufWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush buffer: %w", err)
		}
	}

	return nil
}

func (w *RawVideoMKVWriter) startNewCluster(timecodeMs uint64) error {
	w.clusterTime = timecodeMs

	// Write Cluster element with unknown size
	if _, err := w.writer.Write([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		return err
	}

	// Write Timecode
	return w.writeEBMLElement(w.writer, timecode, w.encodeUInt(timecodeMs))
}

func (w *RawVideoMKVWriter) writeEBMLElement(wr io.Writer, id uint32, data []byte) error {
	// Write ID
	if err := w.writeEBMLID(wr, id); err != nil {
		return err
	}

	// Write size
	if err := w.writeVarInt(wr, uint64(len(data))); err != nil {
		return err
	}

	// Write data
	_, err := wr.Write(data)
	return err
}

func (w *RawVideoMKVWriter) writeEBMLID(wr io.Writer, id uint32) error {
	if id <= 0xFF {
		_, err := wr.Write([]byte{byte(id)})
		return err
	} else if id <= 0xFFFF {
		return binary.Write(wr, binary.BigEndian, uint16(id))
	} else if id <= 0xFFFFFF {
		_, err := wr.Write([]byte{byte(id >> 16), byte(id >> 8), byte(id)})
		return err
	} else {
		return binary.Write(wr, binary.BigEndian, id)
	}
}

func (w *RawVideoMKVWriter) writeVarInt(wr io.Writer, n uint64) error {
	if n < 127 {
		_, err := wr.Write([]byte{byte(n | 0x80)})
		return err
	} else if n < 16383 {
		_, err := wr.Write([]byte{byte((n >> 8) | 0x40), byte(n)})
		return err
	} else if n < 2097151 {
		_, err := wr.Write([]byte{byte((n >> 16) | 0x20), byte(n >> 8), byte(n)})
		return err
	} else if n < 268435455 {
		_, err := wr.Write([]byte{byte((n >> 24) | 0x10), byte(n >> 16), byte(n >> 8), byte(n)})
		return err
	}
	return fmt.Errorf("VarInt too large: %d", n)
}

func (w *RawVideoMKVWriter) encodeUInt(n uint64) []byte {
	buf := make([]byte, 8)
	size := 0
	for i := 7; i >= 0; i-- {
		if n > 0 || size > 0 {
			buf[size] = byte(n >> (uint(i) * 8))
			size++
		}
	}
	if size == 0 {
		return []byte{0}
	}
	return buf[:size]
}

func (w *RawVideoMKVWriter) encodeFloat(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, *(*uint64)(unsafe.Pointer(&f)))
	return buf
}
