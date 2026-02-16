package internal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

type FrameType int

const (
	FrameTypeVideo FrameType = iota
	FrameTypeAudio
)

type Frame struct {
	Type              FrameType
	Data              []byte
	TimestampMs       int64
	IsKeyframe        bool
	ClusterTimeMs     int64
	BlockRelativeTsMs int64
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

// EBML/Matroska element IDs used in this stream path.
const (
	ebmlIDSegment          = 0x18538067
	ebmlIDInfo             = 0x1549A966
	ebmlIDTracks           = 0x1654AE6B
	ebmlIDCluster          = 0x1F43B675
	ebmlIDTrackEntry       = 0xAE
	ebmlIDVideo            = 0xE0
	ebmlIDAudio            = 0xE1
	ebmlIDTrackNumber      = 0xD7
	ebmlIDCodecID          = 0x86
	ebmlIDPixelWidth       = 0xB0
	ebmlIDPixelHeight      = 0xBA
	ebmlIDTimecode         = 0xE7
	ebmlIDTimecodeScale    = 0x2AD7B1
	ebmlIDChannels         = 0x9F
	ebmlIDSamplingFreq     = 0xB5
	ebmlIDColourSpace      = 0x2EB524
	ebmlIDSimpleBlock      = 0xA3
	ebmlIDBlock            = 0xA1
	maxEBMLSizeVintBytes   = 8
	maxEBMLIDVintBytes     = 4
	defaultParserBufSize   = 256 * 1024
	maxReasonableFieldSize = 64 * 1024 * 1024
	frameSendTimeout       = 5 * time.Second
)

type mkvContainer struct {
	id  uint64
	end int64
}

type mkvStreamParser struct {
	reader *MKVReader
	br     *bufio.Reader
	offset int64

	stack []mkvContainer

	currentTrackNumber int64
	currentTrackType   string
	currentClusterTime int64

	inTrackEntry bool
	inVideo      bool
	inAudio      bool
}

func (r *MKVReader) parse() {
	defer close(r.frames)

	parser := &mkvStreamParser{
		reader:             r,
		br:                 bufio.NewReaderSize(r.reader, defaultParserBufSize),
		currentTrackNumber: 0,
	}
	if err := parser.parse(); err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
}

func (p *mkvStreamParser) parse() error {
	for {
		p.popExpiredContainers()

		id, err := p.readElementID()
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.closeRemainingContainers()
				return nil
			}
			return err
		}

		size, unknownSize, err := p.readElementSize()
		if err != nil {
			return err
		}

		if p.isMasterElement(id) {
			if !unknownSize {
				p.pushContainer(id, size)
			}
			continue
		}

		if err := p.handleElementData(id, size); err != nil {
			return err
		}
	}
}

func (p *mkvStreamParser) isMasterElement(id uint64) bool {
	switch id {
	case ebmlIDSegment, ebmlIDInfo, ebmlIDTracks, ebmlIDCluster, ebmlIDTrackEntry, ebmlIDVideo, ebmlIDAudio:
		return true
	default:
		return false
	}
}

func (p *mkvStreamParser) pushContainer(id uint64, size int64) {
	container := mkvContainer{
		id:  id,
		end: p.offset + size,
	}
	p.stack = append(p.stack, container)

	switch id {
	case ebmlIDTrackEntry:
		p.inTrackEntry = true
		p.currentTrackNumber = 0
		p.currentTrackType = ""
	case ebmlIDVideo:
		p.inVideo = true
	case ebmlIDAudio:
		p.inAudio = true
	}
}

func (p *mkvStreamParser) popExpiredContainers() {
	for len(p.stack) > 0 {
		last := p.stack[len(p.stack)-1]
		if p.offset < last.end {
			return
		}
		p.stack = p.stack[:len(p.stack)-1]
		p.onContainerEnd(last.id)
	}
}

func (p *mkvStreamParser) closeRemainingContainers() {
	for i := len(p.stack) - 1; i >= 0; i-- {
		p.onContainerEnd(p.stack[i].id)
	}
	p.stack = p.stack[:0]
}

func (p *mkvStreamParser) onContainerEnd(id uint64) {
	switch id {
	case ebmlIDTrackEntry:
		switch p.currentTrackType {
		case "V_UNCOMPRESSED", "V_VP8", "V_VP9":
			p.reader.videoTrackNumber = p.currentTrackNumber
			DebugLog("Video track number: %d, codec: %s\n", p.currentTrackNumber, p.currentTrackType)
		case "A_OPUS", "A_PCM/INT/LIT":
			p.reader.audioTrackNumber = p.currentTrackNumber
			p.reader.audioCodec = p.currentTrackType
			DebugLog("Audio track number: %d, codec: %s\n", p.currentTrackNumber, p.currentTrackType)
		}
		p.inTrackEntry = false
	case ebmlIDVideo:
		p.inVideo = false
	case ebmlIDAudio:
		p.inAudio = false
	}
}

func (p *mkvStreamParser) handleElementData(id uint64, size int64) error {
	if size < 0 {
		return fmt.Errorf("invalid negative element size: id=%x size=%d", id, size)
	}
	if size > maxReasonableFieldSize && id != ebmlIDSimpleBlock && id != ebmlIDBlock {
		return fmt.Errorf("unexpectedly large element: id=%x size=%d", id, size)
	}

	switch id {
	case ebmlIDTrackNumber:
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		if p.inTrackEntry {
			p.currentTrackNumber = int64(value)
		}
		return nil

	case ebmlIDCodecID:
		value, err := p.readString(size)
		if err != nil {
			return err
		}
		if p.inTrackEntry {
			p.currentTrackType = value
		}
		return nil

	case ebmlIDPixelWidth:
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		if p.inVideo {
			p.reader.videoWidth = int(value)
		}
		return nil

	case ebmlIDPixelHeight:
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		if p.inVideo {
			p.reader.videoHeight = int(value)
		}
		return nil

	case ebmlIDTimecode:
		// Cluster Timecode is an unsigned integer in Matroska.
		// Signedとして読むと 32767ms 超で負値化し、PTSが巻き戻る。
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		p.currentClusterTime = int64(value)
		return nil

	case ebmlIDTimecodeScale:
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		p.reader.timescale = value
		return nil

	case ebmlIDChannels:
		value, err := p.readUnsignedInt(size)
		if err != nil {
			return err
		}
		if p.inAudio {
			p.reader.audioChannels = int(value)
			DebugLog("Audio channels: %d\n", value)
		}
		return nil

	case ebmlIDSamplingFreq:
		value, err := p.readFloat(size)
		if err != nil {
			return err
		}
		if p.inAudio {
			p.reader.audioSampleRate = int(value)
			DebugLog("Audio sample rate: %d\n", int(value))
		}
		return nil

	case ebmlIDColourSpace:
		value, err := p.readString(size)
		if err != nil {
			return err
		}
		if p.inVideo {
			p.reader.pixelFormat = value
			DebugLog("Video pixel format: %s\n", value)
		}
		return nil

	case ebmlIDSimpleBlock, ebmlIDBlock:
		data, err := p.readBytes(size)
		if err != nil {
			return err
		}
		return p.handleSimpleBlock(data)

	default:
		return p.discard(size)
	}
}

func (p *mkvStreamParser) handleSimpleBlock(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("simple block too short")
	}

	trackNum, trackNumSize := parseVint(data)
	if trackNumSize == 0 {
		return fmt.Errorf("invalid track number in simple block")
	}
	if len(data) < trackNumSize+3 {
		return fmt.Errorf("simple block too short after track number")
	}

	relativeTs := int16(binary.BigEndian.Uint16(data[trackNumSize : trackNumSize+2]))
	flags := data[trackNumSize+2]
	isKeyframe := (flags & 0x80) != 0
	frameData := data[trackNumSize+3:]
	timestampMs := p.currentClusterTime + int64(relativeTs)

	var frameType FrameType
	switch int64(trackNum) {
	case p.reader.videoTrackNumber:
		frameType = FrameTypeVideo
	case p.reader.audioTrackNumber:
		frameType = FrameTypeAudio
	default:
		return nil
	}

	frame := &Frame{
		Type:              frameType,
		Data:              frameData,
		TimestampMs:       timestampMs,
		IsKeyframe:        isKeyframe,
		ClusterTimeMs:     p.currentClusterTime,
		BlockRelativeTsMs: int64(relativeTs),
	}
	return p.sendFrame(frame)
}

func (p *mkvStreamParser) sendFrame(frame *Frame) error {
	select {
	case p.reader.frames <- frame:
		return nil
	default:
	}

	timer := time.NewTimer(frameSendTimeout)
	defer timer.Stop()

	select {
	case p.reader.frames <- frame:
		return nil
	case <-timer.C:
		return fmt.Errorf("timeout sending frame")
	}
}

func (p *mkvStreamParser) readElementID() (uint64, error) {
	first, err := p.readByte()
	if err != nil {
		return 0, err
	}

	length := 1
	mask := byte(0x80)
	for length <= maxEBMLIDVintBytes && (first&mask) == 0 {
		mask >>= 1
		length++
	}
	if length > maxEBMLIDVintBytes {
		return 0, fmt.Errorf("invalid element ID first byte: 0x%02x", first)
	}

	id := uint64(first)
	for i := 1; i < length; i++ {
		b, err := p.readByte()
		if err != nil {
			return 0, err
		}
		id = (id << 8) | uint64(b)
	}

	return id, nil
}

func (p *mkvStreamParser) readElementSize() (int64, bool, error) {
	first, err := p.readByte()
	if err != nil {
		return 0, false, err
	}

	length := 1
	mask := byte(0x80)
	for length <= maxEBMLSizeVintBytes && (first&mask) == 0 {
		mask >>= 1
		length++
	}
	if length > maxEBMLSizeVintBytes {
		return 0, false, fmt.Errorf("invalid size first byte: 0x%02x", first)
	}

	value := uint64(first & (mask - 1))
	unknown := value == uint64(mask-1)
	for i := 1; i < length; i++ {
		b, err := p.readByte()
		if err != nil {
			return 0, false, err
		}
		value = (value << 8) | uint64(b)
		if b != 0xFF {
			unknown = false
		}
	}

	if unknown {
		return 0, true, nil
	}
	if value > math.MaxInt64 {
		return 0, false, fmt.Errorf("element size too large: %d", value)
	}

	return int64(value), false, nil
}

func (p *mkvStreamParser) readByte() (byte, error) {
	b, err := p.br.ReadByte()
	if err != nil {
		return 0, err
	}
	p.offset++
	return b, nil
}

func (p *mkvStreamParser) readBytes(size int64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	if size < 0 {
		return nil, fmt.Errorf("invalid read size: %d", size)
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(p.br, buf); err != nil {
		return nil, err
	}
	p.offset += size
	return buf, nil
}

func (p *mkvStreamParser) discard(size int64) error {
	if size == 0 {
		return nil
	}
	if size < 0 {
		return fmt.Errorf("invalid discard size: %d", size)
	}

	n, err := io.CopyN(io.Discard, p.br, size)
	p.offset += n
	if err != nil {
		return err
	}
	if n != size {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (p *mkvStreamParser) readUnsignedInt(size int64) (uint64, error) {
	if size <= 0 || size > 8 {
		return 0, fmt.Errorf("invalid integer size: %d", size)
	}

	buf, err := p.readBytes(size)
	if err != nil {
		return 0, err
	}

	var v uint64
	for _, b := range buf {
		v = (v << 8) | uint64(b)
	}
	return v, nil
}

func (p *mkvStreamParser) readSignedInt(size int64) (int64, error) {
	if size <= 0 || size > 8 {
		return 0, fmt.Errorf("invalid signed integer size: %d", size)
	}

	buf, err := p.readBytes(size)
	if err != nil {
		return 0, err
	}

	var u uint64
	for _, b := range buf {
		u = (u << 8) | uint64(b)
	}
	shift := uint((8 - size) * 8)
	return int64(u<<shift) >> shift, nil
}

func (p *mkvStreamParser) readFloat(size int64) (float64, error) {
	if size != 4 && size != 8 {
		return 0, fmt.Errorf("unsupported float size: %d", size)
	}
	buf, err := p.readBytes(size)
	if err != nil {
		return 0, err
	}
	if size == 4 {
		return float64(math.Float32frombits(binary.BigEndian.Uint32(buf))), nil
	}
	return math.Float64frombits(binary.BigEndian.Uint64(buf)), nil
}

func (p *mkvStreamParser) readString(size int64) (string, error) {
	buf, err := p.readBytes(size)
	if err != nil {
		return "", err
	}
	for len(buf) > 0 && buf[len(buf)-1] == 0 {
		buf = buf[:len(buf)-1]
	}
	return string(buf), nil
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
