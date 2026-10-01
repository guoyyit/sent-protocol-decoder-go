# SENT Protocol Decoder (Go)

面向车载传感器的 SENT（SAE J2716 风格）协议接收与诊断库。信号源完全由
内存管道与确定性模拟器提供，支持注入丢帧、错帧与脉冲抖动，不依赖真实
硬件或外部服务。

包名：`sent`（模块根目录）。

## 快速上手

```go
tick := 3 * time.Microsecond

// 1) 用固定种子模拟一个传感器并发帧边沿。
sensor := sent.NewSensor(42, sent.SensorConfig{Variant: sent.VariantA, Tick: tick})
samples := sensor.Emit(1, 0, statusNibble, dataNibbles, sent.Inject{})

// 2a) 单路、确定性的同步解码核心（也用于回放）。
spec := sent.ChannelSpec{
    Name:  "throttle",
    Frame: sent.DefaultFrameConfig(tick, 0.15, sent.VariantA),
    Physical: sent.PhysicalConfig{Signals: []sent.SignalDef{{
        Name: "position", BitOffset: 0, BitLength: 12, Scale: 0.05,
    }}},
    Fault: sent.FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2},
}
pipeline := sent.NewPipeline(spec) // 测试中可用 newPipeline
for _, s := range samples {
    for _, ev := range pipeline.ProcessSample(s) {
        // ev.Kind == EvtReading / EvtSlowMessage / EvtFrameError ...
    }
}

// 2b) 多路并发接收：每路独立 goroutine，动态增删。
recv := sent.NewReceiver(1024)
recv.AddChannel(spec)
go func() {
    for ev := range recv.Events() { /* 统一多路输出 */ }
}()
for _, s := range samples { recv.Feed("throttle", s) }
recv.RemoveChannel("throttle") // 完整回收该路处理状态
```

## 帧格式

每个快速通道帧由 9 个以下降沿间隔（脉冲）计量的节拍组成：

| 顺序 | 节拍 | 宽度（tick） |
| --- | --- | --- |
| 0 | 同步脉冲 sync | 56 |
| 1 | 状态半字节 status | 12..27（编码值 = 宽度 − 12） |
| 2..7 | 六个数据半字节 d0..d5 | 12..27 |
| 8 | 校验半字节 CRC | 12..27 |

- 六个数据半字节按帧顺序拼成 24 位快速通道值：`FastValue = d0<<20 | d1<<16
  | ... | d5`（`FastValue()`）。
- 帧与帧之间可带可选 pause 脉冲（`SensorConfig.PauseTicks`）；解码器在每个
  合法 56-tick 同步脉冲处重新同步，因此 pause、噪声或单帧损坏都不会造成
  帧错位。
- 状态半字节：低 2 位承载慢速通道的 2 个比特；高 2 位承载传感器错误码
  （见“故障分类”）。

### 变体（Variant）

| 变体 | CRC 多项式（低 4 位系数） | 慢速通道格式 |
| --- | --- | --- |
| `VariantA` | `0x3`（x^4 + x + 1） | `SlowEnhanced`，16 帧 / 32 位 |
| `VariantB` | `0xD`（x^4 + x^3 + x^2 + 1） | `SlowShort`，6 帧 / 12 位 |

CRC 覆盖 status + 6 个数据半字节，初值为 0，按半字节移入、每字节推进 4
位。把返回的 CRC 半字节追加到输入再计算结果为 0（`CRC4` / `verifyCRC`）。

当实际到达的帧用**另一个**变体的多项式才能校验通过时：

- 首次检测到时发出一个 `EvtVariantMismatch`（只报一次）；
- 每一帧仍作为 `EvtFrameError{Kind: ErrVariantMismatch}` 被拒绝，且**不产出
  任何读数**；
- 进行中的慢速通道拼装被以 `SlowResetOnVariant` 原因丢弃，避免跨变体拼接。

## 慢速通道

每个快速帧在状态半字节低 2 位携带 2 个慢速比特。消息按帧序列严格对齐，
第 `i` 条消息占用帧 `i*L+1 .. (i+1)*L`，其中 L 为 6（短）或 16（增强）：

- `SlowShort`（6 帧，12 位）：4 位 ID + 8 位数据，高位先发。
- `SlowEnhanced`（16 帧，32 位）：`0001` 前导 + 4 位慢速 CRC + 4 位 ID +
  16 位数据，高位先发。慢速 CRC 使用当前变体多项式，覆盖 ID 与 4 个数据
  半字节。

拼装器依赖帧序列标签（采集侧的带外元数据，见 `Sample.FrameSeq`）做连续性
检查，因此以下情况都会丢弃当前拼装且**绝不会产生错位消息**，原因可区分：

| `SlowDropReason` | 含义 |
| --- | --- |
| `SlowDropLoss` | 期望的帧序号未到达（中间缺帧） |
| `SlowDropDuplicate` | 同一帧序号再次到达 |
| `SlowDropOutOfOrder` | 到达了比最近一帧更旧的序号 |
| `SlowResetOnVariant` | 变体不匹配拒绝后重置 |
| `SlowPreambleInvalid` | 增强消息的前导半字节不是 `0001` |
| `SlowCRCInvalid` | 增强消息的慢速 CRC 校验失败 |

编码辅助：`EncodeSlowShort(id, data)`、`EncodeSlowEnhanced(variant, id,
data)`。

## 容差与时序合法性

`FrameConfig.Tolerance` 是**每个脉冲**相对其整数 tick 标称宽度的允许相对
偏差（如 `0.15` = ±15%）。解码先把脉冲宽度四舍五入到最近整数 tick，再：

| 判定 | 条件 | 错误 |
| --- | --- | --- |
| 同步脉冲 | 取整为 56 tick 且偏差 ≤ 容差 | 否则 `ErrSyncMissing` |
| 非法半字节 | 取整 tick 数超出 12..27 | `ErrIllegalNibble` |
| 脉冲超差 | 合法 tick 数但偏差 > 容差 | `ErrPulseOutOfTolerance` |
| 校验 | CRC 半字节不匹配 | `ErrCRC`（见变体不匹配） |

任何时序错误都会中止当前帧并回到“等同步”状态，后续脉冲在新的合法同步
脉冲处恢复。校验/变体失败的帧不产生读数，且会在慢速通道体现为帧丢失。

## 故障分类

状态半字节高 2 位（`SensorStatus.Code`）：

| 码值 | 常量 | 语义 |
| --- | --- | --- |
| 0 | `SensorOK` | 正常 |
| 1 | `SensorTransient` | 瞬时、可自愈合错误 |
| 2 | `SensorHard` | 严重但可恢复错误 |
| 3 | `SensorPersist` | 持续故障 |

去抖后的通道状态（`FaultStatus.State`，通过 `EvtFaultChange` 观测）：

- `FaultTransient`：出现瞬时码，或连续 hard 码尚未达到阈值；瞬时码**不会
  锁存**，并在**首个正常帧**立即回到 `FaultOK`。
- `FaultPersistent`：收到 `SensorPersist` 立即锁存；或连续 `SensorHard`
  达到 `PersistThreshold`（默认 3）后锁存。
- 恢复：持续故障后连续 `SensorOK` 达到 `RecoverThreshold`（默认 2）才清除；
  瞬时码会打断 hard 码的连续计数。

每个有效读数上的 `ChannelReading.Sensor` 仍逐帧携带原始的瞬时标志，保证
瞬时事件即使不锁存也可观测。

## 并发与隔离

`Receiver` 为每个通道维护独立的输入队列、事件队列、处理 goroutine 与转发
goroutine：

- 下游消费者过慢时，事件先积压在该通道自己的队列/溢出缓冲中，不会反压
  其他通道；`Feed` 在该通道输入队列满时返回 `ErrSlowConsumer`，调用方可
  重试或仅丢弃该路样本。
- `AddChannel` / `RemoveChannel` 可在运行中动态调用；`RemoveChannel`
  等待该路 goroutine 全部退出，其解码器、故障跟踪器、慢速拼装器和队列随之
  回收（`-race` 测试覆盖了无泄漏）。

## 确定性与回放

- 模拟器使用固定种子的 PCG（`NewSensor(seed, cfg)`）；相同种子与输入生成
  逐条一致的边沿时间序列。
- `Recording` 可用 gob 字节级 `Save` / `LoadRecording`；`Replay()` 同步地
  按 `(时间戳, 通道索引, 输入顺序)` 归并多路样本，输出确定有序的事件切片，
  适合 golden 比对与历史复现。

## 错误与事件总览

- 事件：`EvtReading`、`EvtSlowMessage`、`EvtFrameError`、`EvtSlowDrop`、
  `EvtVariantMismatch`、`EvtFaultChange`。
- 帧错误：`ErrCRC`、`ErrPulseOutOfTolerance`、`ErrSyncMissing`、
  `ErrIllegalNibble`、`ErrVariantMismatch`。

## 测试

`go test ./...`（建议加 `-race`）覆盖：帧错误分类、慢速通道跨帧拼接与
丢弃原因、变体切换与不匹配、瞬时/持续故障判定与恢复、并发多路隔离与
动态回收、确定性回放与 gob 往返。
