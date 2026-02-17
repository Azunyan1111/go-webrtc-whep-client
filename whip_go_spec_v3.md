# whip-go 実装忠実仕様書 v3（言語・ライブラリ非依存）

## 1. この文書の目的
本書は RFC の一般説明ではなく、`whip-go` 実装が実際に行っている処理を、他言語でも同じ挙動を再現できる粒度で定義する。

## 2. 実行インターフェース

### 2.1 起動
- コマンド形式: `whip-go <WHIP_URL> [flags]`
- 必須引数: `WHIP_URL`
- 入力: `stdin` から MKV ストリーム（映像 + 音声）

### 2.2 フラグ（既定値）
- `--debug, -d` = `false`
- `--no-pacing` = `false`
- `--drop-threshold` = `200` (ms)
- `--video-bitrate-kbps, -b` = `5000`
- `--cpu-profile` = `""`
- `--mem-profile` = `""`

注記: `--no-validate` は共通フラグとして存在するが、`whip-go` の送信経路では使用しない。

## 3. 実装プロファイル（whip-go の味）
- 映像は必ず VP8 送信（VP9/H264 送信は未実装）
- 音声は Opus 送信（入力が PCM の場合のみ内部で Opus 変換）
- シグナリングは WHIP `POST` + `201` の最小実装
- ICE は full gather 後に offer 一括送信（Trickle ICE なし）
- 送出は低遅延寄り: キュー詰まり時に古いフレームを積極的に破棄
- RTCP 無受信 5 秒で自動停止

## 4. 起動シーケンス
1. 引数・フラグを解析する。
2. `stdin` から MKV を読み、最初の映像フレームが来るまで待つ。
3. 映像解像度とピクセル形式を確定する。
4. 音声 codec を判定し、PCM の場合のみ Opus encoder を初期化する。
5. 映像 encoder（VP8）を初期化する。
6. PeerConnection を作成し、映像/音声の送信用トラックを追加する。
7. WHIP で SDP offer/answer を交換する。
8. 送信ワーカー群を起動し、映像/音声を並列送信する。

## 5. 入力コンテナ仕様（実装依存）

### 5.1 コンテナとトラック認識
- 想定コンテナ: Matroska/MKV
- トラック codec ID の認識:
  - 映像: `V_UNCOMPRESSED`, `V_VP8`, `V_VP9`
  - 音声: `A_OPUS`, `A_PCM/INT/LIT`
- 映像トラックが見つからない場合は終了する。
- 音声トラックは任意。

### 5.2 フレーム抽出
- `SimpleBlock` / `Block` を読み、`timestampMs = clusterTime + relativeTs` で時刻化する。
- 取得した `timestampMs` を pacing と RTP timestamp 計算の基準に使う。
- `TimecodeScale` は読み取るが、現在の実装では `timestampMs` 換算に反映していない（ms 前提運用）。

## 6. 映像処理仕様

### 6.1 入力フォーマット
- 既定: `RGBA`
- 追加対応: `YUV420P` / `I420`

### 6.2 VP8 エンコード設定
- レート制御: CBR
- `target bitrate`: フラグ値（既定 5000 kbps）
- keyframe: auto、最大間隔 30
- timebase: `1/30`
- thread 数: `min(max(CPU数, 1), 4)`
- quantizer: min 4 / max 48

### 6.3 色変換の特徴
- `RGBA -> I420` 変換時、U/V は 2x2 ブロック先頭画素ベースで算出する近似方式を採用する。

## 7. 音声処理仕様

### 7.1 A_OPUS 入力
- 再エンコードせず RTP 化して送信する。

### 7.2 A_PCM/INT/LIT 入力
- Opus へ変換して送信する。
- 制約:
  - サンプルレートは 48kHz のみ受理
  - チャネルは 1 または 2 のみ受理
  - 10ms 単位でフレーム化してエンコード
- タイムスタンプ生成:
  - 最初の入力時刻を `baseTimestampMs`
  - 各出力フレーム時刻 = `baseTimestampMs + encodedFrameCount * 10`

## 8. RTP パケット化仕様

### 8.1 共通
- RTP version = 2
- SSRC は起動時にランダム生成
- sequence number はトラックごとに 0 から開始
- timestamp 変換:
  - 映像: `pts_ms * 90000 / 1000`
  - 音声: `pts_ms * 48000 / 1000`

### 8.2 映像 VP8
- PT = 97（固定）
- 最大ペイロード長 = 1200 bytes
- VP8 payload descriptor は最小 1 byte
- 先頭断片のみ `S=1`
- 末尾断片のみ RTP marker = 1

### 8.3 音声 Opus
- PT = 111（固定）
- 1 フレーム = 1 RTP パケット
- RTP marker = 1

## 9. 送信並列モデル
送信中は以下の並列処理を行う。

1. フレーム取り込み goroutine
2. 映像ワーカー goroutine
3. 音声ワーカー goroutine
4. RTCP 受信 goroutine（映像）
5. RTCP 受信 goroutine（音声）
6. RTCP タイムアウト監視 goroutine
7. シグナル監視 goroutine
8. デバッグ統計出力 goroutine（debug 有効時のみ）

## 10. キュー制御（低遅延重視の癖）

### 10.1 キュー容量
- 映像/音声それぞれ `12`

### 10.2 キュー満杯時
- 新規投入できない場合、最古フレームを 1 件破棄して再投入する。
- 破棄理由: `queue-full`

### 10.3 遅延蓄積時の間引き
- キュー深さが `4` を超える状態が続く場合、`3` 回に 1 回の割合で最古を破棄する。
- 破棄理由: `latency-trim`

### 10.4 副作用
- キュー破棄を検知したワーカーは pacer を `Reset` して時刻再同期する。

## 11. Pacing / 遅延破棄
- 既定では PTS ベース pacing を有効化する。
- `--no-pacing` 指定時は待機なしで可能な限り送信する。
- pacer の最大待機時間は 1 秒。
- `--drop-threshold` を超えて遅延したフレームは破棄対象にする（0 以下で無効）。
- PTS 巻き戻り時は破棄せず再同期する。

## 12. WHIP 実装仕様（この実装で実際にやる範囲）

### 12.1 SDP 交換
- ローカル offer 作成後、ICE gather 完了を待つ。
- `POST <WHIP_URL>` に SDP offer を送る。
- `Content-Type: application/sdp`
- HTTP timeout = 30 秒
- `201 Created` のみ成功扱い
- レスポンス本文を SDP answer として適用

### 12.2 未実装（現時点）
- Trickle ICE (`PATCH`)
- `Location` への `DELETE`
- `307/308` リダイレクト対応
- `Link: rel="ice-server"` 反映
- 認証ヘッダ自動設定

## 13. RTCP と自動停止
- 各 RTPSender から RTCP を読み続ける。
- RTCP 最終受信時刻を更新する。
- 5 秒間 RTCP が来なければ自動停止する。
- debug 時は RR/SR/NACK/PLI/FIR/REMB を stderr へ出力する。

## 14. 停止条件
以下のいずれかで送信を終了する。

1. `SIGINT` / `SIGTERM`
2. RTCP タイムアウト（5 秒）
3. 入力 EOF
4. 主要処理の致命エラー（SDP 交換失敗、ワーカー異常など）

## 15. エラー処理方針
- 初期化系エラーは即時終了
- フレーム単位エラー（encode/send）はカウンタ加算して継続
- WHIP の非 `201` 応答は本文付きエラーで終了
- 解像度不明（幅/高さ 0）は終了

## 16. 相互運用上の前提
- 受信側が VP8/Opus を受理できること
- MKV タイムスタンプが実質 ms 相当であること
- ICE サーバ設定を外部から厳密制御したい場合は実装拡張が必要

## 17. 再実装時の必須互換ポイント
- 先頭映像フレームで解像度確定する初期化順
- `queue-full` + `latency-trim` の二段階破棄
- キュー破棄後の pacer 再同期
- RTCP 5 秒無受信で停止
- VP8 PT97 / Opus PT111 固定運用
- WHIP `POST + application/sdp + 201` 成功条件

## 18. 根拠引用（実装コード）
以下は本仕様の根拠として実装から直接引用した要点である。

1. `cmd/whip-go/main.go`
   - "frameQueueCapacity         = 12"
   - "frameQueueLowLatencyTarget = 4"
   - "frameQueueTrimInterval     = 3"
   - "RTCP timeout: no reports received for 5 seconds, stopping..."
2. `internal/whip.go`
   - `req.Header.Set("Content-Type", "application/sdp")`
   - `if resp.StatusCode != http.StatusCreated`
3. `internal/rtp_packetizer.go`
   - "VP8PayloadType  = 97"
   - "OpusPayloadType = 111"
   - "MaxRTPPayload   = 1200"
4. `internal/pacer.go`
   - "threshold が0以下の場合は常にfalseを返す（破棄無効）"
5. `internal/opus_encoder.go`
   - "only 48000Hz sample rate is supported"

## 19. 参考規格
- RFC 9725 (WHIP): https://datatracker.ietf.org/doc/html/rfc9725
- RFC 7741 (RTP Payload Format for VP8): https://datatracker.ietf.org/doc/html/rfc7741
- RFC 7587 (RTP Payload Format for Opus): https://datatracker.ietf.org/doc/html/rfc7587
