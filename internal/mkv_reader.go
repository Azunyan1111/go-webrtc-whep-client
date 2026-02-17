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

const (
	lacingNone  = 0
	lacingXiph  = 1
	lacingFixed = 2
	lacingEBML  = 3
)

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
	lacingMode := int((flags & 0x06) >> 1)
	frameData := data[trackNumSize+3:]
	clusterTimeMs := p.scaleTicksToMilliseconds(p.currentClusterTime)
	blockRelativeTsMs := p.scaleTicksToMilliseconds(int64(relativeTs))
	timestampMs := clusterTimeMs + blockRelativeTsMs

	var frameType FrameType
	switch int64(trackNum) {
	case p.reader.videoTrackNumber:
		frameType = FrameTypeVideo
	case p.reader.audioTrackNumber:
		frameType = FrameTypeAudio
	default:
		return nil
	}

	frames, err := p.parseLacedFrames(frameData, lacingMode)
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return nil
	}

	runningTsMs := timestampMs
	for idx, payload := range frames {
		frame := &Frame{
			Type:              frameType,
			Data:              payload,
			TimestampMs:       runningTsMs,
			IsKeyframe:        isKeyframe && idx == 0,
			ClusterTimeMs:     clusterTimeMs,
			BlockRelativeTsMs: blockRelativeTsMs,
		}
		if err := p.sendFrame(frame); err != nil {
			return err
		}

		// Opus の lacing は複数パケットを1ブロックへ詰めるため、
		// 各パケットの想定長に応じて timestamp を進める。
		if frameType == FrameTypeAudio && p.reader.audioCodec == "A_OPUS" {
			runningTsMs += estimateOpusPacketDurationMs(payload)
			continue
		}
		if frameType == FrameTypeAudio && p.reader.audioCodec == "A_PCM/INT/LIT" {
			runningTsMs += p.estimatePCMDurationMs(payload)
		}
	}
	return nil
}

func (p *mkvStreamParser) scaleTicksToMilliseconds(ticks int64) int64 {
	if ticks == 0 {
		return 0
	}
	timeScale := p.reader.timescale
	if timeScale == 0 {
		timeScale = 1000000
	}
	// float64 で演算し、timescale != 1ms の入力でも過剰なオーバーフローを避ける。
	return int64(float64(ticks) * float64(timeScale) / 1000000.0)
}

func (p *mkvStreamParser) estimatePCMDurationMs(payload []byte) int64 {
	if p.reader.audioSampleRate <= 0 || p.reader.audioChannels <= 0 {
		return 0
	}
	bytesPerSample := p.reader.audioChannels * 2 // S16LE
	if bytesPerSample <= 0 {
		return 0
	}
	samples := len(payload) / bytesPerSample
	if samples <= 0 {
		return 0
	}
	return int64(samples * 1000 / p.reader.audioSampleRate)
}

func (p *mkvStreamParser) parseLacedFrames(payload []byte, lacingMode int) ([][]byte, error) {
	switch lacingMode {
	case lacingNone:
		if len(payload) == 0 {
			return nil, nil
		}
		return [][]byte{payload}, nil
	case lacingXiph:
		return parseXiphLacing(payload)
	case lacingFixed:
		return parseFixedLacing(payload)
	case lacingEBML:
		return parseEBMLLacing(payload)
	default:
		return nil, fmt.Errorf("unknown lacing mode: %d", lacingMode)
	}
}

func parseXiphLacing(payload []byte) ([][]byte, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("xiph lacing: payload too short")
	}
	frameCount := int(payload[0]) + 1
	if frameCount <= 0 {
		return nil, fmt.Errorf("xiph lacing: invalid frame count")
	}

	idx := 1
	sizes := make([]int, frameCount)
	totalKnown := 0
	for i := 0; i < frameCount-1; i++ {
		size := 0
		for {
			if idx >= len(payload) {
				return nil, fmt.Errorf("xiph lacing: unexpected EOF in size table")
			}
			b := int(payload[idx])
			idx++
			size += b
			if b != 255 {
				break
			}
		}
		sizes[i] = size
		totalKnown += size
	}

	if totalKnown > len(payload)-idx {
		return nil, fmt.Errorf("xiph lacing: size table exceeds payload")
	}
	sizes[frameCount-1] = len(payload) - idx - totalKnown
	return sliceLacedFrames(payload[idx:], sizes)
}

func parseFixedLacing(payload []byte) ([][]byte, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("fixed lacing: payload too short")
	}
	frameCount := int(payload[0]) + 1
	if frameCount <= 0 {
		return nil, fmt.Errorf("fixed lacing: invalid frame count")
	}
	data := payload[1:]
	if frameCount == 0 {
		return nil, fmt.Errorf("fixed lacing: invalid frame count")
	}
	if len(data)%frameCount != 0 {
		return nil, fmt.Errorf("fixed lacing: payload size %d is not divisible by frame count %d", len(data), frameCount)
	}
	frameSize := len(data) / frameCount
	sizes := make([]int, frameCount)
	for i := range sizes {
		sizes[i] = frameSize
	}
	return sliceLacedFrames(data, sizes)
}

func parseEBMLLacing(payload []byte) ([][]byte, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("ebml lacing: payload too short")
	}
	frameCount := int(payload[0]) + 1
	if frameCount <= 0 {
		return nil, fmt.Errorf("ebml lacing: invalid frame count")
	}

	idx := 1
	firstSizeU64, n, err := readEBMLVint(payload[idx:])
	if err != nil {
		return nil, fmt.Errorf("ebml lacing: failed to read first size: %w", err)
	}
	idx += n
	if firstSizeU64 > math.MaxInt32 {
		return nil, fmt.Errorf("ebml lacing: first size too large: %d", firstSizeU64)
	}

	sizes := make([]int, frameCount)
	sizes[0] = int(firstSizeU64)
	sumSizes := sizes[0]
	prevSize := int64(sizes[0])

	for i := 1; i < frameCount-1; i++ {
		diff, readN, diffErr := readEBMLSignedVint(payload[idx:])
		if diffErr != nil {
			return nil, fmt.Errorf("ebml lacing: failed to read size diff: %w", diffErr)
		}
		idx += readN

		size := prevSize + diff
		if size < 0 || size > math.MaxInt32 {
			return nil, fmt.Errorf("ebml lacing: invalid lace size: %d", size)
		}
		sizes[i] = int(size)
		sumSizes += sizes[i]
		prevSize = size
	}

	if sumSizes > len(payload)-idx {
		return nil, fmt.Errorf("ebml lacing: lace sizes exceed payload")
	}
	sizes[frameCount-1] = len(payload) - idx - sumSizes
	return sliceLacedFrames(payload[idx:], sizes)
}

func sliceLacedFrames(data []byte, sizes []int) ([][]byte, error) {
	offset := 0
	out := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		if size < 0 || offset+size > len(data) {
			return nil, fmt.Errorf("invalid laced frame size: %d", size)
		}
		out = append(out, data[offset:offset+size])
		offset += size
	}
	if offset != len(data) {
		return nil, fmt.Errorf("laced frame payload mismatch: consumed=%d total=%d", offset, len(data))
	}
	return out, nil
}

func readEBMLVint(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	first := data[0]
	length := 1
	mask := byte(0x80)
	for length <= 8 && (first&mask) == 0 {
		mask >>= 1
		length++
	}
	if length > 8 || len(data) < length {
		return 0, 0, fmt.Errorf("invalid EBML vint")
	}

	value := uint64(first & (mask - 1))
	for i := 1; i < length; i++ {
		value = (value << 8) | uint64(data[i])
	}
	return value, length, nil
}

func readEBMLSignedVint(data []byte) (int64, int, error) {
	value, n, err := readEBMLVint(data)
	if err != nil {
		return 0, 0, err
	}
	length := n
	bias := int64((uint64(1) << uint(length*7-1)) - 1)
	return int64(value) - bias, n, nil
}

func estimateOpusPacketDurationMs(payload []byte) int64 {
	if len(payload) == 0 {
		return 0
	}

	toc := payload[0]
	config := toc >> 3
	frameCode := toc & 0x03

	frameCount := 1
	switch frameCode {
	case 0:
		frameCount = 1
	case 1, 2:
		frameCount = 2
	case 3:
		if len(payload) < 2 {
			return 0
		}
		frameCount = int(payload[1] & 0x3F)
		if frameCount < 1 {
			frameCount = 1
		}
	}

	frameDurationUs := int64(20000)
	switch {
	case config < 12:
		switch config & 0x03 {
		case 0:
			frameDurationUs = 10000
		case 1:
			frameDurationUs = 20000
		case 2:
			frameDurationUs = 40000
		case 3:
			frameDurationUs = 60000
		}
	case config < 16:
		if config&0x01 == 0 {
			frameDurationUs = 10000
		} else {
			frameDurationUs = 20000
		}
	default:
		switch config & 0x03 {
		case 0:
			frameDurationUs = 2500
		case 1:
			frameDurationUs = 5000
		case 2:
			frameDurationUs = 10000
		case 3:
			frameDurationUs = 20000
		}
	}

	totalUs := frameDurationUs * int64(frameCount)
	return totalUs / 1000
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
