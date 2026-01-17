// Package mkvwriter provides MKV output for decoded video/audio frames
package mkvwriter

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
	"unsafe"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal/libwebrtc"
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
	bitDepth          = 0x6264

	// Track types
	trackTypeVideo = 0x01
	trackTypeAudio = 0x02
)

// DebugLog is a placeholder for debug logging
var DebugLog = func(format string, args ...interface{}) {}

// DecodedMKVWriter outputs decoded RGBA video and PCM audio to MKV format
type DecodedMKVWriter struct {
	writer          io.Writer
	bufWriter       *bufio.Writer
	width           int
	height          int
	sampleRate      int
	audioChannels   int
	resolutionKnown bool
	isHeaderWritten bool
	videoTrackNum   uint64
	audioTrackNum   uint64
	clusterTime     uint64
	// Separate base timestamps for video and audio
	videoBaseTs  int64
	audioBaseTs  int64
	hasVideoBase bool
	hasAudioBase bool
	// Wall clock time for synchronization
	startWallTime int64
	mutex         sync.Mutex
	done          chan struct{}
	running       chan struct{}
	initialized   bool
}

// NewDecodedMKVWriter creates a new DecodedMKVWriter
func NewDecodedMKVWriter(w io.Writer) *DecodedMKVWriter {
	bufWriter := bufio.NewWriterSize(w, 64*1024) // 64KB buffer
	return &DecodedMKVWriter{
		writer:        bufWriter,
		bufWriter:     bufWriter,
		videoTrackNum: 1,
		audioTrackNum: 2,
		sampleRate:    48000, // default
		audioChannels: 2,     // default stereo
		done:          make(chan struct{}),
		running:       make(chan struct{}),
	}
}

// WriteVideoFrame writes a decoded video frame (I420 format from libwebrtc)
func (w *DecodedMKVWriter) WriteVideoFrame(frame *libwebrtc.VideoFrame) error {
	if frame == nil {
		return nil
	}

	// Wait for initialization
	<-w.running

	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Detect resolution from first frame
	if !w.resolutionKnown {
		// Skip low-resolution preview frames
		if frame.Width < 640 || frame.Height < 360 {
			DebugLog("Skipping low-resolution frame: %dx%d (waiting for >= 640x360)\n", frame.Width, frame.Height)
			return nil
		}
		w.width = frame.Width
		w.height = frame.Height
		w.resolutionKnown = true
		DebugLog("Resolution detected: %dx%d\n", w.width, w.height)

		if err := w.writeHeaders(); err != nil {
			return fmt.Errorf("failed to write headers: %w", err)
		}
	}

	// Initialize base timestamp and wall clock time
	if !w.hasVideoBase {
		w.videoBaseTs = frame.TimestampUs
		w.hasVideoBase = true
		// Use wall clock for synchronization if this is the first frame
		if w.startWallTime == 0 {
			w.startWallTime = time.Now().UnixMicro()
		}
		DebugLog("Base video timestamp: %d us, wall time: %d us\n", w.videoBaseTs, w.startWallTime)
	}

	// Calculate timecode in milliseconds using wall clock time for sync
	// This ensures playback at correct speed regardless of source timestamps
	wallElapsed := time.Now().UnixMicro() - w.startWallTime
	timecodeMs := uint64(wallElapsed / 1000)

	// Convert I420 to RGBA
	rgba, err := libwebrtc.I420ToRGBA(frame)
	if err != nil {
		DebugLog("I420 to RGBA conversion failed: %v\n", err)
		return nil
	}

	// Determine if this is a keyframe (for MKV we can treat all RGBA frames as keyframes)
	keyframe := true

	return w.writeSimpleBlock(w.videoTrackNum, rgba, timecodeMs, keyframe)
}

// WriteAudioFrame writes a decoded audio frame (PCM from libwebrtc)
func (w *DecodedMKVWriter) WriteAudioFrame(frame *libwebrtc.AudioFrame) error {
	if frame == nil || len(frame.PCM) == 0 {
		return nil
	}

	// Wait for initialization
	<-w.running

	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Skip if header not written yet
	if !w.isHeaderWritten {
		return nil
	}

	// Update audio parameters if different
	if frame.SampleRate != w.sampleRate || frame.Channels != w.audioChannels {
		w.sampleRate = frame.SampleRate
		w.audioChannels = frame.Channels
	}

	// Initialize base timestamp and wall clock time
	if !w.hasAudioBase {
		w.audioBaseTs = frame.TimestampUs
		w.hasAudioBase = true
		// Use wall clock for synchronization if this is the first frame
		if w.startWallTime == 0 {
			w.startWallTime = time.Now().UnixMicro()
		}
		DebugLog("Base audio timestamp: %d us, wall time: %d us\n", w.audioBaseTs, w.startWallTime)
	}

	// Calculate timecode in milliseconds using wall clock time for sync
	wallElapsed := time.Now().UnixMicro() - w.startWallTime
	timecodeMs := uint64(wallElapsed / 1000)

	// Convert int16 PCM to bytes (little-endian)
	data := make([]byte, len(frame.PCM)*2)
	for i, sample := range frame.PCM {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}

	return w.writeSimpleBlock(w.audioTrackNum, data, timecodeMs, false)
}

// Run starts the main loop
func (w *DecodedMKVWriter) Run() error {
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

// Close cleans up resources
func (w *DecodedMKVWriter) Close() error {
	select {
	case <-w.done:
		// Already stopped
	default:
		close(w.done)
	}

	time.Sleep(100 * time.Millisecond)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.isHeaderWritten {
		return w.bufWriter.Flush()
	}
	return nil
}

// writeHeaders writes EBML/MKV headers
func (w *DecodedMKVWriter) writeHeaders() error {
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

func (w *DecodedMKVWriter) writeEBMLHeader() error {
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

func (w *DecodedMKVWriter) writeSegmentHeader() error {
	// Segment with unknown size (0x01FFFFFFFFFFFFFF)
	_, err := w.writer.Write([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	return err
}

func (w *DecodedMKVWriter) writeInfo() error {
	infoData := &bytes.Buffer{}

	// TimecodeScale (1ms = 1000000ns)
	if err := w.writeEBMLElement(infoData, timecodeScale, w.encodeUInt(1000000)); err != nil {
		return err
	}

	// MuxingApp
	if err := w.writeEBMLElement(infoData, muxingApp, []byte("go-webrtc-whep-client-libwebrtc")); err != nil {
		return err
	}

	// WritingApp
	if err := w.writeEBMLElement(infoData, writingApp, []byte("go-webrtc-whep-client-libwebrtc")); err != nil {
		return err
	}

	// Write Info element
	return w.writeEBMLElement(w.writer, info, infoData.Bytes())
}

func (w *DecodedMKVWriter) writeTracks() error {
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

	// Audio track - A_PCM/INT/LIT (PCM signed 16-bit little-endian)
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
	if err := w.writeEBMLElement(audioEntry, codecID, []byte("A_PCM/INT/LIT")); err != nil {
		return err
	}

	// Audio element
	audioSettings := &bytes.Buffer{}
	if err := w.writeEBMLElement(audioSettings, samplingFrequency, w.encodeFloat(float64(w.sampleRate))); err != nil {
		return err
	}
	if err := w.writeEBMLElement(audioSettings, channels, w.encodeUInt(uint64(w.audioChannels))); err != nil {
		return err
	}
	// BitDepth - 16 bits per sample
	if err := w.writeEBMLElement(audioSettings, bitDepth, w.encodeUInt(16)); err != nil {
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

func (w *DecodedMKVWriter) writeSimpleBlock(trackNum uint64, data []byte, timecodeMs uint64, keyframe bool) error {
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

func (w *DecodedMKVWriter) startNewCluster(timecodeMs uint64) error {
	w.clusterTime = timecodeMs

	// Write Cluster element with unknown size
	if _, err := w.writer.Write([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		return err
	}

	// Write Timecode
	return w.writeEBMLElement(w.writer, timecode, w.encodeUInt(timecodeMs))
}

func (w *DecodedMKVWriter) writeEBMLElement(wr io.Writer, id uint32, data []byte) error {
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

func (w *DecodedMKVWriter) writeEBMLID(wr io.Writer, id uint32) error {
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

func (w *DecodedMKVWriter) writeVarInt(wr io.Writer, n uint64) error {
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

func (w *DecodedMKVWriter) encodeUInt(n uint64) []byte {
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

func (w *DecodedMKVWriter) encodeFloat(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, *(*uint64)(unsafe.Pointer(&f)))
	return buf
}
