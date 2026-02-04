package main

import (
	"fmt"
	"os"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
)

func testEncode(filename, outputPrefix string) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer f.Close()

	reader := internal.NewMKVReader(f)
	reader.Start()

	// Read first video frame
	var firstFrame *internal.Frame
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			return fmt.Errorf("failed to read frame: %v", err)
		}
		if frame.Type == internal.FrameTypeVideo {
			firstFrame = frame
			break
		}
	}

	width := reader.VideoWidth()
	height := reader.VideoHeight()
	pixelFormat := reader.PixelFormat()
	fmt.Printf("  Resolution: %dx%d, Format: %s\n", width, height, pixelFormat)

	// Create encoder
	encoder, err := internal.NewVP8Encoder(width, height, pixelFormat)
	if err != nil {
		return fmt.Errorf("failed to create encoder: %v", err)
	}
	defer encoder.Close()

	// Encode first keyframe and save
	encoded, isKeyframe, err := encoder.Encode(firstFrame.Data)
	if err != nil {
		return fmt.Errorf("encode error: %v", err)
	}
	if encoded != nil && isKeyframe {
		outFile := fmt.Sprintf("/tmp/%s_keyframe.vp8", outputPrefix)
		if err := os.WriteFile(outFile, encoded, 0644); err != nil {
			return fmt.Errorf("write error: %v", err)
		}
		fmt.Printf("  Saved keyframe to %s (%d bytes)\n", outFile, len(encoded))
	}

	return nil
}

func main() {
	fmt.Println("=== Testing RGBA ===")
	if err := testEncode("/tmp/test_rgba.mkv", "rgba"); err != nil {
		fmt.Printf("RGBA test failed: %v\n", err)
	}

	fmt.Println("\n=== Testing YUV420P ===")
	if err := testEncode("/tmp/test_yuv420p.mkv", "yuv420p"); err != nil {
		fmt.Printf("YUV420P test failed: %v\n", err)
	}
}
