# net-sim 弱网模拟 使用说明 (v2 / App 2.16.1)

> 本文档取代 `docs/v1.11.0/v1.11.0.md` 中的「功能释义」一节。旧版本里
> "TCP 实际延迟 = latency × 4"、"(叠加)" 等说明都是旧实现的 bug，已修复。

## 一句话口径

**配置多少，测出来就是多少。** 所有数值以客户端（手机 App）端到端测量为准：

| 字段 | 单位 | 含义（你测到的） | 怎么测最直观 |
|---|---|---|---|
| `latency` | ms | 每次往返 **RTT 增加** 这么多 | ping-pong / HTTP 请求耗时 / UDP echo |
| `jitter` | ms | RTT 在 `latency ± jitter` 内波动 | 多次 RTT 的 min/max |
| `loss` | % | **往返丢包率**（与 ping 的 packet loss 同口径） | UDP echo 丢失比例 |
| `bandwidth` | Mbps | 上下行各自的吞吐上限 | 下载 / 上传测速 |
| `upload-bandwidth` | Mbps | 上行上限，覆盖 `bandwidth` | 上传测速 |
| `download-bandwidth` | Mbps | 下行上限，覆盖 `bandwidth` | 下载测速 |
| `corruption` | % | 往返后**内容被改动**的 UDP 包比例（每个坏包翻转 1 bit） | UDP echo 校验 |
| `duplication` | % | 往返后**收到重复**的 UDP 包比例 | UDP echo 去重计数 |
| `reordering` | % | 往返后**晚于后发包到达**的 UDP 包比例 | UDP echo 序号 |
| `burst-loss` | 个 | >1 时丢包成串出现，每串连续丢 N 个；**平均丢包率仍等于 `loss`** | 丢包序号连续段长度 |
| `burst-delay` | ms | 进入"坏时段"时 RTT 额外增加 0 ~ `burst-delay` | RTT 偶发尖刺 |
| `packet-size` | bytes | 路径 MTU：大于它的 UDP 包被丢弃；TCP 按 `packet-size-40` 作为 MSS 计算分段丢包 | 发大/小 UDP 包对比 |
| `queue-type` | - | `fifo` / `prio` / `fair-queue`，目前都按 fifo 执行（预留） | - |

- 1 Mbps = 1,000,000 bit/s = 125,000 B/s（与测速软件一致）。
  旧文档的 "Mbps = KB/s ÷ 128" 不再成立：`download-bandwidth: 3` ≈ 375 KB/s (366 KiB/s)。
- 所有字段默认 0 = 关闭；`enabled: false` 时全部不生效。
- 带宽是**整条链路**的上限：所有连接（TCP+UDP）共享同一个上行桶和下行桶。

## TCP 与 UDP 的差异（这是"真实"的表现，不是 bug）

代理在 TCP 层面看到的是可靠字节流，而真实手机网络上 TCP 丢包/损坏后由协议栈重传，
应用永远拿不到残缺数据。因此 TCP 上的效果是这样模拟的：

| 效果 | UDP | TCP |
|---|---|---|
| latency / jitter / burst-delay | 每个包 | 每个数据块，保持顺序（队头阻塞） |
| loss | 直接丢包 | 每个 MSS 分段按 loss 判定；丢失则数据流**停顿一个 RTO** 后继续（RTO = max(200ms, latency + 2×jitter)），数据完整 |
| corruption | 翻转 1 bit 后送达 | 视为校验失败 → 同 loss（重传停顿） |
| duplication / reordering | 真实重复 / 乱序 | 被 TCP 吸收，应用无感（与真实网络一致） |
| bandwidth | 令牌桶 | 令牌桶 + 背压 |

所以 TCP 下设置 `loss: 2` 你看到的是"变慢、偶发卡顿"，而不是"请求失败"。
想看到"请求失败 / 超时"，请用 UDP 业务（QUIC、DNS、游戏、音视频）或加大 loss。

### 其它说明

- `latency` 按"已建立连接上的一次往返"计算。新 TCP 连接的三次握手在本地完成（代理特性），
  因此一次全新的 HTTP 请求约为 `1 × latency`（真实网络约 2 × RTT，含握手）。
- net-sim 只作用于经过 mihomo 隧道转发的流量（TUN / mixed / socks / http 入站）。
  `dns-hijack` 由内核直接应答的 DNS 不受影响，如需模拟 DNS 延迟用已有的 `/dns/delay`。
- 通过 API 开启 net-sim 后，**新建**的 TCP 连接才会被模拟；已存在的长连接不受影响。
  关闭后已被包装的连接立即恢复直通。

## 配置示例

```yaml
net-sim:
  enabled: true
  latency: 200            # RTT +200ms
  jitter: 50              # RTT 150~250ms
  loss: 1.0               # 往返丢包 1%
  bandwidth: 10           # 上下行各 10 Mbps
  upload-bandwidth: 5     # 上行改为 5 Mbps
  download-bandwidth: 15  # 下行改为 15 Mbps
  corruption: 0.5         # 0.5% UDP 包损坏
  duplication: 0.2        # 0.2% UDP 包重复
  reordering: 5.0         # 5% UDP 包乱序
  burst-loss: 2           # 丢包成对出现，平均仍为 1%
  burst-delay: 100        # 坏时段 RTT 额外 +0~100ms
  packet-size: 1500       # MTU 1500
  queue-type: fifo
```

### 场景模板

```yaml
# A. 普通弱网（日常回归）
net-sim: {enabled: true, latency: 100, jitter: 30, loss: 0.5, upload-bandwidth: 2, download-bandwidth: 5}

# B. 电梯 / 地库（突发断续）
net-sim: {enabled: true, latency: 300, jitter: 100, loss: 5, burst-loss: 5, burst-delay: 800, upload-bandwidth: 0.5, download-bandwidth: 1}

# C. 上行塌陷（直播 / 视频通话发送端）
net-sim: {enabled: true, latency: 150, jitter: 50, loss: 2, burst-loss: 3, upload-bandwidth: 0.3, download-bandwidth: 6}

# D. 2G/EDGE
net-sim: {enabled: true, latency: 600, jitter: 150, loss: 2, upload-bandwidth: 0.1, download-bandwidth: 0.2}

# E. 跨境高延迟但带宽够
net-sim: {enabled: true, latency: 250, jitter: 20, loss: 0.3}
```

## REST API（external-controller）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/net-sim` | 当前配置 |
| PUT | `/net-sim` | 全量替换（未传字段 = 0） |
| PATCH | `/net-sim` | 局部修改（只改传入字段） |
| DELETE | `/net-sim` | 重置并关闭 |
| GET | `/net-sim/stats` | 统计计数 |
| DELETE | `/net-sim/stats` | 清空统计 |

参数越界（如 `loss: 150`、负数、`packet-size` 1~67、非法 `queue-type`）返回 400。
配置文件里写了非法值时，内核打印 error 日志并关闭 net-sim。

```bash
API=http://127.0.0.1:9090        # Android: adb forward tcp:19090 tcp:9090 后用 19090
curl -X PUT   $API/net-sim -H 'Content-Type: application/json' -d '{"enabled":true,"latency":200}'
curl -X PATCH $API/net-sim -H 'Content-Type: application/json' -d '{"jitter":50,"loss":1}'
curl          $API/net-sim
curl -X DELETE $API/net-sim/stats; curl $API/net-sim/stats
curl -X DELETE $API/net-sim
```
（设置了 `secret` 时加 `-H "Authorization: Bearer <secret>"`）

### stats 字段

| key | 含义 |
|---|---|
| `total-packets` | 经过模拟的 UDP 包 + TCP 数据块（每个方向各计一次） |
| `upload-packets` / `download-packets` | 按方向拆分 |
| `dropped-packets` / `upload-dropped` / `download-dropped` | 丢弃（UDP 包 / TCP 分段） |
| `burst-drops` | 其中属于突发丢包的数量 |
| `delayed-packets` | 被延迟的包/块 |
| `burst-delays` | 处于突发延迟时段的包/块 |
| `corrupted-packets` | 损坏（TCP 为校验失败的分段） |
| `duplicated-packets` / `reordered-packets` | UDP 重复 / 乱序 |
| `tcp-retransmits` | TCP 重传停顿次数 |
| `oversize-drops` | 超过 packet-size 被丢弃的 UDP 包 |
| `throttled-bytes` | 经过限速桶的字节数 |

## 验证方法（WSL / Linux）

`docs/net-sim/scripts/netsim_e2e.py`（仅依赖 Python3 标准库）会启动本地 TCP echo / HTTP / UDP echo
服务，通过 mihomo 的 socks5 入站（TCP CONNECT + UDP ASSOCIATE，与 TUN 走同一条隧道代码）
逐项设置 net-sim 并对比"配置值 vs 实测值"：

```bash
# WSL 中，自动拉起内核
python3 netsim_e2e.py --mihomo ./mihomo-linux-amd64

# 测 Android 上的内核：手机与电脑同一局域网
adb forward tcp:19090 tcp:9090
python3 netsim_e2e.py --api http://127.0.0.1:19090 --socks 192.168.8.100:7890 \
    --target-host 192.168.8.101          # 电脑 IP（手机能访问到）
```

2026-11-24 在 WSL2 (Ubuntu 22.04) 上的结果（`data/netsim_e2e_wsl.json`）：

| 项目 | 配置 | 实测 |
|---|---|---|
| TCP latency (RTT) | 200ms | 201.0ms |
| UDP latency (RTT) | 200ms | 201.1ms |
| UDP jitter | 100±40ms | avg 97.5, min 65.9, max 127.3 |
| latency 不再限制吞吐 | 200ms | 32MB 下载 332 Mbps |
| download-bandwidth | 2 / 8 / 20 Mbps | 2.01 / 8.05 / 20.13 Mbps |
| upload-bandwidth | 4 Mbps | 4.03 Mbps |
| UDP loss (往返) | 10% | 10.40% |
| UDP burst loss 平均 | 6%, 每串 4 | 5.16% |
| UDP corruption | 5% | 4.57% |
| UDP duplication | 5% | 4.87% |
| UDP reordering | 10% | 9.33% |
| packet-size | 500B | 400B 包 50/50 通过, 600B 包 0/50 |
| TCP loss 数据完整性 | 2% + 40ms | sha256 一致, tcp-retransmits=30 |
| API PATCH / 校验 / DELETE | - | 全部正确 |

概率类指标是统计值：3000 个样本时有约 ±0.5~1% 的随机波动（突发丢包每次连丢 N 个，波动更大），样本越多越接近配置值；单元测试用 5~8 万个样本时误差在 ±0.3% 内。

同一套脚本测旧版内核（2026-02 的 `clash-linux-amd64-net-sim`）：

| 配置 | 旧版实测 | 原因 |
|---|---|---|
| latency=200 (TCP) | RTT 801ms | Read 和 Write 两侧各加一次延迟 × 两个方向 |
| latency=200 (UDP) | RTT 401ms | latency 每方向全额加一次 |
| latency=200 下载 2MB | 26.5s (0.63 Mbps) | 同步 sleep：每个数据块都要等一次延迟，延迟直接变成限速 |
| download-bandwidth=8 | 8.69 Mbps | Mbps 按 1024×1024 计算 |
| UDP loss=10 | 19.03% | 每方向全额判定一次 |
| TCP loss=1 | 5 次下载 2 次数据损坏、1 次超时 | 直接丢弃字节流中的数据块 |
| UDP corruption=0.5 | 100% 包损坏 | 未按概率判定，每个包都至少翻转 1 bit |

## 代码位置

- `component/netsim/` 全部实现（config / engine / delayline / conn(TCP) / udp / bandwidth）
- 接入点（尽量少改上游代码）：
  - `tunnel/connection.go`：`handleSocket` 包装 TCP，`handleUDPToRemote` / `handleUDPToLocal` 接 UDP
  - `config/config.go`、`hub/executor/executor.go`：解析与下发配置
  - `hub/route/netsim.go`、`hub/route/server.go`：REST API
