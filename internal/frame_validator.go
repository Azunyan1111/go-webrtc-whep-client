package internal

// FrameValidator はデコード済みRGBAフレームの品質を検証する
// パケットロスによるノイズ・アーティファクトを検出し、
// 破損フレームの出力を防止する
type FrameValidator struct {
	width              int
	height             int
	lastFrame          []byte // 前フレームのRGBAデータ
	lastHistogram      []int  // 前フレームの輝度ヒストグラム
	frameCount         int    // 処理フレーム数
	consecutiveInvalid int    // 連続無効フレーム数
	thresholds         ValidationThresholds
}

// ValidationThresholds は検証閾値を保持
type ValidationThresholds struct {
	// フレーム間差分: この割合以上のピクセルが大きく変化したら異常
	MaxChangedPixelRatio float64
	// ピクセル変化の閾値（RGBA各チャンネルの差分合計）
	PixelChangeThreshold int
	// マクロブロッキング: ブロック境界での急激な変化検出閾値
	BlockEdgeThreshold int
	// 緑色フレーム検出: 緑色優位ピクセルの割合閾値
	GreenDominantRatio float64
	// ヒストグラム急変: 前フレームとのヒストグラム差分閾値
	HistogramDiffThreshold float64
	// 連続無効フレーム許容数（これを超えたらキーフレーム待ち）
	MaxConsecutiveInvalid int
}

// DefaultThresholds はデフォルトの検証閾値を返す
func DefaultThresholds() ValidationThresholds {
	return ValidationThresholds{
		MaxChangedPixelRatio:   0.30,  // 30%以上のピクセルが急変したら異常
		PixelChangeThreshold:   150,   // RGBA差分合計がこれ以上で「変化」とみなす
		BlockEdgeThreshold:     80,    // ブロック境界での輝度差閾値
		GreenDominantRatio:     0.006, // 0.6%以上が緑優位なら異常（正常:0.28%、ノイズ:0.75%以上）
		HistogramDiffThreshold: 1.00,  // ヒストグラム検出は無効化（正常時の方が高い値が出るため）
		MaxConsecutiveInvalid:  5,     // 5フレーム連続無効でキーフレーム待ち
	}
}

// NewFrameValidator は新しいFrameValidatorを作成
func NewFrameValidator(width, height int) *FrameValidator {
	return &FrameValidator{
		width:         width,
		height:        height,
		lastHistogram: make([]int, 256),
		thresholds:    DefaultThresholds(),
	}
}

// NewFrameValidatorWithThresholds はカスタム閾値でFrameValidatorを作成
func NewFrameValidatorWithThresholds(width, height int, thresholds ValidationThresholds) *FrameValidator {
	return &FrameValidator{
		width:         width,
		height:        height,
		lastHistogram: make([]int, 256),
		thresholds:    thresholds,
	}
}

// ValidationResult は検証結果を保持
type ValidationResult struct {
	IsValid            bool
	Reason             string
	ChangedPixelRatio  float64
	GreenDominantRatio float64
	HistogramDiff      float64
	BlockingScore      float64
}

// ValidateFrame はRGBAフレームを検証し、ノイズ/アーティファクトを検出
// keyframe=trueの場合、前フレームとの比較をスキップ（キーフレームは基準点）
func (v *FrameValidator) ValidateFrame(rgba []byte, keyframe bool) ValidationResult {
	result := ValidationResult{IsValid: true}

	if len(rgba) == 0 {
		result.IsValid = false
		result.Reason = "empty frame"
		return result
	}

	expectedSize := v.width * v.height * 4
	if len(rgba) != expectedSize {
		result.IsValid = false
		result.Reason = "invalid frame size"
		return result
	}

	// 1. 緑色フレーム検出（デコーダ失敗の典型パターン）
	greenRatio := v.detectGreenDominant(rgba)
	result.GreenDominantRatio = greenRatio
	if greenRatio > v.thresholds.GreenDominantRatio {
		result.IsValid = false
		result.Reason = "green dominant frame (decoder failure)"
		v.consecutiveInvalid++
		return result
	}

	// 2. マクロブロッキング検出
	blockingScore := v.detectMacroblocking(rgba)
	result.BlockingScore = blockingScore
	if blockingScore > 0.030 { // 3.0%以上のブロック境界で異常（正常:2%、ノイズ:5%以上）
		result.IsValid = false
		result.Reason = "macroblocking detected"
		v.consecutiveInvalid++
		return result
	}

	// キーフレームの場合、前フレームとの比較はスキップ
	if keyframe {
		v.updateReference(rgba)
		v.consecutiveInvalid = 0
		v.frameCount++
		return result
	}

	// 3. フレーム間急変検出（前フレームがある場合のみ）
	if len(v.lastFrame) == expectedSize {
		changedRatio := v.detectFrameChange(rgba)
		result.ChangedPixelRatio = changedRatio
		if changedRatio > v.thresholds.MaxChangedPixelRatio {
			result.IsValid = false
			result.Reason = "excessive frame change"
			v.consecutiveInvalid++
			return result
		}

		// 4. ヒストグラム急変検出
		histDiff := v.detectHistogramChange(rgba)
		result.HistogramDiff = histDiff
		if histDiff > v.thresholds.HistogramDiffThreshold {
			result.IsValid = false
			result.Reason = "histogram anomaly"
			v.consecutiveInvalid++
			return result
		}
	}

	// 検証成功
	v.updateReference(rgba)
	v.consecutiveInvalid = 0
	v.frameCount++
	return result
}

// ShouldWaitForKeyframe は連続無効フレームが多すぎる場合trueを返す
func (v *FrameValidator) ShouldWaitForKeyframe() bool {
	return v.consecutiveInvalid >= v.thresholds.MaxConsecutiveInvalid
}

// ResetOnKeyframe はキーフレーム受信時に状態をリセット
func (v *FrameValidator) ResetOnKeyframe() {
	v.consecutiveInvalid = 0
}

// detectGreenDominant は緑色優位フレームを検出
// デコーダ失敗時、YUV→RGB変換で緑色が優位になることが多い
func (v *FrameValidator) detectGreenDominant(rgba []byte) float64 {
	greenDominantCount := 0

	// サンプリング（全ピクセルではなく16ピクセルごと）
	sampleStep := 16
	sampledPixels := 0

	for i := 0; i < len(rgba); i += 4 * sampleStep {
		if i+3 >= len(rgba) {
			break
		}
		r, g, b := int(rgba[i]), int(rgba[i+1]), int(rgba[i+2])
		sampledPixels++

		// 緑が赤と青の両方より30以上大きい場合を「緑優位」とする
		if g > r+30 && g > b+30 {
			greenDominantCount++
		}
	}

	if sampledPixels == 0 {
		return 0
	}
	return float64(greenDominantCount) / float64(sampledPixels)
}

// detectMacroblocking はブロック境界での不連続性を検出
// VP8/VP9は16x16または8x8マクロブロックを使用
func (v *FrameValidator) detectMacroblocking(rgba []byte) float64 {
	blockSize := 16
	anomalyCount := 0
	totalBoundaries := 0

	// 水平方向のブロック境界をチェック
	for y := 0; y < v.height; y++ {
		for x := blockSize; x < v.width; x += blockSize {
			if x >= v.width {
				break
			}
			// ブロック境界の左右のピクセルを比較
			leftIdx := (y*v.width + x - 1) * 4
			rightIdx := (y*v.width + x) * 4

			if leftIdx+3 >= len(rgba) || rightIdx+3 >= len(rgba) {
				continue
			}

			// 輝度差を計算
			leftLum := int(rgba[leftIdx])*299 + int(rgba[leftIdx+1])*587 + int(rgba[leftIdx+2])*114
			rightLum := int(rgba[rightIdx])*299 + int(rgba[rightIdx+1])*587 + int(rgba[rightIdx+2])*114
			diff := abs(leftLum-rightLum) / 1000

			totalBoundaries++
			if diff > v.thresholds.BlockEdgeThreshold {
				anomalyCount++
			}
		}
	}

	// 垂直方向のブロック境界をチェック
	for x := 0; x < v.width; x++ {
		for y := blockSize; y < v.height; y += blockSize {
			if y >= v.height {
				break
			}
			// ブロック境界の上下のピクセルを比較
			topIdx := ((y-1)*v.width + x) * 4
			bottomIdx := (y*v.width + x) * 4

			if topIdx+3 >= len(rgba) || bottomIdx+3 >= len(rgba) {
				continue
			}

			// 輝度差を計算
			topLum := int(rgba[topIdx])*299 + int(rgba[topIdx+1])*587 + int(rgba[topIdx+2])*114
			bottomLum := int(rgba[bottomIdx])*299 + int(rgba[bottomIdx+1])*587 + int(rgba[bottomIdx+2])*114
			diff := abs(topLum-bottomLum) / 1000

			totalBoundaries++
			if diff > v.thresholds.BlockEdgeThreshold {
				anomalyCount++
			}
		}
	}

	if totalBoundaries == 0 {
		return 0
	}
	return float64(anomalyCount) / float64(totalBoundaries)
}

// detectFrameChange は前フレームとの急激な変化を検出
func (v *FrameValidator) detectFrameChange(rgba []byte) float64 {
	if len(v.lastFrame) != len(rgba) {
		return 0
	}

	changedPixels := 0

	// サンプリング（8ピクセルごと）
	sampleStep := 8
	sampledPixels := 0

	for i := 0; i < len(rgba); i += 4 * sampleStep {
		if i+3 >= len(rgba) {
			break
		}
		sampledPixels++

		// RGBA各チャンネルの差分合計
		diff := abs(int(rgba[i])-int(v.lastFrame[i])) +
			abs(int(rgba[i+1])-int(v.lastFrame[i+1])) +
			abs(int(rgba[i+2])-int(v.lastFrame[i+2]))

		if diff > v.thresholds.PixelChangeThreshold {
			changedPixels++
		}
	}

	if sampledPixels == 0 {
		return 0
	}
	return float64(changedPixels) / float64(sampledPixels)
}

// detectHistogramChange は輝度ヒストグラムの急変を検出
func (v *FrameValidator) detectHistogramChange(rgba []byte) float64 {
	currentHist := make([]int, 256)
	totalPixels := 0

	// サンプリングして輝度ヒストグラムを作成
	sampleStep := 16
	for i := 0; i < len(rgba); i += 4 * sampleStep {
		if i+2 >= len(rgba) {
			break
		}
		// 輝度を計算 (0.299R + 0.587G + 0.114B)
		lum := (int(rgba[i])*299 + int(rgba[i+1])*587 + int(rgba[i+2])*114) / 1000
		if lum > 255 {
			lum = 255
		}
		currentHist[lum]++
		totalPixels++
	}

	if totalPixels == 0 {
		return 0
	}

	// 前フレームのヒストグラムがない場合
	lastTotal := 0
	for _, count := range v.lastHistogram {
		lastTotal += count
	}
	if lastTotal == 0 {
		return 0
	}

	// ヒストグラム差分を計算（正規化）
	diff := 0.0
	for i := 0; i < 256; i++ {
		currentNorm := float64(currentHist[i]) / float64(totalPixels)
		lastNorm := float64(v.lastHistogram[i]) / float64(lastTotal)
		diff += absFloat(currentNorm - lastNorm)
	}

	return diff / 2.0 // 0-1の範囲に正規化
}

// updateReference は参照フレームを更新
func (v *FrameValidator) updateReference(rgba []byte) {
	// フレームをコピー
	if v.lastFrame == nil || len(v.lastFrame) != len(rgba) {
		v.lastFrame = make([]byte, len(rgba))
	}
	copy(v.lastFrame, rgba)

	// ヒストグラムを更新
	for i := range v.lastHistogram {
		v.lastHistogram[i] = 0
	}
	sampleStep := 16
	for i := 0; i < len(rgba); i += 4 * sampleStep {
		if i+2 >= len(rgba) {
			break
		}
		lum := (int(rgba[i])*299 + int(rgba[i+1])*587 + int(rgba[i+2])*114) / 1000
		if lum > 255 {
			lum = 255
		}
		v.lastHistogram[lum]++
	}
}

// UpdateResolution は解像度変更時に呼び出す
func (v *FrameValidator) UpdateResolution(width, height int) {
	v.width = width
	v.height = height
	v.lastFrame = nil
	v.lastHistogram = make([]int, 256)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
