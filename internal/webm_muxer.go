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

	"github.com/pion/webrtc/v4"
)

// Matroska (MKV) EBML IDs
const (
	EBMLHeader  = 0x1A45DFA3
	Segment     = 0x18538067
	Info        = 0x1549A966
	Tracks      = 0x1654AE6B
	Cluster     = 0x1F43B675
	Timecode    = 0xE7
	SimpleBlock = 0xA3

	// Info elements
	TimecodeScale = 0x2AD7B1
	MuxingApp     = 0x4D80
	WritingApp    = 0x5741
	Duration      = 0x4489

	// Track elements
	TrackEntry        = 0xAE
	TrackNumber       = 0xD7
	TrackUID          = 0x73C5
	TrackType         = 0x83
	CodecID           = 0x86
	CodecPrivate      = 0x63A2
	Video             = 0xE0
	Audio             = 0xE1
	PixelWidth        = 0xB0
	PixelHeight       = 0xBA
	SamplingFrequency = 0xB5
	Channels          = 0x9F

	// Track types
	TrackTypeVideo = 0x01
	TrackTypeAudio = 0x02
)

type WebMMuxer struct {
	videoTrack *webrtc.TrackRemote
	audioTrack *webrtc.TrackRemote
	writer     io.Writer

	startTime       time.Time
	clusterTime     uint64
	videoTrackNum   uint64
	audioTrackNum   uint64
	segmentStart    int64
	clusterStart    int64
	currentCluster  []byte
	isHeaderWritten bool
	bufWriter       *bufio.Writer
	firstVideoTS    uint32
	firstAudioTS    uint32
	hasFirstVideoTS bool
	hasFirstAudioTS bool
	sps             []byte
	pps             []byte
	mutex           sync.Mutex
	done            chan struct{}
	errorChan       chan error
	lastKeyframe    uint64
	baseTimestamp   uint32
	hasBaseTS       bool
}

func NewWebMMuxer(w io.Writer, videoTrack, audioTrack *webrtc.TrackRemote) *WebMMuxer {
	bufWriter := bufio.NewWriterSize(w, 4*1024) // 4KB buffer for lower latency
	return &WebMMuxer{
		videoTrack:    videoTrack,
		audioTrack:    audioTrack,
		writer:        bufWriter,
		bufWriter:     bufWriter,
		startTime:     time.Now(),
		videoTrackNum: 1,
		audioTrackNum: 2,
		done:          make(chan struct{}),
		errorChan:     make(chan error, 2),
	}
}

// Initialize はMatroska (MKV) ヘッダーを初期化して書き込む
func (m *WebMMuxer) Initialize() error {
	// Write EBML header
	if err := m.writeEBMLHeader(); err != nil {
		return fmt.Errorf("failed to write EBML header: %w", err)
	}

	// Start segment
	if err := m.writeSegmentHeader(); err != nil {
		return fmt.Errorf("failed to write segment header: %w", err)
	}

	// Write Info
	if err := m.writeInfo(); err != nil {
		return fmt.Errorf("failed to write info: %w", err)
	}

	// Write Tracks
	if err := m.writeTracks(); err != nil {
		return fmt.Errorf("failed to write tracks: %w", err)
	}

	// Flush headers immediately
	if err := m.bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush headers: %w", err)
	}
	m.isHeaderWritten = true

	return nil
}

func (m *WebMMuxer) Run() error {
	defer close(m.done)

	// Keep running until Stop() is called
	<-m.done

	// Final flush
	if err := m.bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush final data: %w", err)
	}

	return nil
}

func (m *WebMMuxer) writeEBMLHeader() error {
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
	_, err := m.writer.Write(header)
	return err
}

func (m *WebMMuxer) writeSegmentHeader() error {
	// Segment with unknown size (0x01FFFFFFFFFFFFFF)
	_, err := m.writer.Write([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	return err
}

func (m *WebMMuxer) writeInfo() error {
	info := &bytes.Buffer{}

	// TimecodeScale (1ms = 1000000ns)
	if err := m.writeEBMLElement(info, TimecodeScale, m.encodeUInt(1000000)); err != nil {
		return err
	}

	// MuxingApp
	if err := m.writeEBMLElement(info, MuxingApp, []byte("go-webrtc-whep-client")); err != nil {
		return err
	}

	// WritingApp
	if err := m.writeEBMLElement(info, WritingApp, []byte("go-webrtc-whep-client")); err != nil {
		return err
	}

	// Write Info element
	return m.writeEBMLElement(m.writer, Info, info.Bytes())
}

func (m *WebMMuxer) writeTracks() error {
	tracks := &bytes.Buffer{}

	// Video track
	if m.videoTrack != nil {
		videoEntry := &bytes.Buffer{}
		if err := m.writeEBMLElement(videoEntry, TrackNumber, m.encodeUInt(m.videoTrackNum)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(videoEntry, TrackUID, m.encodeUInt(m.videoTrackNum)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(videoEntry, TrackType, []byte{TrackTypeVideo}); err != nil {
			return err
		}

		codecID := "V_VP8"
		if m.videoTrack.Codec().MimeType == webrtc.MimeTypeH264 {
			codecID = "V_MPEG4/ISO/AVC"
		} else if m.videoTrack.Codec().MimeType == webrtc.MimeTypeVP9 {
			codecID = "V_VP9"
		}
		if err := m.writeEBMLElement(videoEntry, CodecID, []byte(codecID)); err != nil {
			return err
		}

		// Video element
		videoSettings := &bytes.Buffer{}
		if err := m.writeEBMLElement(videoSettings, PixelWidth, m.encodeUInt(1920)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(videoSettings, PixelHeight, m.encodeUInt(1080)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(videoEntry, Video, videoSettings.Bytes()); err != nil {
			return err
		}

		if err := m.writeEBMLElement(tracks, TrackEntry, videoEntry.Bytes()); err != nil {
			return err
		}
	}

	// Audio track
	if m.audioTrack != nil {
		audioEntry := &bytes.Buffer{}
		if err := m.writeEBMLElement(audioEntry, TrackNumber, m.encodeUInt(m.audioTrackNum)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(audioEntry, TrackUID, m.encodeUInt(m.audioTrackNum)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(audioEntry, TrackType, []byte{TrackTypeAudio}); err != nil {
			return err
		}
		if err := m.writeEBMLElement(audioEntry, CodecID, []byte("A_OPUS")); err != nil {
			return err
		}

		// Audio element
		audioSettings := &bytes.Buffer{}
		if err := m.writeEBMLElement(audioSettings, SamplingFrequency, m.encodeFloat(48000)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(audioSettings, Channels, m.encodeUInt(2)); err != nil {
			return err
		}
		if err := m.writeEBMLElement(audioEntry, Audio, audioSettings.Bytes()); err != nil {
			return err
		}

		if err := m.writeEBMLElement(tracks, TrackEntry, audioEntry.Bytes()); err != nil {
			return err
		}
	}

	// Write Tracks element
	return m.writeEBMLElement(m.writer, Tracks, tracks.Bytes())
}

// processVideoStream and processAudioStream are removed as they are now handled by StreamManager

// Stop gracefully stops the muxer
func (m *WebMMuxer) Stop() error {
	// Signal done to stop goroutines
	select {
	case <-m.done:
		// Already stopped
	default:
		close(m.done)
	}

	// Wait a bit for goroutines to finish
	time.Sleep(100 * time.Millisecond)

	// Final flush
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.isHeaderWritten {
		return m.bufWriter.Flush()
	}
	return nil
}

func (m *WebMMuxer) writeVideoFrame(data []byte, timestamp uint32) error {
	// Calculate timecode in milliseconds using synchronized base timestamp
	m.mutex.Lock()
	baseTS := m.baseTimestamp
	m.mutex.Unlock()

	// Convert to 90kHz base first, then to milliseconds
	relativeTS := timestamp - baseTS
	timecode := uint64(relativeTS) * 1000 / 90000 // 90kHz to ms

	// Detect keyframe for H.264
	keyframe := false
	frameData := data

	if m.videoTrack.Codec().MimeType == webrtc.MimeTypeH264 && len(data) > 4 {
		// Check if frame contains IDR
		hasIDR := false
		hasSPS := false
		hasPPS := false

		// Scan all NAL units in frame
		i := 0
		for i < len(data)-4 {
			// Find start code
			if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
				if i+4 < len(data) {
					nalType := data[i+4] & 0x1F
					if nalType == 5 {
						hasIDR = true
					} else if nalType == 7 {
						hasSPS = true
					} else if nalType == 8 {
						hasPPS = true
					}
				}
				i += 4
			} else {
				i++
			}
		}

		keyframe = hasIDR

		// If IDR frame but missing SPS/PPS, prepend them
		if hasIDR && (!hasSPS || !hasPPS) {
			m.mutex.Lock()
			if m.sps != nil && m.pps != nil {
				// Prepend SPS and PPS
				newData := make([]byte, 0, len(data)+len(m.sps)+len(m.pps)+8)
				newData = append(newData, 0x00, 0x00, 0x00, 0x01)
				newData = append(newData, m.sps...)
				newData = append(newData, 0x00, 0x00, 0x00, 0x01)
				newData = append(newData, m.pps...)
				newData = append(newData, data...)
				frameData = newData
			}
			m.mutex.Unlock()
		}
	} else if m.videoTrack.Codec().MimeType == webrtc.MimeTypeVP8 {
		// VP8 keyframe detection
		if len(data) > 0 {
			// VP8 payload descriptor parsing
			partitionStartBit := (data[0] & 0x10) != 0
			if partitionStartBit && len(data) > 1 {
				// Check if it's a keyframe
				keyframe = (data[1] & 0x01) == 0
			}
		}
	} else if m.videoTrack.Codec().MimeType == webrtc.MimeTypeVP9 {
		// VP9 keyframe detection
		if len(data) > 0 {
			// VP9 payload descriptor parsing (simplified)
			// Check for P bit (0 = keyframe)
			keyframe = (data[0] & 0x40) == 0
		}
	}

	if keyframe {
		m.lastKeyframe = timecode
	}

	return m.writeSimpleBlock(m.videoTrackNum, frameData, timecode, keyframe)
}

func (m *WebMMuxer) writeAudioFrame(data []byte, timestamp uint32) error {
	// Calculate timecode in milliseconds using synchronized base timestamp
	m.mutex.Lock()
	baseTS := m.baseTimestamp
	m.mutex.Unlock()

	// Opus uses 48kHz clock rate
	relativeTS := timestamp - baseTS
	timecode := uint64(relativeTS) * 1000 / 48000 // 48kHz to ms

	return m.writeSimpleBlock(m.audioTrackNum, data, timecode, false)
}

func (m *WebMMuxer) writeSimpleBlock(trackNum uint64, data []byte, timecode uint64, keyframe bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Start new cluster on keyframe or every second
	needNewCluster := false
	if keyframe && trackNum == m.videoTrackNum {
		// Always start new cluster on keyframe
		needNewCluster = true
	} else if timecode-m.clusterTime > 1000 || m.clusterTime == 0 {
		// Also start new cluster every second
		needNewCluster = true
	}

	if needNewCluster {
		if err := m.startNewCluster(timecode); err != nil {
			return fmt.Errorf("failed to start new cluster: %w", err)
		}
	}

	block := &bytes.Buffer{}

	// Track number (variable size integer)
	if err := m.writeVarInt(block, trackNum); err != nil {
		return fmt.Errorf("failed to write track number: %w", err)
	}

	// Timecode (relative to cluster)
	relativeTime := int16(timecode - m.clusterTime)
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
	if err := m.writeEBMLElement(m.writer, SimpleBlock, block.Bytes()); err != nil {
		return fmt.Errorf("failed to write simple block: %w", err)
	}

	// Flush more frequently for lower latency
	// Flush on keyframes or every 100ms (instead of 500ms)
	if m.isHeaderWritten && (keyframe || timecode-m.lastKeyframe > 100) {
		if err := m.bufWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush buffer: %w", err)
		}
	}

	return nil
}

func (m *WebMMuxer) startNewCluster(timecode uint64) error {
	m.clusterTime = timecode

	// Write Cluster element with unknown size
	if _, err := m.writer.Write([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		return err
	}

	// Write Timecode
	return m.writeEBMLElement(m.writer, Timecode, m.encodeUInt(timecode))
}

func (m *WebMMuxer) writeEBMLElement(w io.Writer, id uint32, data []byte) error {
	// Write ID
	if err := m.writeEBMLID(w, id); err != nil {
		return err
	}

	// Write size
	if err := m.writeVarInt(w, uint64(len(data))); err != nil {
		return err
	}

	// Write data
	_, err := w.Write(data)
	return err
}

func (m *WebMMuxer) writeEBMLID(w io.Writer, id uint32) error {
	if id <= 0xFF {
		_, err := w.Write([]byte{byte(id)})
		return err
	} else if id <= 0xFFFF {
		return binary.Write(w, binary.BigEndian, uint16(id))
	} else if id <= 0xFFFFFF {
		_, err := w.Write([]byte{byte(id >> 16), byte(id >> 8), byte(id)})
		return err
	} else {
		return binary.Write(w, binary.BigEndian, id)
	}
}

func (m *WebMMuxer) writeVarInt(w io.Writer, n uint64) error {
	if n < 127 {
		_, err := w.Write([]byte{byte(n | 0x80)})
		return err
	} else if n < 16383 {
		_, err := w.Write([]byte{byte((n >> 8) | 0x40), byte(n)})
		return err
	} else if n < 2097151 {
		_, err := w.Write([]byte{byte((n >> 16) | 0x20), byte(n >> 8), byte(n)})
		return err
	} else if n < 268435455 {
		_, err := w.Write([]byte{byte((n >> 24) | 0x10), byte(n >> 16), byte(n >> 8), byte(n)})
		return err
	}
	return fmt.Errorf("VarInt too large: %d", n)
}

func (m *WebMMuxer) encodeUInt(n uint64) []byte {
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

func (m *WebMMuxer) encodeFloat(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, *(*uint64)(unsafe.Pointer(&f)))
	return buf
}

func (m *WebMMuxer) encodeFloat32(f float32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, *(*uint32)(unsafe.Pointer(&f)))
	return buf
}
