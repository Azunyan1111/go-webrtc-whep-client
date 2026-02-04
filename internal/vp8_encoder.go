package internal

import (
	"fmt"
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
	cfg.GThreads = 4
	cfg.GLagInFrames = 0
	cfg.RcMinQuantizer = 4
	cfg.RcMaxQuantizer = 48

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

	DebugLog("VP8Encoder: requested %dx%d, image W=%d H=%d DW=%d DH=%d, pixelFormat=%s\n",
		width, height, img.W, img.H, img.DW, img.DH, pixelFormat)

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

	// Encode frame
	if err := vpx.Error(vpx.CodecEncode(e.ctx, e.img, vpx.CodecPts(e.pts), 1, 0, vpx.DlGoodQuality)); err != nil {
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

	// Convert RGBA to YUV420
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			rgbaIdx := (row*w + col) * 4
			r := int(rgba[rgbaIdx])
			g := int(rgba[rgbaIdx+1])
			b := int(rgba[rgbaIdx+2])

			// RGB to YUV conversion (BT.601)
			yVal := ((66*r + 129*g + 25*b + 128) >> 8) + 16
			if yVal > 255 {
				yVal = 255
			}
			if yVal < 0 {
				yVal = 0
			}
			yPlane[row*yStride+col] = byte(yVal)

			// Subsample for U and V
			if row%2 == 0 && col%2 == 0 {
				uvRow := row / 2
				uvCol := col / 2
				uVal := ((-38*r - 74*g + 112*b + 128) >> 8) + 128
				vVal := ((112*r - 94*g - 18*b + 128) >> 8) + 128
				if uVal > 255 {
					uVal = 255
				}
				if uVal < 0 {
					uVal = 0
				}
				if vVal > 255 {
					vVal = 255
				}
				if vVal < 0 {
					vVal = 0
				}
				uPlane[uvRow*uStride+uvCol] = byte(uVal)
				vPlane[uvRow*vStride+uvCol] = byte(vVal)
			}
		}
	}
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
