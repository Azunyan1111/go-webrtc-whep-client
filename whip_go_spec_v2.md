# whip-go 仕様書 v2（言語・ライブラリ非依存）

## 1. 目的
本仕様は、`whip-go` が実装している WHIP/WebRTC 送信機能を、Go や Pion に依存せず再実装できる粒度で定義する。
対象は「stdin 由来のメディアを WHIP で配信する送信クライアント」である。

## 2. 適用範囲
- 対象: WebRTC 送信（映像 VP8、音声 Opus）と WHIP シグナリング
- 非対象: 独自拡張認証方式、SFU 固有 API、録画制御 API

## 3. 規格上の根拠（引用付き）
以下は仕様上の根拠として必須参照とする。

1. WHIP: RFC 9725
   - 引用: "The WHIP endpoint SHALL return the HTTP 201 Created response"
   - 引用: "The WHIP client MAY perform trickle ICE or half trickle ICE"
   - 引用: "If an ICE session is terminated, the WHIP client MUST send an HTTP DELETE request"
2. WebRTC Offer/Answer: RFC 9429（旧 JSEP）
   - 引用: "An offer or answer may contain any number of ICE candidates"
3. RTP payload for VP8: RFC 7741
   - 引用: "The RTP timestamp MUST be based on a 90 kHz clock"
   - 引用: "S: Start of VP8 partition. SHOULD be set to 1"
4. RTP payload for Opus: RFC 7587
   - 引用: "The RTP timestamp is incremented with a 48000 Hz clock rate"

## 4. プロファイル定義
本仕様は 2 つの準拠レベルを定義する。

- `Core`（現実装準拠）
  - 非 Trickle ICE（完全 gather 後に POST）
  - HTTP `POST` で SDP offer を送信し、`201 Created` + SDP answer を受信
  - メディア送信は RTP（VP8/Opus）
- `Full`（RFC 9725 完全準拠目標）
  - `PATCH` による Trickle ICE / ICE restart
  - `Location` リソースへの `DELETE`
  - `307/308` リダイレクト処理
  - `Link: rel="ice-server"` の反映

## 5. システムモデル
送信クライアントは以下の 5 コンポーネントで構成する。

1. 入力デマルチプレクサ: stdin からコンテナフレームを取り出す
2. メディア変換器: 映像を VP8 化、必要なら音声を Opus 化
3. RTP パケット化器: VP8/Opus を RTP 化
4. WebRTC セッション管理器: PeerConnection、RTCP、ICE 状態管理
5. WHIP シグナリング器: HTTP(S) で offer/answer 交換

## 6. 入力メディア仕様

### 6.1 入力形式
- 入力はコンテナストリーム（想定: Matroska/MKV）
- 少なくとも 1 本の映像トラックを含むこと（無い場合はエラー終了）
- 音声トラックは任意

### 6.2 映像
- 入力ピクセル形式:
  - `RGBA`（既定）
  - `YUV420P` / `I420`
- 出力コーデック: `VP8`
- 入力フレーム時刻（ms）を送信時刻の基準 PTS とする

### 6.3 音声
- 入力 `A_OPUS`: Opus を RTP 化して送信
- 入力 `A_PCM/INT/LIT`: Opus へ変換して送信
- PCM→Opus 変換条件:
  - サンプルレート `48000 Hz`
  - チャネル `1` または `2`
  - 10ms 単位でフレーム化

## 7. WebRTC セッション仕様

### 7.1 コーデック宣言
- 映像: `VP8`（PT 97）
- 音声: `Opus`（PT 111）
- RTP clock rate:
  - VP8: 90kHz
  - Opus: 48kHz

### 7.2 送受信方向
- 映像・音声とも送信主体（publish 用）
- 実装上は送信用 m-line を作成し、受信は RTCP 最低限とする

### 7.3 ICE
- `Core`: 全 ICE candidate gather 完了後に offer を WHIP へ送る（non-trickle）
- 既定 STUN サーバは実装依存でよいが、設定可能にすることを推奨

## 8. WHIP シグナリング仕様

### 8.1 Offer 送信
- HTTP `POST` を WHIP endpoint に送信
- `Content-Type: application/sdp`
- Body: SDP offer（文字列）

### 8.2 Answer 受信
- 成功条件: HTTP `201 Created`
- Body: SDP answer
- `201` 以外は失敗とし、可能ならレスポンス本文をエラーメッセージに含める

### 8.3 Full 準拠で追加必須
- `Location` ヘッダの保持
- セッション終了時 `DELETE {Location}`
- `PATCH` による Trickle ICE/ICE restart
- `307/308` リダイレクト追従
- `Link: rel="ice-server"` による ICE サーバ設定更新

## 9. RTP パケット化仕様

### 9.1 共通
- RTP version は `2`
- RTP timestamp 変換:
  - `timestamp = pts_ms * clock_rate / 1000`

### 9.2 VP8（RFC 7741）
- 最大 RTP ペイロード長（実装プロファイル）: `1200 bytes`
- 1byte の最小 VP8 payload descriptor を使用
- 先頭フラグメントのみ `S=1`
- 最終フラグメントのみ RTP marker を `1`

### 9.3 Opus（RFC 7587）
- 原則 1 Opus フレームを 1 RTP パケットに格納
- RTP marker は `1`（実装プロファイル）

## 10. 送信タイミング制御
- 既定で PTS ベース pacing を有効
- 遅延フレーム破棄閾値（既定 `200ms`）を超えるフレームは破棄可能
- キュー滞留時は低遅延維持のため古いフレームを先頭から破棄可能

## 11. RTCP / ヘルス監視
- 各送信トラックで RTCP を受信し続けること
- 一定時間（実装プロファイル: 5 秒）RTCP が無い場合は切断判定して停止可能
- 受信 RTCP（RR/SR/NACK/PLI/FIR/REMB）は統計・制御判断に使用可能

## 12. エラー処理
- Offer 作成失敗: 即時終了
- WHIP POST 失敗/タイムアウト: 即時終了
- SDP answer 適用失敗: 即時終了
- メディア変換失敗: 当該フレーム破棄で継続可能
- RTP 送信失敗: ログ記録し継続、連続失敗時は終了ポリシーを持つこと

## 13. 状態機械
以下の状態遷移を最低限実装する。

1. `INIT`: 入力待機、トラック情報確定
2. `NEGOTIATING`: SDP offer 作成、WHIP POST、answer 適用
3. `ESTABLISHED`: ICE 接続成立待機
4. `STREAMING`: RTP/RTCP 送受信
5. `TERMINATING`: 停止処理（`Full` は DELETE）
6. `CLOSED`: 終了

## 14. 相互運用性要件
- WHIP endpoint は RFC 9725 の `201 Created + SDP answer` 応答を返すこと
- 受信側は VP8/Opus を受理できること
- SDP のコーデック宣言と RTP 実データが一致すること

## 15. セキュリティ要件
- WHIP endpoint は HTTPS を推奨（平文 HTTP は閉域用途に限定）
- 認証トークン運用（Bearer 等）は `Full` で実装対象とする
- ログに機微情報（トークン、SDP 内秘密情報）を残さない運用を推奨

## 16. 実装チェックリスト
- [ ] `POST application/sdp` と `201` 判定を実装
- [ ] SDP offer/answer 適用を実装
- [ ] VP8 90kHz RTP timestamp を実装
- [ ] Opus 48kHz RTP timestamp を実装
- [ ] PTS ベース pacing と遅延破棄を実装
- [ ] RTCP 受信監視を実装
- [ ] (`Full`) `PATCH` / `DELETE` / `Location` / redirect / ice-server link を実装

## 17. 参考URL
- RFC 9725 (WHIP): https://datatracker.ietf.org/doc/html/rfc9725
- RFC 9429 (JSEP): https://datatracker.ietf.org/doc/html/rfc9429
- RFC 7741 (RTP Payload Format for VP8): https://datatracker.ietf.org/doc/html/rfc7741
- RFC 7587 (RTP Payload Format for Opus): https://datatracker.ietf.org/doc/html/rfc7587
