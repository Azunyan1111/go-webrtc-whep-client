package internal

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/remko/go-mkvparse"
)

type FrameType int

const (
	FrameTypeVideo FrameType = iota
	FrameTypeAudio
)

type Frame struct {
	Type        FrameType
	Data        []byte
	TimestampMs int64
	IsKeyframe  bool
}

type MKVReader struct {
	reader           io.Reader
	videoWidth       int
	videoHeight      int
	videoTrackNumber int64
	audioTrackNumber int64
	timescale        uint64
	frames           chan *Frame
	err              error
	started          bool
	pixelFormat      string
	audioCodec       string
	audioSampleRate  int
	audioChannels    int
}

func NewMKVReader(reader io.Reader) *MKVReader {
	return &MKVReader{
		reader:           reader,
		frames:           make(chan *Frame, 100),
		timescale:        1000000, // Default to 1ms
		videoTrackNumber: -1,
		audioTrackNumber: -1,
		pixelFormat:      "RGBA",
	}
}

func (r *MKVReader) VideoWidth() int {
	return r.videoWidth
}

func (r *MKVReader) VideoHeight() int {
	return r.videoHeight
}

func (r *MKVReader) PixelFormat() string {
	return r.pixelFormat
}

func (r *MKVReader) AudioCodec() string {
	return r.audioCodec
}

func (r *MKVReader) AudioSampleRate() int {
	return r.audioSampleRate
}

func (r *MKVReader) AudioChannels() int {
	return r.audioChannels
}

func (r *MKVReader) Start() {
	if r.started {
		return
	}
	r.started = true
	go r.parse()
}

func (r *MKVReader) ReadFrame() (*Frame, error) {
	if !r.started {
		r.Start()
	}
	frame, ok := <-r.frames
	if !ok {
		if r.err != nil {
			return nil, r.err
		}
		return nil, io.EOF
	}
	return frame, nil
}

func (r *MKVReader) parse() {
	defer close(r.frames)
	handler := &mkvHandler{reader: r}
	err := mkvparse.Parse(r.reader, handler)
	if err != nil && err != io.EOF {
		r.err = err
	}
}

type mkvHandler struct {
	mkvparse.DefaultHandler
	reader             *MKVReader
	currentTrackNumber int64
	currentTrackType   string
	currentClusterTime int64
	inTrackEntry       bool
	inVideo            bool
	inAudio            bool
	inColour           bool
}

func (h *mkvHandler) HandleMasterBegin(id mkvparse.ElementID, info mkvparse.ElementInfo) (bool, error) {
	switch id {
	case mkvparse.TrackEntryElement:
		h.inTrackEntry = true
		h.currentTrackNumber = 0
		h.currentTrackType = ""
	case mkvparse.VideoElement:
		h.inVideo = true
	case mkvparse.AudioElement:
		h.inAudio = true
	case mkvparse.ColourElement:
		h.inColour = true
	case mkvparse.ClusterElement:
		// Reset cluster time
	}
	return true, nil
}

func (h *mkvHandler) HandleMasterEnd(id mkvparse.ElementID, info mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.TrackEntryElement:
		if h.currentTrackType == "V_UNCOMPRESSED" || h.currentTrackType == "V_VP8" || h.currentTrackType == "V_VP9" {
			h.reader.videoTrackNumber = h.currentTrackNumber
			DebugLog("Video track number: %d, codec: %s\n", h.currentTrackNumber, h.currentTrackType)
		} else if h.currentTrackType == "A_OPUS" || h.currentTrackType == "A_PCM/INT/LIT" {
			h.reader.audioTrackNumber = h.currentTrackNumber
			h.reader.audioCodec = h.currentTrackType
			DebugLog("Audio track number: %d, codec: %s\n", h.currentTrackNumber, h.currentTrackType)
		}
		h.inTrackEntry = false
	case mkvparse.VideoElement:
		h.inVideo = false
	case mkvparse.AudioElement:
		h.inAudio = false
	case mkvparse.ColourElement:
		h.inColour = false
	}
	return nil
}

func (h *mkvHandler) HandleInteger(id mkvparse.ElementID, value int64, info mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.TrackNumberElement:
		if h.inTrackEntry {
			h.currentTrackNumber = value
		}
	case mkvparse.PixelWidthElement:
		if h.inVideo {
			h.reader.videoWidth = int(value)
		}
	case mkvparse.PixelHeightElement:
		if h.inVideo {
			h.reader.videoHeight = int(value)
		}
	case mkvparse.TimecodeElement:
		h.currentClusterTime = value
	case mkvparse.TimecodeScaleElement:
		h.reader.timescale = uint64(value)
	case mkvparse.ChannelsElement:
		if h.inAudio {
			h.reader.audioChannels = int(value)
			DebugLog("Audio channels: %d\n", value)
		}
	}
	return nil
}

func (h *mkvHandler) HandleFloat(id mkvparse.ElementID, value float64, info mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.SamplingFrequencyElement:
		if h.inAudio {
			h.reader.audioSampleRate = int(value)
			DebugLog("Audio sample rate: %d\n", int(value))
		}
	}
	return nil
}

func (h *mkvHandler) HandleString(id mkvparse.ElementID, value string, info mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.CodecIDElement:
		if h.inTrackEntry {
			h.currentTrackType = value
		}
	}
	return nil
}

func (h *mkvHandler) HandleBinary(id mkvparse.ElementID, data []byte, info mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.SimpleBlockElement:
		return h.handleSimpleBlock(data)
	case mkvparse.BlockElement:
		return h.handleSimpleBlock(data)
	case mkvparse.ColourSpaceElement:
		if h.inVideo {
			h.reader.pixelFormat = string(data)
			DebugLog("Video pixel format: %s\n", string(data))
		}
	}
	return nil
}

func (h *mkvHandler) handleSimpleBlock(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("simple block too short")
	}

	// Parse track number (variable size integer)
	trackNum, trackNumSize := parseVint(data)
	if trackNumSize == 0 {
		return fmt.Errorf("invalid track number in simple block")
	}

	if len(data) < trackNumSize+3 {
		return fmt.Errorf("simple block too short after track number")
	}

	// Parse relative timestamp (2 bytes, signed)
	relativeTs := int16(binary.BigEndian.Uint16(data[trackNumSize : trackNumSize+2]))

	// Parse flags (1 byte)
	flags := data[trackNumSize+2]
	isKeyframe := (flags & 0x80) != 0

	// Frame data starts after header
	frameData := data[trackNumSize+3:]

	// Calculate absolute timestamp in milliseconds
	timestampMs := h.currentClusterTime + int64(relativeTs)

	// Determine frame type
	var frameType FrameType
	if int64(trackNum) == h.reader.videoTrackNumber {
		frameType = FrameTypeVideo
	} else if int64(trackNum) == h.reader.audioTrackNumber {
		frameType = FrameTypeAudio
	} else {
		// Unknown track, skip
		return nil
	}

	frame := &Frame{
		Type:        frameType,
		Data:        make([]byte, len(frameData)),
		TimestampMs: timestampMs,
		IsKeyframe:  isKeyframe,
	}
	copy(frame.Data, frameData)

	select {
	case h.reader.frames <- frame:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout sending frame")
	}

	return nil
}

func parseVint(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	first := data[0]
	var size int
	var mask byte

	switch {
	case first&0x80 != 0:
		size = 1
		mask = 0x7F
	case first&0x40 != 0:
		size = 2
		mask = 0x3F
	case first&0x20 != 0:
		size = 3
		mask = 0x1F
	case first&0x10 != 0:
		size = 4
		mask = 0x0F
	default:
		return 0, 0
	}

	if len(data) < size {
		return 0, 0
	}

	value := uint64(first & mask)
	for i := 1; i < size; i++ {
		value = (value << 8) | uint64(data[i])
	}

	return value, size
}
