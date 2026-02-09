package internal

import (
	"time"
)

const pacingWaitLogInterval = time.Second

// Pacer はPTSに基づいてフレーム送信タイミングを制御する
type Pacer struct {
	baseWallTime time.Time     // 基準実時刻
	basePTS      int64         // 基準PTS（ミリ秒）
	initialized  bool          // 初期化済みフラグ
	maxWait      time.Duration // 最大待機時間（異常PTS対策）
}

// NewPacer は新しいPacerを作成する
func NewPacer(maxWait time.Duration) *Pacer {
	return &Pacer{
		maxWait: maxWait,
	}
}

// Wait はPTSに基づいて適切なタイミングまで待機する
// 入力がリアルタイムより遅い場合は待機なしで即座に返る
func (p *Pacer) Wait(timestampMs int64) {
	if !p.initialized {
		p.resync(timestampMs)
		return
	}

	// 期待送信時刻を計算
	ptsDiff := timestampMs - p.basePTS
	if ptsDiff < 0 {
		// PTSが戻った場合（ループ等）はリセット
		p.resync(timestampMs)
		return
	}

	expectedTime := p.baseWallTime.Add(time.Duration(ptsDiff) * time.Millisecond)
	waitDuration := time.Until(expectedTime)

	// 待機が必要な場合のみスリープ
	if waitDuration > 0 {
		// 最大待機時間で制限
		if waitDuration > p.maxWait {
			DebugLog("Pacing: clamping wait from %v to %v (PTS jump detected)\n", waitDuration, p.maxWait)
			waitDuration = p.maxWait
		}
		DebugLogPeriodic("pacer.wait", pacingWaitLogInterval, "Pacing: waiting %v (PTS: %dms)\n", waitDuration, timestampMs)
		time.Sleep(waitDuration)
	}
}

// Reset はPacerの状態をリセットする（再同期用）
func (p *Pacer) Reset() {
	p.initialized = false
	p.baseWallTime = time.Time{}
	p.basePTS = 0
}

// ShouldDrop はPTSに基づいてフレームを破棄すべきかを判定する
// threshold が0以下の場合は常にfalseを返す（破棄無効）
func (p *Pacer) ShouldDrop(timestampMs int64, threshold time.Duration) bool {
	if threshold <= 0 {
		return false
	}

	if !p.initialized {
		// 初期化前は破棄しない（Waitで初期化される）
		return false
	}

	// PTSが戻った場合は破棄しない（リセット処理はWaitで行う）
	ptsDiff := timestampMs - p.basePTS
	if ptsDiff < 0 {
		return false
	}

	// 期待送信時刻を計算
	expectedTime := p.baseWallTime.Add(time.Duration(ptsDiff) * time.Millisecond)
	lateness := time.Since(expectedTime)

	// 遅延が閾値を超えていたら破棄
	if lateness > threshold {
		// 大幅遅延時は連続ドロップを避けるため基準時刻を再同期する
		if p.maxWait > 0 && lateness > p.maxWait {
			DebugLog("Pacing drift detected: PTS=%dms, lateness=%v (maxWait=%v), resyncing\n", timestampMs, lateness, p.maxWait)
			p.resync(timestampMs)
			return false
		}
		DebugLog("Dropping frame: PTS=%dms, lateness=%v (threshold=%v)\n", timestampMs, lateness, threshold)
		return true
	}

	return false
}

func (p *Pacer) resync(timestampMs int64) {
	p.baseWallTime = time.Now()
	p.basePTS = timestampMs
	p.initialized = true
}
