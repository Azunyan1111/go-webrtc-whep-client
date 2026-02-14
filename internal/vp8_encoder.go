package internal

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/Azunyan1111/libvpx-go/vpx"
)

type VP8Encoder struct {
	ctx         *vpx.CodecCtx
	img         *vpx.Image
	width       int
	height      int
	pts         int64
	pixelFormat string
}

var (
	yRTable [256]int
	yGTable [256]int
	yBTable [256]int
	uRTable [256]int
	uGTable [256]int
	uBTable [256]int
	vRTable [256]int
	vGTable [256]int
	vBTable [256]int
)

func init() {
	for i := 0; i < 256; i++ {
		yRTable[i] = 66 * i
		yGTable[i] = 129 * i
		yBTable[i] = 25 * i

		uRTable[i] = -38 * i
		uGTable[i] = -74 * i
		uBTable[i] = 112 * i

		vRTable[i] = 112 * i
		vGTable[i] = -94 * i
		vBTable[i] = -18 * i
	}
}

func NewVP8Encoder(width, height int, pixelFormat string) (*VP8Encoder, error) {
	ctx := vpx.NewCodecCtx()
	if ctx == nil {
		return nil, fmt.Errorf("failed to create codec context")
	}

	iface := vpx.EncoderIfaceVP8()
	if iface == nil {
		vpx.CodecDestroy(ctx)
		return nil, fmt.Errorf("failed to get VP8 encoder interface")
	}

	cfg := &vpx.CodecEncCfg{}
	if err := vpx.Error(vpx.CodecEncConfigDefault(iface, cfg, 0)); err != nil {
		vpx.CodecDestroy(ctx)
		return nil, fmt.Errorf("failed to get default encoder config: %v", err)
	}
	cfg.Deref()

	// Configure encoder parameters
	cfg.GW = uint32(width)
	cfg.GH = uint32(height)
	cfg.GTimebase = vpx.Rational{Num: 1, Den: 30}
	cfg.RcTargetBitrate = 1000 // 1 Mbps
	cfg.GPass = vpx.RcOnePass
	cfg.RcEndUsage = vpx.Cbr
	cfg.KfMode = vpx.KfAuto
	cfg.KfMaxDist = 30
	// スレッド数は上限を設けてCPU過負荷を抑える
	numThreads := runtime.NumCPU()
	if numThreads > 4 {
		numThreads = 4
	}
	if numThreads < 1 {
		numThreads = 1
	}
	cfg.GThreads = uint32(numThreads)
	cfg.GLagInFrames = 0
	cfg.RcMinQuantizer = 4
	cfg.RcMaxQuantizer = 48
	// リアルタイムエンコード用のプロファイル設定
	cfg.GProfile = 0 // Simple profile for faster encoding

	if err := vpx.Error(vpx.CodecEncInitVer(ctx, iface, cfg, 0, vpx.EncoderABIVersion)); err != nil {
		vpx.CodecDestroy(ctx)
		return nil, fmt.Errorf("failed to initialize encoder: %v", err)
	}

	img := vpx.ImageAlloc(nil, vpx.ImageFormatI420, uint32(width), uint32(height), 1)
	if img == nil {
		vpx.CodecDestroy(ctx)
		return nil, fmt.Errorf("failed to allocate image")
	}
	img.Deref()

	DebugLog("VP8Encoder: requested %dx%d, image W=%d H=%d DW=%d DH=%d, pixelFormat=%s, threads=%d\n",
		width, height, img.W, img.H, img.DW, img.DH, pixelFormat, numThreads)

	return &VP8Encoder{
		ctx:         ctx,
		img:         img,
		width:       width,
		height:      height,
		pts:         0,
		pixelFormat: pixelFormat,
	}, nil
}

func (e *VP8Encoder) Encode(frameData []byte) ([]byte, bool, error) {
	// Use image's actual dimensions (DW, DH) for size check
	w := int(e.img.DW)
	h := int(e.img.DH)

	switch e.pixelFormat {
	case "YUV420P", "I420":
		expectedSize := w * h * 3 / 2
		if len(frameData) != expectedSize {
			DebugLog("Invalid YUV420P data size: expected %d (%dx%dx3/2), got %d\n", expectedSize, w, h, len(frameData))
			return nil, false, fmt.Errorf("invalid YUV420P data size: expected %d, got %d", expectedSize, len(frameData))
		}
		e.yuv420pToI420(frameData)
	default:
		// RGBA (default)
		expectedSize := w * h * 4
		if len(frameData) != expectedSize {
			DebugLog("Invalid RGBA data size: expected %d (%dx%dx4), got %d\n", expectedSize, w, h, len(frameData))
			return nil, false, fmt.Errorf("invalid RGBA data size: expected %d, got %d", expectedSize, len(frameData))
		}
		e.rgbaToI420(frameData)
	}

	// Encode frame (DlRealtime for low-latency encoding)
	if err := vpx.Error(vpx.CodecEncode(e.ctx, e.img, vpx.CodecPts(e.pts), 1, 0, vpx.DlRealtime)); err != nil {
		detail := vpx.CodecErrorDetail(e.ctx)
		return nil, false, fmt.Errorf("failed to encode frame: %v (detail: %s)", err, detail)
	}
	e.pts++

	// Get encoded data
	var iter vpx.CodecIter
	pkt := vpx.CodecGetCxData(e.ctx, &iter)
	if pkt == nil {
		return nil, false, nil
	}
	pkt.Deref()

	if pkt.Kind != vpx.CodecCxFramePkt {
		return nil, false, nil
	}

	data := pkt.GetFrameData()
	isKeyframe := pkt.IsKeyframe()

	return data, isKeyframe, nil
}

func (e *VP8Encoder) rgbaToI420(rgba []byte) {
	h := int(e.img.DH)
	w := int(e.img.DW)

	yStride := int(e.img.Stride[vpx.PlaneY])
	uStride := int(e.img.Stride[vpx.PlaneU])
	vStride := int(e.img.Stride[vpx.PlaneV])

	// Access planes directly via unsafe.Pointer (same as libvpx-go test code)
	yPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneY])))[:yStride*h]
	uPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneU])))[:uStride*h/2]
	vPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneV])))[:vStride*h/2]

	// Convert RGBA to YUV420 with 2x2 traversal to avoid per-pixel modulo checks.
	for row := 0; row < h; row += 2 {
		row0Base := row * w * 4
		yRow0 := row * yStride

		row1 := row + 1
		hasRow1 := row1 < h
		row1Base := row1 * w * 4
		yRow1 := row1 * yStride

		uvRow := (row / 2) * uStride
		vvRow := (row / 2) * vStride

		for col := 0; col < w; col += 2 {
			idx00 := row0Base + col*4
			r00 := int(rgba[idx00])
			g00 := int(rgba[idx00+1])
			b00 := int(rgba[idx00+2])
			y00 := ((yRTable[r00] + yGTable[g00] + yBTable[b00] + 128) >> 8) + 16
			yPlane[yRow0+col] = clampToByte(y00)

			col1 := col + 1
			if col1 < w {
				idx01 := idx00 + 4
				r01 := int(rgba[idx01])
				g01 := int(rgba[idx01+1])
				b01 := int(rgba[idx01+2])
				y01 := ((yRTable[r01] + yGTable[g01] + yBTable[b01] + 128) >> 8) + 16
				yPlane[yRow0+col1] = clampToByte(y01)
			}

			if hasRow1 {
				idx10 := row1Base + col*4
				r10 := int(rgba[idx10])
				g10 := int(rgba[idx10+1])
				b10 := int(rgba[idx10+2])
				y10 := ((yRTable[r10] + yGTable[g10] + yBTable[b10] + 128) >> 8) + 16
				yPlane[yRow1+col] = clampToByte(y10)

				if col1 < w {
					idx11 := idx10 + 4
					r11 := int(rgba[idx11])
					g11 := int(rgba[idx11+1])
					b11 := int(rgba[idx11+2])
					y11 := ((yRTable[r11] + yGTable[g11] + yBTable[b11] + 128) >> 8) + 16
					yPlane[yRow1+col1] = clampToByte(y11)
				}
			}

			uvCol := col / 2
			uVal := ((uRTable[r00] + uGTable[g00] + uBTable[b00] + 128) >> 8) + 128
			vVal := ((vRTable[r00] + vGTable[g00] + vBTable[b00] + 128) >> 8) + 128
			uPlane[uvRow+uvCol] = clampToByte(uVal)
			vPlane[vvRow+uvCol] = clampToByte(vVal)
		}
	}
}

func clampToByte(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func (e *VP8Encoder) yuv420pToI420(yuv []byte) {
	h := int(e.img.DH)
	w := int(e.img.DW)

	yStride := int(e.img.Stride[vpx.PlaneY])
	uStride := int(e.img.Stride[vpx.PlaneU])
	vStride := int(e.img.Stride[vpx.PlaneV])

	// Access planes directly via unsafe.Pointer
	yPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneY])))[:yStride*h]
	uPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneU])))[:uStride*h/2]
	vPlane := (*(*[1 << 30]byte)(unsafe.Pointer(e.img.Planes[vpx.PlaneV])))[:vStride*h/2]

	// YUV420P layout: Y plane, then U plane, then V plane
	ySize := w * h
	uvSize := w * h / 4

	srcY := yuv[:ySize]
	srcU := yuv[ySize : ySize+uvSize]
	srcV := yuv[ySize+uvSize : ySize+2*uvSize]

	// Copy Y plane (row by row to handle stride)
	for row := 0; row < h; row++ {
		copy(yPlane[row*yStride:row*yStride+w], srcY[row*w:(row+1)*w])
	}

	// Copy U plane
	uvH := h / 2
	uvW := w / 2
	for row := 0; row < uvH; row++ {
		copy(uPlane[row*uStride:row*uStride+uvW], srcU[row*uvW:(row+1)*uvW])
	}

	// Copy V plane
	for row := 0; row < uvH; row++ {
		copy(vPlane[row*vStride:row*vStride+uvW], srcV[row*uvW:(row+1)*uvW])
	}
}

func (e *VP8Encoder) Close() {
	if e.img != nil {
		vpx.ImageFree(e.img)
		e.img = nil
	}
	if e.ctx != nil {
		vpx.CodecDestroy(e.ctx)
		e.ctx = nil
	}
}
