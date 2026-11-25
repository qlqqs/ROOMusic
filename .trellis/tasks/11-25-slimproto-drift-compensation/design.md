# 设计

## 组件

| 位置 | 内容 |
| --- | --- |
| `zones` | 新接口 `DriftCompensator`。`Options` 新增可选的成员流过滤工厂，渲染层实现。`groupStreamInfo` 为每个非主设备的读者包一层，`GroupMemberStream` 新增 `Drift DriftCompensator` 字段（主设备为 nil） |
| `playback/render`（或新子包） | 补偿器实现：ffmpeg 解码（FLAC→s32le，或保持源位深）→ Go 插删 → ffmpeg 编码（参数与 `encode.go` 的 FLAC 一致，含 `>48 kHz` 时 `-frame_size 4096`）。进程走现有预约 |
| `devices/slimproto` | 协调器在 `strm s` 时记下每台成员的 `Drift`；`correct()` 换成 R4/R5 的控制；位置回报与 `apparentStart` 用源时间轴 |
| `app` | 接线 |

接口草案（实现时可调整命名，语义不变）：

```go
// DriftCompensator 由成员流过滤器实现，帧数以该流采样率计。
type DriftCompensator interface {
    // Adjust 追加净调整：正数删帧（成员追赶），负数插帧（成员等待）。
    Adjust(frames int64)
    // Source 把成员已输出的帧号换算为源帧号（含已发生的全部调整）。
    Source(output int64) int64
    // Pending 返回已请求但尚未写出、或写出位置在 output 之后的净调整。
    Pending(output int64) int64
}
```

## 数据流

1. 分发器给成员的读者输出共享的 FLAC 字节，补偿器从中解码。
2. 插删器按队列中的欠量，每隔至少 2000 帧调整 1 帧，并记录 `(输出帧号, 累计调整)`。
3. 编码后的输出给成员的 HTTP 取流。

## 时间轴

- 成员回报的 elapsed 换成输出帧号 `elapsed × rate`，经 `Source` 得到源帧号，再换回时间，用来计算 `apparentStart`。主设备不换算。
- 欠量 = 源时间轴上成员相对主设备的落后量（帧）− `Pending(当前输出帧)`。`|欠量| > 0.5 ms` 时调用 `Adjust`；`|落后| > 20 ms` 时走 `strm a`，并清空待生效的调整。
- 接续流和换流时，补偿器与成员流一起新建，计数从 0 开始。

## 插值

- 删帧：把第 k、k+1 帧合并为一帧，取两者的均值，输出少一帧。
- 插帧：在第 k、k+1 帧之间插入两者的均值，输出多一帧。
- 都按声道、按样本整数运算，不做抖动。

## 失败与资源

- 解码或编码进程退出、插删器出错：关闭这台成员的读者，走分发器已有的剔除回报，成员标为 `dropped`。
- 不在编组里的单台播放不经过补偿器。
